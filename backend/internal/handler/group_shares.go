package handler

// Offering a WHOLE project to a collective, and the read that tells the person
// what they may offer.
//
// The single-transcript share path (ShareTranscript) runs its inserts as
// autocommit statements and skips whatever it cannot do, which is right when the
// person named one transcript and several collectives: a collective that refuses
// them changes nothing about the others. A project batch is the opposite
// promise. The person selected a set and pressed one button, so a refusal must
// leave NOTHING written and a partial success must never be silently narrowed.
//
// That promise shapes the whole file: every check that can refuse runs BEFORE
// any write, the writes then run in ONE transaction whose failure rolls the
// whole batch back, and the duplicate-conflict pattern the single path uses
// (record it and carry on) is deliberately NOT reproduced inside that
// transaction, where carrying on after an error is not possible at all.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/projectname"
)

// ShareStatus is the state a NEW submission opens in. It is the closed set the
// acceptance modes map onto: an open collective accepts on receipt, a curated
// one queues the submission for review. The other attempt states (rejected,
// retracted, revoked) are decisions about an existing attempt and can never be
// the state a submission starts in, so they are deliberately absent here.
type ShareStatus string

const (
	// ShareStatusApproved is the state an open or verified-only collective
	// accepts a submission in.
	ShareStatusApproved ShareStatus = "approved"
	// ShareStatusPending is the state a curated collective queues a submission
	// in until a moderator decides it.
	ShareStatusPending ShareStatus = "pending"
)

// shareRefusalReason names why a collective will not take a submission from this
// member. It is a closed set so a caller answers a refusal by its reason rather
// than by matching on message text.
type shareRefusalReason string

const (
	// shareRefusalUnverifiedOrg means the collective only accepts members whose
	// GitHub organization membership is visible, and this member's is not.
	shareRefusalUnverifiedOrg shareRefusalReason = "unverified_org"
)

// shareRefusal is a collective-level refusal: it is about the member and the
// collective, not about any one transcript, so it refuses a whole batch.
type shareRefusal struct {
	Reason  shareRefusalReason
	Message string
}

// shareStatusForGroup answers what state a submission from this member to this
// collective opens in, or refuses the submission outright.
//
// It is the ONE place the acceptance-mode rule lives. Both the single share and
// the project batch call it, so the two paths cannot drift about which
// collectives accept a contribution or about who may offer one. An unrecognised
// acceptance mode falls through to the open-collective answer, which is the
// shipped behaviour of the single share path and is preserved deliberately: the
// database CHECK on the column is what keeps the set closed.
func shareStatusForGroup(ctx context.Context, q Querier, user *AuthUser, group sqlc.Group) (ShareStatus, *shareRefusal) {
	switch group.AcceptanceMode {
	case "verified_only":
		// A collective linked to a specific GitHub organization requires THAT
		// organization to be visible on the member's profile. One that names no
		// organization accepts any visible organization at all.
		if group.LinkedGithubOrg.Valid && group.LinkedGithubOrg.String != "" {
			ok, err := q.HasUserVisibleOrg(ctx, sqlc.HasUserVisibleOrgParams{
				UserID: user.PgID(),
				Lower:  group.LinkedGithubOrg.String,
			})
			// A failed read is a refusal, not an acceptance: this collective
			// admits verified members only, and an unanswered question about
			// verification must not be resolved in the submitter's favour.
			if err != nil || !ok {
				return "", &shareRefusal{
					Reason: shareRefusalUnverifiedOrg,
					Message: fmt.Sprintf("this collective accepts contributions only from members of the %s organization, "+
						"and that organization is not visible on your profile. Determined before anything was submitted, "+
						"so nothing was written. Make your %s membership public on GitHub, refresh your organizations on "+
						"your Village profile, then contribute again.",
						group.LinkedGithubOrg.String, group.LinkedGithubOrg.String),
				}
			}
			return ShareStatusApproved, nil
		}
		visibleOrgs, _ := q.ListUserVisibleOrgs(ctx, user.PgID())
		if len(visibleOrgs) == 0 {
			return "", &shareRefusal{
				Reason: shareRefusalUnverifiedOrg,
				Message: "this collective accepts contributions only from members whose GitHub organizations are visible, " +
					"and none of yours are. Determined before anything was submitted, so nothing was written. Make at " +
					"least one organization membership public on GitHub, refresh your organizations on your Village " +
					"profile, then contribute again.",
			}
		}
		return ShareStatusApproved, nil
	case "curated":
		return ShareStatusPending, nil
	default:
		return ShareStatusApproved, nil
	}
}

