package handler

// Offering one EXACT selection of sessions to a collective.
//
// The person selects sessions, roots and helpers alike, on the paged
// contribution view and offers exactly that list. The server never expands a
// root into its helpers and never narrows the list: either every listed session
// that is not already live is offered, or the whole request is refused and
// nothing is written.
//
// The write is the whole-project offer's machinery (group_shares.go) driven by
// a list of ids instead of a project: the same publish locks, the same
// collective authority transaction and final authority check, the same row lock
// before visibility is decided, the same ledger statement and conflict
// classifier, and the same trigger-written governance audit for a private
// transcript whose visibility moves with its submission. What differs is the
// answer: every refusal carries a closed machine-readable code and lists every
// offending transcript, and a session that is already live is reported under
// already_shared instead of failing the request.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// RegisterContributionSelectionRoutes mounts the selection write under both
// collective prefixes. The literal selection segment is resolved ahead of any
// {transcriptID} pattern under the same parent.
func (h *Handler) RegisterContributionSelectionRoutes(r chi.Router) {
	for _, prefix := range []string{"/collectives", "/groups"} {
		r.With(h.AuthRequired).Post(prefix+"/{id}/shares/selection", h.BatchShareSelection)
	}
}

// selectionRefusal is a refusal with its closed code and the details that
// name every offender. Returned from inside the write transaction it rolls the
// transaction back, so "nothing was written" stays true.
type selectionRefusal struct {
	status  int
	message string
	code    schema.VillageContributionErrorCode
	details []schema.VillageContributionErrorDetail
}

func (e *selectionRefusal) Error() string { return e.message }

func writeSelectionRefusal(w http.ResponseWriter, e *selectionRefusal) {
	code := e.code
	writeJSON(w, e.status, schema.VillageErrorResponse{Error: e.message, Code: &code, Details: e.details})
}

// requestLevelRefusal is a refusal about the caller and the collective rather
// than any one transcript, so its one detail has no transcript_id.
func requestLevelRefusal(code schema.VillageContributionErrorCode, message string) *selectionRefusal {
	return &selectionRefusal{
		status:  http.StatusForbidden,
		message: message,
		code:    code,
		details: []schema.VillageContributionErrorDetail{{Code: code}},
	}
}

func selectionMembershipRefusal(groupID uuid.UUID) *selectionRefusal {
	return requestLevelRefusal(schema.VillageContributionErrorMembership, fmt.Sprintf(
		"you may not contribute to collective %s: contributing needs an accepted membership as an owner, member or "+
			"contributor, and you have none. Refused before anything was submitted, so nothing was written. Join the "+
			"collective, or wait until your request to join is accepted, then contribute again.", groupID))
}

// selectionPlan is one classification of the selection against the share
// states read for it. Every list is in ascending transcript id order.
type selectionPlan struct {
	write   []sqlc.ListOwnerSelectionShareStatesRow
	already []string
	refused []schema.VillageContributionErrorDetail
}

// selectionRequest is the decoded request: the selected ids folded and sorted,
// and the private approvals as a set.
type selectionRequest struct {
	projectHash string
	ids         []string
	pgIDs       []pgtype.UUID
	approved    map[string]struct{}
}

