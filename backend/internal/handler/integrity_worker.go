package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/scry"
)

const (
	// integrityTickInterval is the longest wait between two passes; a nudge
	// starts one sooner.
	integrityTickInterval = 30 * time.Second
	// integrityBatchSize is how many transcripts one pass checks at most.
	integrityBatchSize = 10
	// integrityCallSpacing is the pause between two checks in one pass.
	integrityCallSpacing = time.Second
	// backgroundCheckTimeout bounds one check call.
	backgroundCheckTimeout = 30 * time.Second
	// scryHealthTimeout bounds the health call that starts each pass.
	scryHealthTimeout = 3 * time.Second
	// maxIntegrityAttempts is how many failed attempts one content gets
	// under one checker version before it is left failed.
	maxIntegrityAttempts = 6
	// maxIntegrityBackoff caps the wait after a failed attempt.
	maxIntegrityBackoff = 6 * time.Hour
	// integrityAttemptHold is how long a taken attempt is held. One
	// transcript's work runs under this bound, so an attempt interrupted by a
	// shutdown or a crash is due again once it passes.
	integrityAttemptHold = 2 * backgroundCheckTimeout
)

// integrityBackoff is the wait after the given number of failed attempts.
func integrityBackoff(attempts int32) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	// 1 minute << 9 is already past the cap; stop shifting before it can
	// overflow.
	if attempts >= 9 {
		return maxIntegrityBackoff
	}
	return min(time.Minute<<attempts, maxIntegrityBackoff)
}

// Outcome classes besides the service's own failure classes, which are the
// scry.ErrorClass values. Every class but notCandidate and checked is also a
// last_error value.
const (
	outcomeChecked      = "checked"
	outcomeLegacy       = "legacy_content"
	outcomeReadFailed   = "read_failed"
	outcomeNotCandidate = "not_candidate"
	// outcomeSecretRefused is a configuration failure, not the content's:
	// the service refused the shared secret, so the attempt is given back
	// uncounted and the pass ends.
	outcomeSecretRefused = "secret_refused"
	// outcomeStoreFailed is an answer village could not store. It counts as a
	// failed attempt with no last_error class: the content was not at fault.
	outcomeStoreFailed = "store_failed"
)

// checkOutcome is how one transcript's check ended: checked, skipped as
// legacy content, not a candidate any more (it left the checked set or its
// content changed before the read, so nothing was sent), or failed with a
// closed class. Status is the service's HTTP status on a failed call.
type checkOutcome struct {
	Class  string
	Status int
}

// sent reports whether the outcome followed a call to the service.
func (o checkOutcome) sent() bool {
	switch o.Class {
	case outcomeLegacy, outcomeReadFailed, outcomeNotCandidate, outcomeSecretRefused, "":
		return false
	default:
		return true
	}
}

// StartIntegrityWorker starts the background check when it is configured.
// The work is derived from state, so a worker that starts after a restart
// picks up everything an earlier one left unfinished.
func (h *Handler) StartIntegrityWorker() {
	s := h.integrity
	if s == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(integrityTickInterval)
		defer ticker.Stop()
		for {
			if err := h.integrityTick(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("integrity check pass failed", "operation", "integrity_check", "consequence", "the transcripts it could not finish are retried on a later pass", "error", err.Error())
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-s.wake:
			}
		}
	}()
}

// DrainIntegrity stops the background check and waits, until ctx ends, for a
// check already in flight to finish and be recorded.
func (h *Handler) DrainIntegrity(ctx context.Context) error {
	s := h.integrity
	if s == nil {
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("integrity check still in flight at shutdown: %w", ctx.Err())
	}
}

// nudgeIntegrity tells the background check that a transcript may have
// entered the checked set or changed its content, so the next pass need not
// wait for the timer. It never blocks, and a missed nudge loses nothing: the
// work is derived from state on every pass.
func (h *Handler) nudgeIntegrity() {
	if h.onIntegrityNudge != nil {
		h.onIntegrityNudge()
	}
	if h.integrity == nil {
		return
	}
	select {
	case h.integrity.wake <- struct{}{}:
	default:
	}
}