// batchShareRequest is one project offered to one collective.
//
// An omitted or empty transcript_ids means EVERY transcript the caller owns in
// the project: the surface's "contribute the whole project" action does not have
// to enumerate a set the server can resolve itself, and cannot enumerate it
// stalely. visibility_confirmed is the person's answer to the one consequence
// they cannot undo - a private transcript becomes visible to the collective - so
// the server refuses rather than assumes when a private transcript is in the set
// and the answer is absent.
type batchShareRequest struct {
	ProjectHash         string   `json:"project_hash"`
	TranscriptIDs       []string `json:"transcript_ids"`
	VisibilityConfirmed bool     `json:"visibility_confirmed"`
}

// batchShareEntry is one transcript this request actually submitted, with the
// state its attempt opened in.
type batchShareEntry struct {
	TranscriptID string      `json:"transcript_id"`
	Status       ShareStatus `json:"status"`
}

// batchShareResponse is the receipt. already_shared is not an error: those
// transcripts were already live in the collective before this request, and
// naming them is what lets the surface show a complete answer for the project
// instead of a partial one the person has to reconcile.
type batchShareResponse struct {
	ProjectHash   string            `json:"project_hash"`
	Shared        []batchShareEntry `json:"shared"`
	AlreadyShared []string          `json:"already_shared"`
}

// maxListedPrivateTranscripts bounds how many ids the consent refusal names. The
// person needs enough to recognise what they selected, not the whole set; the
// count that follows carries the size.
const maxListedPrivateTranscripts = 5

// batchShareConflict marks a database refusal of a new share attempt while the
// batch transaction was running - another writer got to the same pair first. It
// carries the KIND as well as the transcript, because the two kinds need
// opposite advice: a duplicate submission is cleared by withdrawing or deciding
// the live one, while an ordering conflict is cleared by simply asking again.
// It travels out of the transaction so the handler can name the transcript
// instead of reporting an anonymous internal failure.
type batchShareConflict struct {
	TranscriptID string
	Kind         shareAttemptConflict
}

func (e *batchShareConflict) Error() string {
	return "a competing submission of transcript " + e.TranscriptID + " (" + string(e.Kind) +
		") was recorded while this batch was being written"
}

// message is what the person is told. Both kinds roll the WHOLE batch back, so
// both say so; they differ in what clears the conflict.
func (e *batchShareConflict) message() string {
	switch e.Kind {
	case shareAttemptConflictEventNum:
		return fmt.Sprintf(
			"transcript %s was submitted to this collective by another request at the same moment as this one, so this "+
				"contribution lost the race for that transcript's place in the collective's history. The WHOLE "+
				"contribution was rolled back and nothing was written. Contribute the project again: the next attempt "+
				"either submits it or reports it as already contributed.", e.TranscriptID)
	default:
		return fmt.Sprintf(
			"transcript %s was submitted to this collective by another request while this contribution was being "+
				"written, so a second submission would be a duplicate rather than a new attempt. The WHOLE "+
				"contribution was rolled back and nothing was written. Contribute the project again: the transcript "+
				"that conflicted will be reported as already contributed and the rest will be submitted.", e.TranscriptID)
	}
}