// classifySelection decides each selected transcript from its share state.
// wasLive, when not nil, is the set the preflight found live; a transcript in
// it that is no longer live has been withdrawn or decided while this request
// waited, and is refused as stale rather than resubmitted.
//
// For one transcript the first matching reason wins: missing, then moved, then
// live (already_shared, not a refusal), then stale, then private without an
// approval. Only a root is checked against the requested project.
func classifySelection(req selectionRequest, rows []sqlc.ListOwnerSelectionShareStatesRow, wasLive map[string]struct{}) selectionPlan {
	byID := make(map[string]sqlc.ListOwnerSelectionShareStatesRow, len(rows))
	for _, row := range rows {
		byID[uuid.UUID(row.ID.Bytes).String()] = row
	}
	plan := selectionPlan{write: []sqlc.ListOwnerSelectionShareStatesRow{}, already: []string{}}
	refuse := func(id string, code schema.VillageContributionErrorCode, current *schema.ProjectHash) {
		tid := schema.TranscriptID(id)
		plan.refused = append(plan.refused, schema.VillageContributionErrorDetail{TranscriptID: &tid, Code: code, CurrentProjectHash: current})
	}
	for _, id := range req.ids {
		row, found := byID[id]
		switch {
		case !found:
			refuse(id, schema.VillageContributionErrorMissing, nil)
		case row.IsRoot && row.ProjectHash != req.projectHash:
			current := schema.ProjectHash(row.ProjectHash)
			refuse(id, schema.VillageContributionErrorMoved, &current)
		case row.Live:
			plan.already = append(plan.already, id)
		default:
			if _, was := wasLive[id]; was {
				refuse(id, schema.VillageContributionErrorStaleSelection, nil)
				continue
			}
			if _, ok := req.approved[id]; row.Visibility == dbVisibilityPrivate && !ok {
				refuse(id, schema.VillageContributionErrorPrivateUnapproved, nil)
				continue
			}
			plan.write = append(plan.write, row)
		}
	}
	return plan
}

// transcriptRefusal builds the 409 for per-transcript refusals. The primary
// code is the first of the details' codes in the contract's own order of
// VillageContributionErrorCode; the details carry every offender.
func transcriptRefusal(details []schema.VillageContributionErrorDetail, selected int) *selectionRefusal {
	counts := map[schema.VillageContributionErrorCode]int{}
	for _, detail := range details {
		counts[detail.Code]++
	}
	var primary schema.VillageContributionErrorCode
	var parts []string
	for _, code := range schema.AllVillageContributionErrorCodes {
		n := counts[code]
		if n == 0 {
			continue
		}
		if primary == "" {
			primary = code
		}
		parts = append(parts, fmt.Sprintf("%d %s", n, selectionRefusalPhrase(code)))
	}
	return &selectionRefusal{
		status: http.StatusConflict,
		message: fmt.Sprintf(
			"%d of the %d sessions in this selection cannot be contributed (%s), so the whole selection was refused and "+
				"nothing was written. details names each session and its reason. Read the selection again, resolve or "+
				"deselect those sessions, then contribute again.",
			len(details), selected, strings.Join(parts, ", ")),
		code:    primary,
		details: details,
	}
}

func selectionRefusalPhrase(code schema.VillageContributionErrorCode) string {
	switch code {
	case schema.VillageContributionErrorMoved:
		return "now in another project"
	case schema.VillageContributionErrorMissing:
		return "not among your published sessions"
	case schema.VillageContributionErrorPrivateUnapproved:
		return "private without your approval"
	case schema.VillageContributionErrorConflict:
		return "submitted by another request at the same moment"
	case schema.VillageContributionErrorStaleSelection:
		return "withdrawn or decided since the selection was read"
	default:
		return string(code)
	}
}

// decodeSelectionRequest decodes the contract-checked body. Fields the contract
// does not declare are ignored, as the contract's open object allows. A
// repeated id is the same session named twice and is folded.
func decodeSelectionRequest(body contractBody) (selectionRequest, error) {
	var raw schema.VillageBatchShareSelectionRequest
	if err := json.Unmarshal(body.Declared, &raw); err != nil {
		return selectionRequest{}, err
	}
	req := selectionRequest{projectHash: string(raw.ProjectHash), approved: map[string]struct{}{}}
	seen := map[string]struct{}{}
	for _, value := range raw.TranscriptIDs {
		id, err := uuid.Parse(string(value))
		if err != nil {
			return selectionRequest{}, fmt.Errorf("transcript_ids: %q is not a transcript id", value)
		}
		canonical := id.String()
		if _, repeated := seen[canonical]; repeated {
			continue
		}
		seen[canonical] = struct{}{}
		req.ids = append(req.ids, canonical)
	}
	for _, value := range raw.PrivateApprovals {
		id, err := uuid.Parse(string(value))
		if err != nil {
			return selectionRequest{}, fmt.Errorf("private_approvals: %q is not a transcript id", value)
		}
		req.approved[id.String()] = struct{}{}
	}
	slices.Sort(req.ids)
	req.pgIDs = make([]pgtype.UUID, 0, len(req.ids))
	for _, id := range req.ids {
		req.pgIDs = append(req.pgIDs, toPgUUID(uuid.MustParse(id)))
	}
	return req, nil
}

