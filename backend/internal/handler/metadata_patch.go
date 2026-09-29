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
// restore is the visibility the same re-publish narrowed away before staging
// its content (narrowForRepublish), or "" when it narrowed nothing. It is put
// back in this transaction, so the receipt and the audience commit together.
// It applies only while the row is still the private value the narrowing left:
// a different value was written by someone else after the narrowing, and that
// decision stands.
//
// Pinning here under the same FOR UPDATE lock is observably equivalent to
// resolving with COALESCE in SQL (no concurrent writer can intervene, so a
// value pinned unchanged is WHEN-false at the trigger) and avoids renumbering
// the 60-positional-param update query.
func pinRepublishGovernance(ctx context.Context, q Querier, id pgtype.UUID, params *sqlc.UpdateTranscriptByOwnerAndLocalIDParams, restore string) error {
	pre, err := q.GetTranscriptGovernanceForUpdate(ctx, id)
	if err != nil {
		return err
	}
	params.Visibility = pre.Visibility
	if restore != "" && pre.Visibility == dbVisibilityPrivate {
		params.Visibility = restore
	}
	if !params.LicenseID.Valid {
		params.LicenseID = pre.LicenseID
	}
	return nil
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
// a re-publish that failed definitely: it puts back the visibility the
// narrowing removed, under the same condition pinRepublishGovernance applies.
// Runs inside inTxAs.
func restoreNarrowedVisibility(ctx context.Context, q Querier, id pgtype.UUID, restore string) error {
	pre, err := q.GetTranscriptGovernanceForUpdate(ctx, id)
	if err != nil {
		return err
	}
	if restore == "" || pre.Visibility != dbVisibilityPrivate {
		return nil
	}
	_, err = applyMetadataPatch(ctx, q, id, metadataPatch{Visibility: &restore})
	return err
}