// BatchShareProject offers every selected transcript of ONE project to ONE
// collective in a single request.
// POST /api/v1/groups/{id}/shares (AuthRequired)
func (h *Handler) BatchShareProject(w http.ResponseWriter, r *http.Request) {
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

	req, refusal := decodeBatchShareRequest(r)
	if refusal != "" {
		writeError(w, http.StatusBadRequest, refusal)
		return
	}

	// Membership first, and the collective's own record second: a caller who is
	// not a member gets the same answer whether or not the collective exists, so
	// this route cannot be used to find out which collectives are there.
	if _, err := h.queries.GetGroupMember(ctx, sqlc.GetGroupMemberParams{
		GroupID: pgGroupID,
		UserID:  user.PgID(),
	}); err != nil {
		writeError(w, http.StatusForbidden, fmt.Sprintf(
			"you are not a member of collective %s, so you cannot contribute to it. Refused before anything was "+
				"submitted, so nothing was written. Join the collective first, then contribute the project again.",
			groupID))
		return
	}
	group, err := h.queries.GetGroupByID(ctx, pgGroupID)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf(
			"collective %s no longer exists. It may have been deleted while this page was open. Nothing was submitted. "+
				"Reload your collectives and contribute the project to one that is still there.", groupID))
		return
	}

	status, groupRefusal := shareStatusForGroup(ctx, h.queries, user, group)
	if groupRefusal != nil {
		writeError(w, http.StatusForbidden, groupRefusal.Message)
		return
	}

	candidates, err := h.queries.ListOwnerProjectShareCandidates(ctx, sqlc.ListOwnerProjectShareCandidatesParams{
		OwnerID:     user.PgID(),
		ProjectHash: req.ProjectHash,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf(
			"the transcripts of project %s could not be read, so this contribution was not attempted and nothing was "+
				"written. Retry in a moment; if it keeps failing, report the project hash with this message.",
			req.ProjectHash))
		return
	}
	if len(candidates) == 0 {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf(
			"you own no transcripts in project %s, so there is nothing of yours to contribute. Nothing was written. "+
				"Check that you copied the project from your own project list; a project you can see but do not own "+
				"cannot be contributed by you.", req.ProjectHash))
		return
	}

	selected, selectionRefusal := selectBatchCandidates(candidates, req.TranscriptIDs, req.ProjectHash)
	if selectionRefusal != "" {
		writeError(w, http.StatusUnprocessableEntity, selectionRefusal)
		return
	}

	liveIDs := make([]pgtype.UUID, 0, len(selected))
	for _, candidate := range selected {
		liveIDs = append(liveIDs, candidate.ID)
	}
	live, err := h.queries.ListLiveShareAttemptsForGroup(ctx, sqlc.ListLiveShareAttemptsForGroupParams{
		GroupID:       pgGroupID,
		TranscriptIds: liveIDs,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf(
			"the existing submissions of project %s to this collective could not be read, so this contribution was not "+
				"attempted and nothing was written. Retry in a moment.", req.ProjectHash))
		return
	}
	alreadyLive := map[pgtype.UUID]struct{}{}
	for _, row := range live {
		alreadyLive[row.TranscriptID] = struct{}{}
	}

	alreadyShared := make([]string, 0, len(alreadyLive))
	remaining := make([]sqlc.ListOwnerProjectShareCandidatesRow, 0, len(selected))
	for _, candidate := range selected {
		if _, isLive := alreadyLive[candidate.ID]; isLive {
			alreadyShared = append(alreadyShared, uuid.UUID(candidate.ID.Bytes).String())
			continue
		}
		remaining = append(remaining, candidate)
	}

	if len(remaining) == 0 {
		writeError(w, http.StatusConflict, "Every transcript in this selection is already submitted to "+
			pluralCollectives(len(alreadyShared))+": a submission awaiting review or already accepted is still live, so a second submission would be a duplicate rather than a new attempt. "+
			"Nothing was changed. Withdraw the existing submissions first if you want to submit them again, or wait for the collective to decide them; once they are rejected or withdrawn, sharing again opens a new attempt.")
		return
	}

	if consentRefusal := consentRefusalForPrivate(remaining, req.VisibilityConfirmed); consentRefusal != "" {
		writeError(w, http.StatusUnprocessableEntity, consentRefusal)
		return
	}

	localIDs := make([]string, 0, len(remaining))
	for _, candidate := range remaining {
		localIDs = append(localIDs, candidate.LocalID)
	}

	var writeErr error
	if err := h.withPublishLocksMany(ctx, user.PgID(), localIDs, func(conn *pgxpool.Conn) error {
		writeErr = h.writeBatchShare(ctx, conn, user, pgGroupID, status, remaining)
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf(
			"contributing project %s could not be serialized against other work on the same sessions, so nothing was "+
				"submitted. Retry the contribution.", req.ProjectHash))
		return
	}
	if writeErr != nil {
		var conflict *batchShareConflict
		if errors.As(writeErr, &conflict) {
			writeError(w, http.StatusConflict, conflict.message())
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf(
			"contributing project %s to this collective failed while the submissions were being recorded, so the WHOLE "+
				"contribution was rolled back and nothing was written. Retry the contribution; if it keeps failing, the "+
				"collective may have been deleted while the request was in flight.", req.ProjectHash))
		return
	}

	shared := make([]batchShareEntry, 0, len(remaining))
	for _, candidate := range remaining {
		shared = append(shared, batchShareEntry{
			TranscriptID: uuid.UUID(candidate.ID.Bytes).String(),
			Status:       status,
		})
	}
	writeJSON(w, http.StatusOK, batchShareResponse{
		ProjectHash:   req.ProjectHash,
		Shared:        shared,
		AlreadyShared: alreadyShared,
	})
}