// BatchShareSelection offers exactly the listed sessions to one collective in
// one transaction.
// POST /api/v1/groups/{id}/shares/selection (AuthRequired), and the same under
// /api/v1/collectives.
//
// The preflight reads only the listed ids and refuses before any lock is taken.
// The locked write then re-checks authority and every listed session under the
// same publish locks, authority locks and row locks the whole-project offer
// takes, so a change made while the request waited is seen.
func (h *Handler) BatchShareSelection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := GetUser(ctx)

	groupID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"the collective id %q in this address is not a valid identifier, so there is no collective to contribute to. "+
				"Refused before anything was read, so nothing was submitted. Open the collective from its own page and "+
				"retry from there.", chi.URLParam(r, "id")))
		return
	}
	pgGroupID := toPgUUID(groupID)

	body, ok := h.readContractBody(w, r, opBatchShareSelection)
	if !ok {
		return
	}
	req, err := decodeSelectionRequest(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "request body failed contract validation: "+err.Error())
		return
	}

	// Membership first, and the collective's own record second, so an unknown
	// collective answers exactly as one the caller does not belong to.
	member, err := h.queries.GetGroupMember(ctx, sqlc.GetGroupMemberParams{GroupID: pgGroupID, UserID: user.PgID()})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError,
			"your membership of this collective could not be read, so this contribution was not attempted and nothing "+
				"was written. Retry in a moment.")
		return
	}
	if err != nil || !contributingRole(member.Role) {
		writeSelectionRefusal(w, selectionMembershipRefusal(groupID))
		return
	}
	group, err := h.queries.GetGroupByID(ctx, pgGroupID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, fmt.Sprintf(
			"collective %s no longer exists. It may have been deleted while this page was open. Nothing was submitted. "+
				"Reload your collectives and contribute to one that is still there.", groupID))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			"this collective could not be read, so this contribution was not attempted and nothing was written. Retry "+
				"in a moment.")
		return
	}
	status, policyRefusal := shareStatusForGroup(ctx, h.queries, user, group)
	if policyRefusal != nil {
		writeSelectionRefusal(w, requestLevelRefusal(schema.VillageContributionErrorPolicy, policyRefusal.Message))
		return
	}

	// The preflight: bounded by the listed ids, never by the owner's library.
	rows, err := h.queries.ListOwnerSelectionShareStates(ctx, sqlc.ListOwnerSelectionShareStatesParams{
		GroupID:       pgGroupID,
		OwnerID:       user.PgID(),
		TranscriptIds: req.pgIDs,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			"the selected sessions could not be read, so this contribution was not attempted and nothing was written. "+
				"Retry in a moment.")
		return
	}
	preflight := classifySelection(req, rows, nil)
	if len(preflight.refused) > 0 {
		writeSelectionRefusal(w, transcriptRefusal(preflight.refused, len(req.ids)))
		return
	}
	wasLive := make(map[string]struct{}, len(preflight.already))
	for _, id := range preflight.already {
		wasLive[id] = struct{}{}
	}
	localIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		localIDs = append(localIDs, row.LocalID)
	}

	result := schema.VillageBatchShareResponse{
		ProjectHash:   schema.ProjectHash(req.projectHash),
		Shared:        []schema.VillageBatchShareEntry{},
		AlreadyShared: []schema.TranscriptID{},
	}
	var writeErr error
	if err := h.withPublishLocksMany(ctx, user.PgID(), localIDs, func(conn *pgxpool.Conn) error {
		writeErr = h.writeSelectionShare(ctx, conn, user, pgGroupID, status, req, preflight.write, wasLive, &result)
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError,
			"this contribution could not be serialized against other work on the same sessions, so nothing was "+
				"submitted. Retry the contribution.")
		return
	}
	if writeErr != nil {
		var refusal *selectionRefusal
		if errors.As(writeErr, &refusal) {
			writeSelectionRefusal(w, refusal)
			return
		}
		writeError(w, http.StatusInternalServerError,
			"the database could not confirm this contribution, so it was rolled back. Read the selection and its "+
				"current submissions again before explicitly retrying.")
		return
	}

	if len(result.Shared) > 0 {
		// The contribution committed, so its transcripts may have entered the
		// checked set.
		h.nudgeIntegrity()
	}
	writeJSON(w, http.StatusOK, result)
}