// integrityTick runs one pass: ask the service its version, list what has no
// answer under the held version, and check each transcript in turn. When the
// service does not answer health the pass ends before it writes anything.
func (h *Handler) integrityTick(ctx context.Context) error {
	s := h.integrity
	if s == nil {
		return nil
	}
	healthCtx, cancel := context.WithTimeout(ctx, scryHealthTimeout)
	health, err := s.checker.Health(healthCtx)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		slog.Info("integrity check service did not answer health", "outcome", string(scry.Classify(err)), "status", scryStatus(err))
		return nil
	}
	s.observeVersion(health.ScryVersion)

	// The pass lists and records under the held version. When an answer
	// moves it forward, the rest of the pass lists again under the newer
	// version, so a sweep starts without waiting for the next health call.
	// One pass checks at most integrityBatchSize transcripts in all.
	var errs []error
	pause := false
	budget := integrityBatchSize
	for budget > 0 {
		version, _ := s.currentVersion()
		rows, err := h.queries.ListIntegrityCandidates(ctx, sqlc.ListIntegrityCandidatesParams{ScryVersion: version, MaxAttempts: maxIntegrityAttempts, Batch: int32(budget)})
		if err != nil {
			if ctx.Err() != nil {
				return errors.Join(errs...)
			}
			return errors.Join(append(errs, fmt.Errorf("list integrity candidates: %w", err))...)
		}
		if h.onIntegrityCandidates != nil {
			h.onIntegrityCandidates()
		}
		advanced := false
		for _, row := range rows {
			if ctx.Err() != nil {
				return errors.Join(errs...)
			}
			if pause && s.callSpacing > 0 {
				timer := time.NewTimer(s.callSpacing)
				select {
				case <-ctx.Done():
					timer.Stop()
					return errors.Join(errs...)
				case <-timer.C:
				}
			}
			budget--
			sent, stop, err := h.checkCandidate(ctx, version, row)
			pause = sent
			if err != nil {
				errs = append(errs, err)
			}
			if stop {
				// The service refused the secret; every other check would
				// be refused too, and none of them says anything about
				// content.
				return errors.Join(errs...)
			}
			if held, _ := s.currentVersion(); held != version {
				advanced = true
				break
			}
		}
		if !advanced {
			break
		}
	}
	return errors.Join(errs...)
}

