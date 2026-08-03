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
// from the LOCKED pre-image: visibility is NEVER changed by a re-publish
// (governance edits go through the PATCH path), and an absent CLI license
// (Valid=false) preserves the existing one. Runs inside inTxAs; if the pinned
// params still move the license, the migration-026 trigger records
// license_changed with the transaction's actor.
//
// Pinning here under the same FOR UPDATE lock is observably equivalent to
// resolving with COALESCE in SQL: no concurrent writer
// can intervene, so a pinned value is WHEN-false at the trigger) and avoids
// renumbering the 60-positional-param update query.
func pinRepublishGovernance(ctx context.Context, q Querier, id pgtype.UUID, params *sqlc.UpdateTranscriptByOwnerAndLocalIDParams) error {
	pre, err := q.GetTranscriptGovernanceForUpdate(ctx, id)
	if err != nil {
		return err
	}
	params.Visibility = pre.Visibility
	if !params.LicenseID.Valid {
		params.LicenseID = pre.LicenseID
	}
	return nil
}
