package handler

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// errUnlicenseBlocked: a GRANTED license can never be cleared back to NULL —
// CC licenses are irrevocable for anyone who already received the work, so an
// un-license would misrepresent the legal state. Changing to a DIFFERENT menu
// license stays allowed (audited as license_changed); license:"" on a
// never-licensed row is an idempotent no-op. The PATCH handler maps this to a
// 400 with an actionable body.
var errUnlicenseBlocked = errors.New("cannot remove the license: a granted license is irrevocable; set a different license instead")
var errSharedVisibilityRequiresNarrowing = errors.New("cannot update a shared transcript without explicitly narrowing visibility to private or public; choose private or public and retry")

// metadataPatch is a PATCH's partial-update intent: a nil Title/Description/
// Visibility pointer means "leave unchanged". License is tri-state via
// LicenseProvided — false preserves the current license, true applies License
// (which may be NULL to clear). The final row is resolved against the LOCKED
// pre-image inside the txn (applyMetadataPatch), so an omitted field is never
// reverted to a value read before the lock.
type metadataPatch struct {
	Title           *string
	Description     *string
	Visibility      *string
	License         pgtype.Text
	LicenseProvided bool
	Tags            *[]string
}

// applyMetadataPatch resolves the patch against the LOCKED narrow pre-image and
// applies the update. Callers run it inside inTxAs: the migration-026 triggers
// write the governance audit (license_changed / visibility_changed /
// governance_changed, or nothing when no axis moved — the suppression is the
// trigger's WHEN clause, not code here), attributed to the transaction's actor.
func applyMetadataPatch(ctx context.Context, q Querier, id pgtype.UUID, patch metadataPatch) (sqlc.Transcript, error) {
	pre, err := q.GetTranscriptGovernanceForUpdate(ctx, id)
	if err != nil {
		return sqlc.Transcript{}, err
	}
	if pre.Visibility == dbVisibilityShared && patch.Visibility == nil {
		return sqlc.Transcript{}, errSharedVisibilityRequiresNarrowing
	}
	params := sqlc.UpdateTranscriptMetadataParams{
		ID:          id,
		Title:       pre.Title,
		Description: pre.Description,
		Visibility:  pre.Visibility,
		LicenseID:   pre.LicenseID,
	}
	if patch.Title != nil {
		params.Title = toPgText(*patch.Title)
	}
	if patch.Description != nil {
		params.Description = toPgText(*patch.Description)
	}
	if patch.Visibility != nil {
		params.Visibility = *patch.Visibility
	}
	if patch.LicenseProvided {
		if !patch.License.Valid && pre.LicenseID.Valid {
			return sqlc.Transcript{}, errUnlicenseBlocked
		}
		params.LicenseID = patch.License
	}
	return q.UpdateTranscriptMetadata(ctx, params)
}

// pinRepublishGovernance pins the governance axes of a re-publish onto params
// from the LOCKED pre-image: a re-publish never chooses a visibility of its own
// (governance edits go through the PATCH path), and an absent CLI license
// (Valid=false) preserves the existing one. Runs inside inTxAs; if the pinned
// params still move the license, the migration-026 trigger records
// license_changed with the transaction's actor.
//
// narrowedFrom is the visibility the same re-publish narrowed away before
// staging its content (narrowForRepublish), or "" when it narrowed nothing. It
// is put back in this transaction (republishRestoreTarget), so the receipt and
// the audience commit together.
//
// Pinning here under the same FOR UPDATE lock is observably equivalent to
// resolving with COALESCE in SQL (no concurrent writer can intervene, so a
// value pinned unchanged is WHEN-false at the trigger) and avoids renumbering
// the 60-positional-param update query.
func pinRepublishGovernance(ctx context.Context, q Querier, id pgtype.UUID, params *sqlc.UpdateTranscriptByOwnerAndLocalIDParams, narrowedFrom string) error {
	pre, err := q.GetTranscriptGovernanceForUpdate(ctx, id)
	if err != nil {
		return err
	}
	params.Visibility = republishRestoreTarget(pre.Visibility, narrowedFrom)
	if !params.LicenseID.Valid {
		params.LicenseID = pre.LicenseID
	}
	return nil
}

// republishRestoreTarget is the visibility a re-publish writes back, given the
// value its narrowing removed and the value the row lock returns now. The
// restore applies only while the row is still the private value the narrowing
// left. Every writer of visibility holds the same publish lock as the
// re-publish, so under the lock nothing else can have moved it; the condition
// is defensive, and if a later decision were ever found there it would stand.
func republishRestoreTarget(locked, narrowedFrom string) string {
	if narrowedFrom != "" && locked == dbVisibilityPrivate {
		return narrowedFrom
	}
	return locked
}

// narrowForRepublish moves a public or shared transcript to private before a
// re-publish stages replacement content, and returns the visibility it held
// under the row lock so the re-publish can put it back. It returns "" when the
// row was already private and nothing was written. Runs inside inTxAs, so the
// migration-026 trigger records the narrowing as the publisher.
func narrowForRepublish(ctx context.Context, q Querier, id pgtype.UUID) (string, error) {
	pre, err := q.GetTranscriptGovernanceForUpdate(ctx, id)
	if err != nil {
		return "", err
	}
	if pre.Visibility == dbVisibilityPrivate {
		return "", nil
	}
	private := dbVisibilityPrivate
	if _, err := applyMetadataPatch(ctx, q, id, metadataPatch{Visibility: &private}); err != nil {
		return "", err
	}
	return pre.Visibility, nil
}

// restoreNarrowedVisibility is the compensating half of narrowForRepublish for
// a re-publish that failed definitely: it writes back what
// republishRestoreTarget says, and nothing when that is what the row already
// holds. Runs inside inTxAs.
func restoreNarrowedVisibility(ctx context.Context, q Querier, id pgtype.UUID, narrowedFrom string) error {
	pre, err := q.GetTranscriptGovernanceForUpdate(ctx, id)
	if err != nil {
		return err
	}
	target := republishRestoreTarget(pre.Visibility, narrowedFrom)
	if target == pre.Visibility {
		return nil
	}
	_, err = applyMetadataPatch(ctx, q, id, metadataPatch{Visibility: &target})
	return err
}
