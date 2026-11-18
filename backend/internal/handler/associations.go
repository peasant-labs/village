package handler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// ErrAssociationBinding marks a producer-visible association ledger conflict.
// The caller maps it to a 422 rather than masking an immutable identity violation
// as an internal server failure.
var ErrAssociationBinding = errors.New("association ledger binding rejected")

// sessionPublishLockKey names the advisory lock that serializes work on ONE
// owner's source session. It is the only key shape that guards a transcript, so
// the single-transcript and whole-project paths contend on the same string
// rather than each formatting their own and never seeing each other.
func sessionPublishLockKey(ownerID pgtype.UUID, localID string) string {
	return database.SessionPublishLockKey(ownerID, localID)
}

// associationPublishLockKey names the advisory lock that serializes work on one
// durable association.
func associationPublishLockKey(ownerID pgtype.UUID, associationID schema.AssociationID) string {
	return fmt.Sprintf("village:association-publish:%x:association:%s", ownerID.Bytes, associationID)
}

// withPublishLocks serializes publishes that could mutate the same source
// transcript or durable association. The locks and callback share one pooled
// connection so they span preflight validation, encrypted object storage, and persistence without
// opening a transaction over network I/O. Sorted keys avoid lock cycles.
func (h *Handler) withPublishLocks(ctx context.Context, ownerID pgtype.UUID, localID string, associations []schema.PublishedAssociation, fn func(conn *pgxpool.Conn) error) error {
	keys := make([]string, 0, len(associations)+1)
	keys = append(keys, sessionPublishLockKey(ownerID, localID))
	for _, association := range associations {
		keys = append(keys, associationPublishLockKey(ownerID, association.ID))
	}
	return h.withPublishLockKeys(ctx, keys, fn)
}

// withPublishLocksMany serializes work that spans SEVERAL of one owner's source
// sessions - a whole project offered to a collective in one request. It holds
// the same per-session keys the single-transcript path holds, acquired in the
// same sorted order on one pinned connection, so a batch and a concurrent single
// share contend rather than interleave, and no two callers can take two keys in
// opposite orders.
func (h *Handler) withPublishLocksMany(ctx context.Context, ownerID pgtype.UUID, localIDs []string, fn func(conn *pgxpool.Conn) error) error {
	keys := make([]string, 0, len(localIDs))
	for _, localID := range localIDs {
		keys = append(keys, sessionPublishLockKey(ownerID, localID))
	}
	return h.withPublishLockKeys(ctx, keys, fn)
}

// withPublishLockKeys is the one implementation both entry points share: keys
// are deduplicated and sorted, acquired on a single pinned pool connection, and
// released - or the connection evicted - by the same cleanup path.
func (h *Handler) withPublishLockKeys(ctx context.Context, keys []string, fn func(conn *pgxpool.Conn) error) error {
	if h.pool == nil {
		return fn(nil)
	}
	orderedKeys := make([]string, 0, len(keys))
	seenKeys := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if _, exists := seenKeys[key]; exists {
			continue
		}
		seenKeys[key] = struct{}{}
		orderedKeys = append(orderedKeys, key)
	}
	sort.Strings(orderedKeys)

	conn, err := h.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire publish lock connection: %w", err)
	}
	attempted := make([]string, 0, len(orderedKeys))
	defer func() { releasePublishLockConnection(conn, attempted) }()
	for _, key := range orderedKeys {
		// Record before Exec: cancellation can race with the server granting the
		// session lock and make the client unable to observe whether it succeeded.
		attempted = append(attempted, key)
		if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(hashtextextended($1, 0))", key); err != nil {
			return fmt.Errorf("acquire publish lock %q: %w", key, err)
		}
	}
	return fn(conn)
}

// releasePublishLockConnection never returns a potentially lock-owning
// physical session to the pool when unlock could not be proved.
func releasePublishLockConnection(conn *pgxpool.Conn, attempted []string) {
	unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unlockFailed := false
	for i := len(attempted) - 1; i >= 0; i-- {
		var didUnlock bool
		if err := conn.QueryRow(unlockCtx, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", attempted[i]).Scan(&didUnlock); err != nil {
			unlockFailed = true
			slog.Error("publish advisory-lock cleanup could not prove the physical session is unlocked",
				"operation", "publish", "stage", "advisory_unlock",
				"lock_scope_digest", fmt.Sprintf("%x", sha256.Sum256([]byte(attempted[i]))),
				"consequence", "the pooled connection will be evicted instead of reused",
				"remediation", "inspect PostgreSQL connectivity and advisory-lock health before retrying the publish",
				"error", err)
		}
		_ = didUnlock
	}
	if unlockFailed {
		physical := conn.Hijack()
		if err := physical.Close(unlockCtx); err != nil {
			slog.Error("evicted publish connection could not be closed after uncertain advisory-lock cleanup",
				"operation", "publish", "stage", "physical_connection_close",
				"consequence", "PostgreSQL must reap the detached session before its locks are certainly released",
				"remediation", "inspect PostgreSQL sessions and network health; terminate the detached backend if it remains",
				"error", err)
		}
		return
	}
	conn.Release()
}