// writeBatchShare performs every write of one batch inside ONE transaction.
//
// The single share path answers a duplicate conflict by recording the collective
// and continuing to the next one. That is safe there ONLY because each insert is
// its own autocommit statement. Here the statements share a transaction, so an
// error has already aborted it: continuing would run the remaining statements
// against a dead transaction and commit nothing while reporting success. The
// conflict is therefore returned, which rolls the whole batch back, and the
// person is told to contribute again - the next attempt reads the conflicting
// transcript as already contributed and submits the rest.
func (h *Handler) writeBatchShare(ctx context.Context, conn *pgxpool.Conn, user *AuthUser, groupID pgtype.UUID, status ShareStatus, candidates []sqlc.ListOwnerProjectShareCandidatesRow) error {
	shared := dbVisibilityShared
	return h.inTxAsOnConn(ctx, conn, user.PgID(), func(q Querier) error {
		for _, candidate := range candidates {
			if err := q.ShareTranscriptWithStatus(ctx, sqlc.ShareTranscriptWithStatusParams{
				TranscriptID: candidate.ID,
				GroupID:      groupID,
				Status:       string(status),
			}); err != nil {
				if kind := classifyShareAttemptConflict(err); kind != shareAttemptConflictNone {
					// Returning rolls the WHOLE batch back. The single share
					// path records a conflict and carries on, which is safe only
					// because its statements are autocommit; inside this
					// transaction the error has already aborted it, so
					// continuing would commit nothing while reporting success.
					return &batchShareConflict{TranscriptID: uuid.UUID(candidate.ID.Bytes).String(), Kind: kind}
				}
				return fmt.Errorf("open a share attempt for transcript %s: %w", uuid.UUID(candidate.ID.Bytes).String(), err)
			}
			// Contributing a private transcript makes it visible to the
			// collective, so its visibility moves with the submission and in the
			// same transaction: the two can never be observed apart, and the
			// governance trigger records the change against this actor.
			if candidate.Visibility != dbVisibilityPrivate {
				continue
			}
			if _, err := applyMetadataPatch(ctx, q, candidate.ID, metadataPatch{Visibility: &shared}); err != nil {
				return fmt.Errorf("record the new visibility of transcript %s: %w", uuid.UUID(candidate.ID.Bytes).String(), err)
			}
		}
		return nil
	})
}

// decodeBatchShareRequest reads and validates the body. Unknown fields are
// rejected rather than ignored: a client that misspells visibility_confirmed
// would otherwise be told its private transcripts need confirming while it
// believes it sent one.
func decodeBatchShareRequest(r *http.Request) (batchShareRequest, string) {
	var req batchShareRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return req, fmt.Sprintf(
			"this contribution request could not be read: %s. Refused before anything was submitted, so nothing was "+
				"written. Send project_hash, an optional transcript_ids list, and visibility_confirmed, and no other "+
				"fields.", err.Error())
	}
	if !projectHashPattern.MatchString(req.ProjectHash) {
		return req, fmt.Sprintf(
			"the project %q is not a project hash: a project is identified by exactly 64 lowercase hexadecimal "+
				"characters, and this value is not. Refused before anything was submitted, so nothing was written. "+
				"Copy the project_hash exactly as it appears on your project page, in lowercase.", req.ProjectHash)
	}
	seen := map[string]struct{}{}
	deduped := make([]string, 0, len(req.TranscriptIDs))
	for _, raw := range req.TranscriptIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			return req, fmt.Sprintf(
				"the transcript %q in this contribution is not a valid identifier. Refused before anything was "+
					"submitted, so nothing was written. Send the transcript ids exactly as they appear on the "+
					"contribute page, or send no transcript_ids at all to contribute the whole project.", raw)
		}
		// A repeated id is the same transcript named twice, not a request to
		// submit it twice, so it is folded rather than refused: the person's
		// intent is unambiguous and there is nothing for them to fix.
		canonical := id.String()
		if _, repeated := seen[canonical]; repeated {
			continue
		}
		seen[canonical] = struct{}{}
		deduped = append(deduped, canonical)
	}
	req.TranscriptIDs = deduped
	return req, ""
}