// checkCandidate takes the attempt for one listed transcript, checks it and
// records how it ended. The work runs on a context the shutdown does not
// cancel, bounded by the attempt's hold, so a check in flight finishes and is
// recorded while the drain waits for it. stop reports that the pass must end.
func (h *Handler) checkCandidate(ctx context.Context, version string, row sqlc.Transcript) (sent, stop bool, err error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), integrityAttemptHold)
	defer cancel()
	hash := row.ContentHash.String
	attempts, err := h.queries.ClaimIntegrityAttempt(ctx, sqlc.ClaimIntegrityAttemptParams{
		TranscriptID: row.ID,
		ContentHash:  hash,
		ScryVersion:  version,
		HoldSeconds:  integrityAttemptHold.Seconds(),
		MaxAttempts:  maxIntegrityAttempts,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Another instance holds it, or it finished since the listing.
		return false, false, nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		// The transcript was deleted since the listing.
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("take the integrity attempt for transcript %s: %w", uuidFromPg(row.ID), err)
	}

	resp, outcome, err := h.checkTranscript(ctx, row)
	if err != nil {
		// A failure inside village after the attempt was taken still counts
		// toward the cap and backs off, so the content is not resent every
		// hold.
		slog.Warn("integrity check could not finish", "transcript_id", uuidFromPg(row.ID).String(), "scry_version", version, "consequence", "the attempt backs off and counts toward the attempt cap", "error", err.Error())
		if outcome.Class == outcomeChecked {
			outcome.Class = outcomeStoreFailed
		} else {
			outcome = checkOutcome{Class: outcomeReadFailed}
		}
	}
	slog.Info("integrity check", "transcript_id", uuidFromPg(row.ID).String(), "scry_version", version, "verdict", string(resp.Verdict), "outcome", outcome.Class, "status", outcome.Status)

	key := func(status, lastError string, retryAfter time.Duration) sqlc.CompleteIntegrityAttemptParams {
		return sqlc.CompleteIntegrityAttemptParams{
			Status:            status,
			LastError:         pgtype.Text{String: lastError, Valid: lastError != ""},
			RetryAfterSeconds: retryAfter.Seconds(),
			TranscriptID:      row.ID,
			ContentHash:       hash,
			ScryVersion:       version,
		}
	}
	var completion sqlc.CompleteIntegrityAttemptParams
	switch outcome.Class {
	case outcomeNotCandidate, outcomeSecretRefused:
		stop := outcome.Class == outcomeSecretRefused
		if err := h.queries.ReleaseIntegrityAttempt(ctx, sqlc.ReleaseIntegrityAttemptParams{TranscriptID: row.ID, ContentHash: hash, ScryVersion: version}); err != nil {
			return false, stop, fmt.Errorf("release the integrity attempt for transcript %s: %w", uuidFromPg(row.ID), err)
		}
		return false, stop, nil
	case outcomeChecked:
		completion = key("done", "", 0)
	case outcomeLegacy:
		completion = key("skipped", outcomeLegacy, 0)
	case string(scry.ClassTooLarge):
		completion = key("skipped", outcome.Class, 0)
	default:
		lastError := outcome.Class
		if lastError == outcomeStoreFailed {
			lastError = ""
		}
		if attempts >= maxIntegrityAttempts {
			completion = key("failed", lastError, 0)
		} else {
			completion = key("pending", lastError, integrityBackoff(attempts))
		}
	}
	if err := h.queries.CompleteIntegrityAttempt(ctx, completion); err != nil {
		return outcome.sent(), false, fmt.Errorf("record the integrity attempt for transcript %s: %w", uuidFromPg(row.ID), err)
	}
	return outcome.sent(), false, nil
}