// writeSelectionShare performs the locked write of one selection inside ONE
// transaction. Any refusal or statement error rolls the whole selection back.
//
// Order: the final authority check; the row lock of every session the preflight
// would write, in ascending id order; a fresh read of every selected session's
// share state; then, only if nothing is refused, one ledger attempt per session
// and the visibility of each approved private session.
func (h *Handler) writeSelectionShare(ctx context.Context, conn *pgxpool.Conn, user *AuthUser, groupID pgtype.UUID, status ShareStatus, req selectionRequest, toWrite []sqlc.ListOwnerSelectionShareStatesRow, wasLive map[string]struct{}, result *schema.VillageBatchShareResponse) error {
	shared := dbVisibilityShared
	return h.inCollectiveAuthorityTx(ctx, conn, user.PgID(), []pgtype.UUID{groupID}, func(ctx context.Context, q Querier) error {
		currentStatus, authority, err := readShareAuthority(ctx, q, user, groupID)
		if err != nil {
			return err
		}
		if authority != nil {
			if authority.code == schema.VillageContributionErrorMembership {
				return selectionMembershipRefusal(uuid.UUID(groupID.Bytes))
			}
			return requestLevelRefusal(authority.code, authority.message)
		}
		if currentStatus != status {
			return requestLevelRefusal(schema.VillageContributionErrorPolicy,
				"the collective's acceptance policy changed while this contribution waited, so the state your "+
					"contribution would open in is no longer the one it was checked against. Nothing was submitted. "+
					"Reload the collective's policy, then contribute again.")
		}

		// The visibility and project of a session about to be written are read
		// after its row lock is held, so an owner's edit made while this request
		// waited is seen, and none can land between the check and the write.
		for _, row := range toWrite {
			if _, err := q.GetTranscriptGovernanceForUpdate(ctx, row.ID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("lock a selected transcript before deciding its visibility: %w", err)
			}
		}
		rows, err := q.ListOwnerSelectionShareStates(ctx, sqlc.ListOwnerSelectionShareStatesParams{
			GroupID:       groupID,
			OwnerID:       user.PgID(),
			TranscriptIds: req.pgIDs,
		})
		if err != nil {
			return fmt.Errorf("read the selection again under its locks: %w", err)
		}
		plan := classifySelection(req, rows, wasLive)
		if len(plan.refused) > 0 {
			return transcriptRefusal(plan.refused, len(req.ids))
		}

		for _, row := range plan.write {
			id := uuid.UUID(row.ID.Bytes).String()
			if err := q.ShareTranscriptWithStatus(ctx, sqlc.ShareTranscriptWithStatusParams{
				TranscriptID: row.ID,
				GroupID:      groupID,
				Status:       string(currentStatus),
			}); err != nil {
				if kind := classifyShareAttemptConflict(err); kind != shareAttemptConflictNone {
					tid := schema.TranscriptID(id)
					return transcriptRefusal([]schema.VillageContributionErrorDetail{{TranscriptID: &tid, Code: schema.VillageContributionErrorConflict}}, len(req.ids))
				}
				return fmt.Errorf("open a share attempt for a selected transcript: %w", err)
			}
			result.Shared = append(result.Shared, schema.VillageBatchShareEntry{
				TranscriptID: schema.TranscriptID(id),
				Status:       schema.VillageContributionStatus(currentStatus),
			})
			// A private session's visibility moves with its submission in the
			// same transaction, and the governance trigger records the change
			// against this actor. The approval was checked under the row lock.
			if row.Visibility != dbVisibilityPrivate {
				continue
			}
			if _, err := applyMetadataPatch(ctx, q, row.ID, metadataPatch{Visibility: &shared}); err != nil {
				return fmt.Errorf("record the new visibility of a selected transcript: %w", err)
			}
		}
		for _, id := range plan.already {
			result.AlreadyShared = append(result.AlreadyShared, schema.TranscriptID(id))
		}
		return nil
	})
}