// selectBatchCandidates resolves the requested ids against the project's owned
// transcripts. An empty request selects the whole project.
func selectBatchCandidates(candidates []sqlc.ListOwnerProjectShareCandidatesRow, requested []string, projectHash string) ([]sqlc.ListOwnerProjectShareCandidatesRow, string) {
	if len(requested) == 0 {
		return candidates, ""
	}
	byID := make(map[string]sqlc.ListOwnerProjectShareCandidatesRow, len(candidates))
	for _, candidate := range candidates {
		byID[uuid.UUID(candidate.ID.Bytes).String()] = candidate
	}
	var offending []string
	for _, id := range requested {
		if _, ok := byID[id]; !ok {
			offending = append(offending, id)
		}
	}
	if len(offending) > 0 {
		// One transcript that is not yours, or belongs to another project,
		// refuses the WHOLE request: the person selected a project, and
		// contributing some of what they selected while silently dropping the
		// rest would leave them believing they had contributed all of it.
		return nil, fmt.Sprintf(
			"transcript %s is not one of yours in project %s, and %d of the %d transcripts in this contribution are "+
				"not. Refused before anything was submitted, so nothing was written. Contribute only transcripts you "+
				"own in this project, or send no transcript_ids at all to contribute the whole project.",
			offending[0], projectHash, len(offending), len(requested))
	}
	// The response follows the project's own order, not the order the client
	// happened to send, so the receipt matches the list the person is looking at.
	selected := make([]sqlc.ListOwnerProjectShareCandidatesRow, 0, len(requested))
	requestedSet := make(map[string]struct{}, len(requested))
	for _, id := range requested {
		requestedSet[id] = struct{}{}
	}
	for _, candidate := range candidates {
		if _, ok := requestedSet[uuid.UUID(candidate.ID.Bytes).String()]; ok {
			selected = append(selected, candidate)
		}
	}
	return selected, ""
}

// consentRefusalForPrivate refuses a batch that would make private transcripts
// visible without the person having said so.
func consentRefusalForPrivate(candidates []sqlc.ListOwnerProjectShareCandidatesRow, confirmed bool) string {
	if confirmed {
		return ""
	}
	var private []string
	for _, candidate := range candidates {
		if candidate.Visibility == dbVisibilityPrivate {
			private = append(private, uuid.UUID(candidate.ID.Bytes).String())
		}
	}
	if len(private) == 0 {
		return ""
	}
	listed := private
	if len(listed) > maxListedPrivateTranscripts {
		listed = listed[:maxListedPrivateTranscripts]
	}
	return fmt.Sprintf(
		"%d of the transcripts in this contribution are private, and contributing them makes them visible to everyone "+
			"in the collective: %s. Refused before anything was submitted, so nothing was written and they are still "+
			"private. Contribute again with visibility_confirmed set to true if you intend to share them, or deselect "+
			"them first.", len(private), strings.Join(listed, ", "))
}

// contributableTranscript is one row of the contribute surface: the transcript,
// the project identity it groups under, and whether it is already live in this
// collective.
type contributableTranscript struct {
	ID                 string                 `json:"id"`
	LocalID            string                 `json:"local_id"`
	Title              *string                `json:"title"`
	Visibility         string                 `json:"visibility"`
	ProjectHash        string                 `json:"project_hash"`
	ProjectDisplayName string                 `json:"project_display_name"`
	ProjectNameSource  projectname.NameSource `json:"project_name_source"`
	GitBranch          *string                `json:"git_branch"`
	ParentSessionID    *string                `json:"parent_session_id"`
	SessionOrigin      string                 `json:"session_origin"`
	ModelProvider      string                 `json:"model_provider"`
	PublishedAt        pgtype.Timestamptz     `json:"published_at"`
	AlreadyShared      bool                   `json:"already_shared"`
}