// tryPublishConnectionWait bounds how long an optional rewrite waits for a
// pooled connection. pgxpool has no non-blocking acquire, so the wait is short
// and a saturated pool is skipped without waiting at all.
const tryPublishConnectionWait = 50 * time.Millisecond

// publishLockSkips counts optional rewrites skipped because no connection or
// lock was free, so a read path that stops migrating under load is visible.
var publishLockSkips atomic.Int64

// withTryPublishLock never waits for the transcript's advisory lock and waits at
// most tryPublishConnectionWait for one pooled connection, taking only that one.
// Optional read repair can be skipped safely; each skip is counted and logged
// at debug level.
func (h *Handler) withTryPublishLock(ctx context.Context, ownerID pgtype.UUID, localID string, fn func(*pgxpool.Conn) error) (bool, error) {
	if h.pool == nil {
		return true, fn(nil)
	}
	skip := func(reason string) (bool, error) {
		slog.Debug("optional publish-locked rewrite skipped",
			"reason", reason, "skipped_total", publishLockSkips.Add(1),
			"consequence", "the stored generation is read as it is and the rewrite is retried by a later read")
		return false, nil
	}
	if stat := h.pool.Stat(); stat.IdleConns() == 0 && stat.TotalConns() >= stat.MaxConns() {
		return skip("pool_saturated")
	}
	acquireCtx, cancel := context.WithTimeout(ctx, tryPublishConnectionWait)
	conn, err := h.pool.Acquire(acquireCtx)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return skip("no_connection_available")
	}
	key := sessionPublishLockKey(ownerID, localID)
	defer releasePublishLockConnection(conn, []string{key})
	var acquired bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1, 0))", key).Scan(&acquired); err != nil {
		return false, err
	}
	if !acquired {
		return skip("lock_held")
	}
	return true, fn(conn)
}