// checkTranscript checks one listed transcript's current content. It sends
// nothing unless the transcript is still in the checked set and still holds
// the listed content when it is read, and it sends the session detail exactly
// as the content route serves it for that content, so flagged positions index
// that payload. An answer that arrives after the transcript left the set is
// still stored: the check was asked for while that copy was readable by
// others.
func (h *Handler) checkTranscript(ctx context.Context, row sqlc.Transcript) (schema.ScryCheckResponse, checkOutcome, error) {
	var none schema.ScryCheckResponse
	hash := row.ContentHash.String
	fresh, err := h.queries.GetTranscriptByID(ctx, row.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return none, checkOutcome{Class: outcomeNotCandidate}, nil
	}
	if err != nil {
		return none, checkOutcome{Class: outcomeReadFailed}, fmt.Errorf("reload the transcript: %w", err)
	}
	if !fresh.ContentHash.Valid || fresh.ContentHash.String != hash {
		return none, checkOutcome{Class: outcomeNotCandidate}, nil
	}
	inSet, err := h.queries.IsInIntegrityCheckedSet(ctx, row.ID)
	if err != nil {
		return none, checkOutcome{Class: outcomeReadFailed}, fmt.Errorf("test the checked set: %w", err)
	}
	if !inSet {
		return none, checkOutcome{Class: outcomeNotCandidate}, nil
	}
	legacy, err := h.queries.IsLegacyLineageContent(ctx, sqlc.IsLegacyLineageContentParams{TranscriptID: row.ID, ContentHash: hash})
	if err != nil {
		return none, checkOutcome{Class: outcomeReadFailed}, fmt.Errorf("read the content's rewrite lineage: %w", err)
	}
	if legacy {
		return none, checkOutcome{Class: outcomeLegacy}, nil
	}

	read, err := h.readEncryptedTranscript(ctx, fresh, "", func(reloaded sqlc.Transcript) bool {
		in, err := h.queries.IsInIntegrityCheckedSet(ctx, reloaded.ID)
		return err == nil && in
	})
	if err != nil {
		return none, checkOutcome{Class: outcomeReadFailed}, nil
	}
	if read.Row.ContentHash.String != hash {
		return none, checkOutcome{Class: outcomeNotCandidate}, nil
	}
	if sniffShape(read.Plaintext) == ShapeRawJSONL {
		return none, checkOutcome{Class: outcomeLegacy}, nil
	}
	payload, _, err := defaultContentMigrator.Migrate(ctx, read.Plaintext)
	if err != nil || validateObservedModelValues(payload) != nil || validateThinkingLevelValues(payload) != nil {
		return none, checkOutcome{Class: outcomeReadFailed}, nil
	}
	metadata, err := h.readPublishMetadata(ctx, read.Row)
	if err != nil {
		return none, checkOutcome{Class: outcomeReadFailed}, nil
	}
	req, err := buildScryRequest(read.Row, payload, metadata)
	if err != nil {
		return none, checkOutcome{Class: outcomeReadFailed}, nil
	}

	checkCtx, cancel := context.WithTimeout(ctx, backgroundCheckTimeout)
	resp, err := h.integrity.checker.Check(checkCtx, req)
	cancel()
	if err != nil {
		status := scryStatus(err)
		if h.integrity.noteFailure(err) {
			return none, checkOutcome{Class: outcomeSecretRefused, Status: status}, nil
		}
		if status != 0 {
			// The service answered past the secret check.
			h.integrity.notePassedSecret()
		}
		return none, checkOutcome{Class: string(scry.Classify(err)), Status: status}, nil
	}
	h.integrity.notePassedSecret()
	h.integrity.observeVersion(resp.ScryVersion)
	if err := recordIntegrityResult(ctx, h.queries, row.ID, hash, resp); err != nil {
		return resp, checkOutcome{Class: outcomeChecked}, err
	}
	return resp, checkOutcome{Class: outcomeChecked}, nil
}

// readPublishMetadata reads and decodes the generation's publish metadata, or
// returns nil when the row holds none.
func (h *Handler) readPublishMetadata(ctx context.Context, row sqlc.Transcript) (*schema.AuthoritativePublishRequest, error) {
	object, err := metadataObjectFromTranscript(row)
	if err != nil || object == nil {
		return nil, err
	}
	descriptor, err := descriptorFromTranscript(row)
	if err != nil {
		return nil, err
	}
	raw, err := h.blobs.ReadMetadata(ctx, uuidFromPg(row.ID), descriptor, *object)
	if err != nil {
		return nil, err
	}
	decoded, err := schema.DecodeAuthoritativePublishMetadataRaw(raw)
	if err != nil {
		return nil, err
	}
	return &decoded, nil
}

// scryStatus is the HTTP status of a failed call, 0 when there was none.
func scryStatus(err error) int {
	var e *scry.Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

// noteFailure reports whether a failed check was the service refusing the
// shared secret, and warns once about it, so an operator sees it without a
// line per pass. Health needs no secret, so only a check that got past the
// secret re-arms the warning (notePassedSecret).
func (s *integrityService) noteFailure(err error) bool {
	status := scryStatus(err)
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		return false
	}
	if s.secretRejected.CompareAndSwap(false, true) {
		slog.Warn("integrity check service rejected the shared secret", "status", status, "consequence", "no transcript is checked until SCRY_SHARED_SECRET matches the service")
	}
	return true
}

// notePassedSecret records that a check got past the service's secret check.
func (s *integrityService) notePassedSecret() {
	s.secretRejected.Store(false)
}