// contributableResponse is the whole corpus for one collective. It is
// deliberately not paginated: the surface builds a project tree over the whole
// set, and a page of it would let a person contribute part of a project while
// believing they contributed all of it. The row limit below is the bound.
type contributableResponse struct {
	GroupID     string                    `json:"group_id"`
	Transcripts []contributableTranscript `json:"transcripts"`
}

// ListContributable serves everything the caller may offer to ONE collective.
// GET /api/v1/groups/{id}/contributable (AuthRequired)
func (h *Handler) ListContributable(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := GetUser(ctx)

	groupID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"the collective id %q in this address is not a valid identifier, so there is no collective to answer for. "+
				"Nothing was read. Open the collective from its own page and retry from there.", chi.URLParam(r, "id")))
		return
	}
	pgGroupID := toPgUUID(groupID)

	if _, err := h.queries.GetGroupMember(ctx, sqlc.GetGroupMemberParams{
		GroupID: pgGroupID,
		UserID:  user.PgID(),
	}); err != nil {
		writeError(w, http.StatusForbidden, fmt.Sprintf(
			"you are not a member of collective %s, so Village will not list what you could contribute to it. Nothing "+
				"was read. Join the collective first, then open its contribute page.", groupID))
		return
	}

	if h.contributableRowLimit <= 0 {
		writeError(w, http.StatusInternalServerError,
			"the contribute listing has no row limit configured, so this server will not serve an unbounded response. "+
				"Nothing was read. This is a server wiring fault: construct the handler through its constructor, which "+
				"sets the limit, and restart.")
		return
	}

	rows, err := h.queries.ListOwnerContributableTranscripts(ctx, sqlc.ListOwnerContributableTranscriptsParams{
		OwnerID: user.PgID(),
		GroupID: pgGroupID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			"your transcripts could not be read, so Village cannot say what you could contribute to this collective. "+
				"Nothing was changed. Retry in a moment.")
		return
	}
	if len(rows) > h.contributableRowLimit {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf(
			"you have %d published transcripts and this listing serves at most %d in one answer, so Village will not "+
				"build a contribute page it cannot render. Nothing was changed. Contribute from a project page, which "+
				"is scoped to one project, or ask for the listing to be raised.",
			len(rows), h.contributableRowLimit))
		return
	}

	keys := make([]projectIdentityKey, 0, len(rows))
	seen := map[string]struct{}{}
	for _, row := range rows {
		if _, ok := seen[row.ProjectHash]; ok {
			continue
		}
		seen[row.ProjectHash] = struct{}{}
		keys = append(keys, projectIdentityKey{OwnerID: user.PgID(), ProjectHash: row.ProjectHash})
	}
	identities := h.resolveProjectIdentities(ctx, keys)

	transcripts := make([]contributableTranscript, 0, len(rows))
	for _, row := range rows {
		identity := identities[projectIdentityKey{OwnerID: user.PgID(), ProjectHash: row.ProjectHash}]
		transcripts = append(transcripts, contributableTranscript{
			ID:                 uuid.UUID(row.ID.Bytes).String(),
			LocalID:            row.LocalID,
			Title:              pgTextPointer(row.Title),
			Visibility:         row.Visibility,
			ProjectHash:        row.ProjectHash,
			ProjectDisplayName: identity.DisplayName,
			ProjectNameSource:  identity.Source,
			GitBranch:          pgTextPointer(row.GitBranch),
			ParentSessionID:    pgTextPointer(row.ParentSessionID),
			SessionOrigin:      row.SessionOrigin,
			ModelProvider:      row.ModelProvider,
			PublishedAt:        row.PublishedAt,
			AlreadyShared:      row.AlreadyShared,
		})
	}

	writeJSON(w, http.StatusOK, contributableResponse{
		GroupID:     groupID.String(),
		Transcripts: transcripts,
	})
}