// validatePublishedAssociationBindings resolves every requested association before
// the publish path mutates its transcript row. Existing associations are immutable:
// an exact owner/id/transcript/hash replay is a no-op; a rebind or a second ID for
// the same owner/transcript/hash is rejected with remediation.
func validatePublishedAssociationBindings(ctx context.Context, q Querier, ownerID, transcriptID pgtype.UUID, associations []schema.PublishedAssociation) ([]schema.PublishedAssociation, error) {
	if len(associations) == 0 {
		return nil, nil
	}

	associationIDs := make([]string, 0, len(associations))
	observedHashes := make([]string, 0, len(associations))
	requestedByID := make(map[string]schema.PublishedAssociation, len(associations))
	requestedByHash := make(map[string]schema.PublishedAssociation, len(associations))
	for _, association := range associations {
		id := association.ID.String()
		if prior, exists := requestedByID[id]; exists {
			return nil, fmt.Errorf("%w: association %q appears more than once with observed commits %q and %q; each publish request must name a durable association once", ErrAssociationBinding, id, prior.ObservedCommitHash, association.ObservedCommitHash)
		}
		if prior, exists := requestedByHash[association.ObservedCommitHash]; exists {
			return nil, fmt.Errorf("%w: observed commit %q appears under both association %q and %q; one owner/transcript/observed-commit relationship has one durable ID", ErrAssociationBinding, association.ObservedCommitHash, prior.ID, association.ID)
		}
		requestedByID[id] = association
		requestedByHash[association.ObservedCommitHash] = association
		associationIDs = append(associationIDs, id)
		observedHashes = append(observedHashes, association.ObservedCommitHash)
	}

	byIDRows, err := q.ListTranscriptAssociationsByOwnerAndIDs(ctx, sqlc.ListTranscriptAssociationsByOwnerAndIDsParams{
		OwnerID:        ownerID,
		AssociationIds: associationIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve published associations for authenticated owner: %w", err)
	}
	byID := make(map[string]sqlc.TranscriptAssociation, len(byIDRows))
	for _, row := range byIDRows {
		byID[row.AssociationID] = row
	}

	byRelationship := map[string]sqlc.TranscriptAssociation{}
	if transcriptID.Valid {
		byRelationshipRows, err := q.ListTranscriptAssociationsByOwnerTranscriptAndObservedCommitHashes(ctx, sqlc.ListTranscriptAssociationsByOwnerTranscriptAndObservedCommitHashesParams{
			OwnerID:              ownerID,
			TranscriptID:         transcriptID,
			ObservedCommitHashes: observedHashes,
		})
		if err != nil {
			return nil, fmt.Errorf("resolve observed commits for authenticated owner: %w", err)
		}
		for _, row := range byRelationshipRows {
			byRelationship[row.ObservedCommitSha] = row
		}
	}

	newAssociations := make([]schema.PublishedAssociation, 0, len(associations))
	for _, association := range associations {
		if existing, exists := byID[association.ID.String()]; exists {
			if existing.TranscriptID != transcriptID || existing.ObservedCommitSha != association.ObservedCommitHash {
				return nil, fmt.Errorf("%w: association %q is already bound to a different transcript or observed commit; durable association IDs cannot be rebound; reuse its original transcript/hash binding or publish a new association ID", ErrAssociationBinding, association.ID)
			}
			continue
		}
		if existing, exists := byRelationship[association.ObservedCommitHash]; exists {
			return nil, fmt.Errorf("%w: observed commit %q is already recorded as association %q; one owner/transcript/observed-commit relationship has one durable ID; reuse %q instead of creating an alias", ErrAssociationBinding, association.ObservedCommitHash, existing.AssociationID, existing.AssociationID)
		}
		newAssociations = append(newAssociations, association)
	}
	return newAssociations, nil
}

func insertPublishedAssociationBindings(ctx context.Context, q Querier, ownerID, transcriptID pgtype.UUID, associations []schema.PublishedAssociation) error {
	if len(associations) == 0 {
		return nil
	}
	type associationRecord struct {
		AssociationID      string `json:"association_id"`
		ObservedCommitHash string `json:"observed_commit_sha"`
	}
	records := make([]associationRecord, 0, len(associations))
	for _, association := range associations {
		records = append(records, associationRecord{
			AssociationID:      association.ID.String(),
			ObservedCommitHash: association.ObservedCommitHash,
		})
	}
	items, err := json.Marshal(records)
	if err != nil {
		return fmt.Errorf("encode published association bindings for storage: %w", err)
	}
	if err := q.InsertTranscriptAssociations(ctx, sqlc.InsertTranscriptAssociationsParams{
		OwnerID:      ownerID,
		TranscriptID: transcriptID,
		Items:        items,
	}); err != nil {
		return fmt.Errorf("append %d association bindings for transcript: %w", len(records), err)
	}
	return nil
}

// resolveAssociationTargets confirms every association target belongs to the
// authenticated owner. It is called inside UploadAnnotations' one transaction so
// an unknown or foreign target aborts the entire batch before the upsert runs.
func resolveAssociationTargets(ctx context.Context, q Querier, ownerID pgtype.UUID, annotations []schema.AnnotationPushItem) error {
	associationIDs := make([]string, 0, len(annotations))
	seen := make(map[string]struct{}, len(annotations))
	for _, annotation := range annotations {
		if annotation.TargetKind != schema.TargetAssociation {
			continue
		}
		associationID := annotation.TargetAssociationID
		if associationID == nil {
			// The module validator normally rejects this before the transaction. Keep
			// this fail-closed guard at the storage boundary for direct callers.
			return fmt.Errorf("%w: association target is missing targetAssociationId; the target cannot be authorized; send exactly one valid targetAssociationId", ErrAssociationBinding)
		}
		if _, exists := seen[associationID.String()]; !exists {
			seen[associationID.String()] = struct{}{}
			associationIDs = append(associationIDs, associationID.String())
		}
	}
	if len(associationIDs) == 0 {
		return nil
	}
	resolvedIDs, err := q.ListTranscriptAssociationIDsByOwner(ctx, sqlc.ListTranscriptAssociationIDsByOwnerParams{
		OwnerID:        ownerID,
		AssociationIds: associationIDs,
	})
	if err != nil {
		return fmt.Errorf("resolve association targets for authenticated owner: %w", err)
	}
	resolved := make(map[string]struct{}, len(resolvedIDs))
	for _, id := range resolvedIDs {
		resolved[id] = struct{}{}
	}
	for _, associationID := range associationIDs {
		if _, exists := resolved[associationID]; !exists {
			return fmt.Errorf("%w: association target %q is not recorded for the authenticated owner; annotations may target only the owner's published association ledger; publish the exact association first", ErrAssociationBinding, associationID)
		}
	}
	return nil
}
