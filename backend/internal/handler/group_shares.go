package handler

// Offering a WHOLE project to a collective, and the read that tells the person
// what they may offer.
//
// Single-transcript and whole-project sharing both validate every requested
// target under final authority locks and write their selected operation in one
// transaction. A refusal never silently narrows a person's selection.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
		// A collective linked to a specific GitHub organization requires
		// MEMBERSHIP of that organization. One that names no organization accepts
		// any organization the member has on record. Neither requires the
		// affiliation to be published: publishing is a separate, opt-in choice
		// about the person's profile (see org_membership.go).
		if group.LinkedGithubOrg.Valid && group.LinkedGithubOrg.String != "" {
			ok, err := hasOrgMembership(ctx, q, user.PgID(), group.LinkedGithubOrg.String)
			// A failed read is a refusal, not an acceptance: this collective
			// admits verified members only, and an unanswered question about
			// membership must not be resolved in the submitter's favour.
			if err != nil || !ok {
				return "", &shareRefusal{
					Reason: shareRefusalUnverifiedOrg,
					Message: fmt.Sprintf("this collective accepts contributions only from members of the %s organization, "+
						"and village has no record of your membership of it. Determined before anything was submitted, "+
						"so nothing was written. If you have joined %s recently, sign in again so village re-reads your "+
						"organizations, then contribute again.",
						group.LinkedGithubOrg.String, group.LinkedGithubOrg.String),
				}
			}
			return ShareStatusApproved, nil
		}
		anyOrg, err := hasAnyOrgMembership(ctx, q, user.PgID())
		if err != nil || !anyOrg {
			return "", &shareRefusal{
				Reason: shareRefusalUnverifiedOrg,
				Message: "this collective accepts contributions only from members of a GitHub organization, and village " +
					"has no organization on record for you. Determined before anything was submitted, so nothing was " +
					"written. If you have joined one recently, sign in again so village re-reads your organizations, " +
					"then contribute again.",
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
// batchConsentRefusal is a private transcript found under the row lock that
// the request did not confirm contributing. It rolls the whole batch back and
// answers with the same consent message the check before the lock gives.
type batchConsentRefusal struct {
	message string
}

func (e *batchConsentRefusal) Error() string { return e.message }

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

	body, ok := h.readContractBody(w, r, opBatchShareProject)
	if !ok {
		return
	}
	req, refusal := decodeBatchShareRequest(body)
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

	remaining := make([]sqlc.ListOwnerProjectShareCandidatesRow, 0, len(selected))
	for _, candidate := range selected {
		if _, isLive := alreadyLive[candidate.ID]; isLive {
			continue
		}
		remaining = append(remaining, candidate)
	}

	if consentRefusal := consentRefusalForPrivate(remaining, req.VisibilityConfirmed); consentRefusal != "" {
		writeError(w, http.StatusUnprocessableEntity, consentRefusal)
		return
	}

	localIDs := make([]string, 0, len(selected))
	for _, candidate := range selected {
		localIDs = append(localIDs, candidate.LocalID)
	}

	var writeErr error
	result := batchShareResponse{ProjectHash: req.ProjectHash, Shared: []batchShareEntry{}, AlreadyShared: []string{}}
	if err := h.withPublishLocksMany(ctx, user.PgID(), localIDs, func(conn *pgxpool.Conn) error {
		writeErr = h.writeBatchShare(ctx, conn, user, pgGroupID, status, selected, req.VisibilityConfirmed, req.ProjectHash, alreadyLive, &result)
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
		var consent *batchConsentRefusal
		if errors.As(writeErr, &consent) {
			writeError(w, http.StatusUnprocessableEntity, consent.message)
			return
		}
		var authority *shareOperationRefusal
		if errors.As(writeErr, &authority) {
			writeError(w, authority.status, authority.message)
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf(
			"the database could not confirm the contribution of project %s. Reload current submissions and visibility "+
				"before explicitly retrying; an earlier content upload is a separate operation and is not rolled back.", req.ProjectHash))
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// writeBatchShare performs every write of one batch inside ONE transaction.
//
// Any statement error rolls back the operation. Continuing after a PostgreSQL
// error would run the remaining statements against an aborted transaction.
func (h *Handler) writeBatchShare(ctx context.Context, conn *pgxpool.Conn, user *AuthUser, groupID pgtype.UUID, status ShareStatus, candidates []sqlc.ListOwnerProjectShareCandidatesRow, visibilityConfirmed bool, projectHash string, preflightLive map[pgtype.UUID]struct{}, result *batchShareResponse) error {
	shared := dbVisibilityShared
	return h.inCollectiveAuthorityTx(ctx, conn, user.PgID(), []pgtype.UUID{groupID}, func(ctx context.Context, q Querier) error {
		currentStatus, err := finalShareStatus(ctx, q, user, groupID)
		if err != nil {
			return err
		}
		if currentStatus != status {
			return &shareOperationRefusal{http.StatusConflict, "The collective's acceptance policy changed while this contribution waited. Nothing was submitted; reload the policy and explicitly retry without changing your consent."}
		}
		for _, candidate := range candidates {
			current, err := q.GetTranscriptByID(ctx, candidate.ID)
			if err != nil {
				return err
			}
			if current.OwnerID != user.PgID() || current.ProjectHash != projectHash {
				return &shareOperationRefusal{http.StatusForbidden, "A selected transcript no longer belongs to your selected project. Nothing was submitted; reload the project and explicitly retry."}
			}
			latest, err := q.GetLatestShareAttempt(ctx, sqlc.GetLatestShareAttemptParams{TranscriptID: candidate.ID, GroupID: groupID})
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			isLive := err == nil && shareAttemptIsLive(latest.Status)
			_, wasLive := preflightLive[candidate.ID]
			if isLive != wasLive {
				return &shareOperationRefusal{http.StatusConflict, "The selected transcript " + uuid.UUID(candidate.ID.Bytes).String() + " changed contribution state while this request waited. The whole contribution was rolled back; reload the complete selection and explicitly retry. No withdrawn or rejected contribution was automatically resubmitted."}
			}
			if isLive {
				result.AlreadyShared = append(result.AlreadyShared, uuid.UUID(candidate.ID.Bytes).String())
				continue
			}
			if err := q.ShareTranscriptWithStatus(ctx, sqlc.ShareTranscriptWithStatusParams{
				TranscriptID: candidate.ID,
				GroupID:      groupID,
				Status:       string(status),
			}); err != nil {
				if kind := classifyShareAttemptConflict(err); kind != shareAttemptConflictNone {
					// Returning rolls the whole batch back, including visibility.
					return &batchShareConflict{TranscriptID: uuid.UUID(candidate.ID.Bytes).String(), Kind: kind}
				}
				return fmt.Errorf("open a share attempt for transcript %s: %w", uuid.UUID(candidate.ID.Bytes).String(), err)
			}
			result.Shared = append(result.Shared, batchShareEntry{TranscriptID: uuid.UUID(candidate.ID.Bytes).String(), Status: currentStatus})
			// Contributing a private transcript makes it visible to the
			// collective, so its visibility moves with the submission and in the
			// same transaction: the two can never be observed apart, and the
			// governance trigger records the change against this actor.
			//
			// The flip, and the consent it needs, are decided from the value
			// under the row lock, not from the candidate read before the publish
			// lock was taken. That read can be stale in both directions: a
			// republish makes a transcript private only while it replaces the
			// content and then restores it, and an owner's edit or unshare can
			// make one private while this request waits for the lock.
			locked, err := q.GetTranscriptGovernanceForUpdate(ctx, candidate.ID)
			if err != nil {
				return fmt.Errorf("lock transcript %s before recording its visibility: %w", uuid.UUID(candidate.ID.Bytes).String(), err)
			}
			if locked.Visibility != dbVisibilityPrivate {
				continue
			}
			if !visibilityConfirmed {
				// Returning rolls the whole batch back, so the refusal's
				// "nothing was written" stays true.
				privateNow := candidate
				privateNow.Visibility = dbVisibilityPrivate
				return &batchConsentRefusal{message: consentRefusalForPrivate([]sqlc.ListOwnerProjectShareCandidatesRow{privateNow}, false)}
			}
			if _, err := applyMetadataPatch(ctx, q, candidate.ID, metadataPatch{Visibility: &shared}); err != nil {
				return fmt.Errorf("record the new visibility of transcript %s: %w", uuid.UUID(candidate.ID.Bytes).String(), err)
			}
		}
		if len(result.Shared) == 0 {
			return &shareOperationRefusal{http.StatusConflict, "Every transcript in this selection is already submitted to " + pluralCollectives(len(result.AlreadyShared)) + ". Nothing was changed. Withdraw the existing submissions or wait for them to be decided before submitting again."}
		}
		return nil
	})
}

// decodeBatchShareRequest decodes and validates the already contract-checked
// body. Fields the contract does not declare are refused rather than ignored,
// which is stricter than the contract's open object on purpose: a client that
// misspells visibility_confirmed would otherwise be told its private
// transcripts need confirming while it believes it sent one.
func decodeBatchShareRequest(body contractBody) (batchShareRequest, string) {
	var req batchShareRequest
	if len(body.Undeclared) > 0 {
		return req, fmt.Sprintf(
			"this contribution request names fields the contract does not declare: %s. Refused before anything was "+
				"submitted, so nothing was written. Send project_hash, an optional transcript_ids list, and "+
				"visibility_confirmed, and no other fields.", strings.Join(body.Undeclared, ", "))
	}
	if err := json.Unmarshal(body.Declared, &req); err != nil {
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
	if r.URL.Query().Get("view") == "grouped" {
		h.listCollectiveGrouped(w, r, GroupedRouteContributable)
		return
	}
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
