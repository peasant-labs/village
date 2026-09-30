package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/promptattach"
)

// The pull request reads the transcript, home, and collective pages show: a
// transcript's pull request list, the summary on a list row, the collective's
// count, and the caller's own count. Each starts from transcripts the caller may
// already read - the transcript's own read check (canViewTranscript), the list's
// own visibility, or the collective's data access, under which a viewer the
// collective withholds its transcripts from is counted nothing - and then asks
// one question of each attachment bound to them, answered by
// pullRequestReadable:
//
//   - an attachment on a public repository is readable by anyone who can read
//     the transcript;
//   - an attachment on a private repository is readable by the pull request's
//     author and by a member of the collective the attachment belongs to;
//   - anything else is omitted, never refused, so the answer does not confirm
//     that it exists. That includes an attachment whose repository link is gone,
//     whose visibility can no longer be checked.
//
// The contract also allows a reader GitHub admits to a private repository to see
// its attached attachments. Village does not grant access through a
// repository's readers, so it does not ask GitHub here either: such an
// attachment is omitted, which is the direction the contract's rule fails in.
//
// requested, waiting, and preview attachments are never listed: they are the
// author's own steps, not something attached to a pull request yet. Every count
// covers attached attachments only.

// pullRequestSummaryRecentLimit is how many pull requests a list row names
// beside its count, so a row reads "#42, #45 +2" without one read per row.
const pullRequestSummaryRecentLimit = 3

// pullRequestCandidate is one (transcript, attachment) binding with the facts
// the visibility rule needs: the pull request, its state and author, the
// repository link the attachment's collective holds now, and whether the viewer
// is a member of that collective.
type pullRequestCandidate struct {
	transcriptID   pgtype.UUID
	owner          string
	name           string
	number         int
	state          string
	authorID       pgtype.UUID
	installationID int64
	isPrivate      bool
	viewerIsMember bool
}

// candidateOf maps one candidate row. The owner and collective reads select
// exactly the transcript read's columns, and their rows convert to its row type,
// so the three statements cannot drift apart without this failing to compile.
func candidateOf(row sqlc.ListPullRequestCandidatesByTranscriptsRow) pullRequestCandidate {
	return pullRequestCandidate{
		transcriptID: row.TranscriptID, owner: row.RepoOwner, name: row.RepoName, number: int(row.Number),
		state: row.State, authorID: row.AuthorID, installationID: row.InstallationID,
		isPrivate: row.IsPrivate, viewerIsMember: row.ViewerIsMember,
	}
}

func candidatesFromTranscriptRows(rows []sqlc.ListPullRequestCandidatesByTranscriptsRow) []pullRequestCandidate {
	out := make([]pullRequestCandidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, candidateOf(row))
	}
	return out
}

func candidatesFromOwnerRows(rows []sqlc.ListAttachedPullRequestCandidatesByOwnerRow) []pullRequestCandidate {
	out := make([]pullRequestCandidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, candidateOf(sqlc.ListPullRequestCandidatesByTranscriptsRow(row)))
	}
	return out
}

func candidatesFromGroupRows(rows []sqlc.ListAttachedPullRequestCandidatesByGroupRow) []pullRequestCandidate {
	out := make([]pullRequestCandidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, candidateOf(sqlc.ListPullRequestCandidatesByTranscriptsRow(row)))
	}
	return out
}

// pullRequestKey names one pull request case-insensitively on the repository,
// the way the attachment routes address it, so the same pull request is listed
// and counted once.
func pullRequestKey(c pullRequestCandidate) string {
	return pullRequestKeyOf(c.owner, c.name, c.number)
}

func pullRequestKeyOf(owner, name string, number int) string {
	return strings.ToLower(owner) + "/" + strings.ToLower(name) + "#" + strconv.Itoa(number)
}

func pullRequestRefOf(c pullRequestCandidate) schema.VillagePullRequestRef {
	return schema.VillagePullRequestRef{Owner: c.owner, Name: c.name, Number: c.number}
}

func emptyPullRequestsSummary() schema.VillagePullRequestsSummary {
	return schema.VillagePullRequestsSummary{Recent: []schema.VillagePullRequestRef{}}
}

// viewerPgID is the viewer's id for a candidate query: NULL for an anonymous
// reader, which no membership row can match.
func viewerPgID(viewer *AuthUser) pgtype.UUID {
	if viewer == nil {
		return pgtype.UUID{}
	}
	return viewer.PgID()
}

// pullRequestReadable is the visibility rule, in the one place all four reads
// apply it.
func pullRequestReadable(viewer pgtype.UUID, c pullRequestCandidate) bool {
	if !c.isPrivate {
		return true
	}
	if !viewer.Valid {
		return false
	}
	return viewer == c.authorID || c.viewerIsMember
}

// readablePullRequests returns the candidates the viewer may read, each pull
// request once per transcript, in the order the query returned them.
func readablePullRequests(viewer pgtype.UUID, candidates []pullRequestCandidate) []pullRequestCandidate {
	out := make([]pullRequestCandidate, 0, len(candidates))
	seen := map[pgtype.UUID]map[string]bool{}
	for _, c := range candidates {
		key := pullRequestKey(c)
		if seen[c.transcriptID][key] || !pullRequestReadable(viewer, c) {
			continue
		}
		if seen[c.transcriptID] == nil {
			seen[c.transcriptID] = map[string]bool{}
		}
		seen[c.transcriptID][key] = true
		out = append(out, c)
	}
	return out
}

// countReadablePullRequests counts the distinct pull requests the viewer may
// read, however many transcripts each one binds.
func countReadablePullRequests(viewer pgtype.UUID, candidates []pullRequestCandidate) int32 {
	counted := map[string]bool{}
	for _, c := range readablePullRequests(viewer, candidates) {
		counted[pullRequestKey(c)] = true
	}
	return int32(len(counted))
}

// pullRequestSummaries answers the pull request summary of every transcript on
// a page with one query, whatever the page holds. Every requested transcript
// gets a summary, empty when nothing readable is attached.
func (h *Handler) pullRequestSummaries(ctx context.Context, viewer *AuthUser, transcriptIDs []pgtype.UUID) (map[pgtype.UUID]schema.VillagePullRequestsSummary, error) {
	summaries := make(map[pgtype.UUID]schema.VillagePullRequestsSummary, len(transcriptIDs))
	for _, id := range transcriptIDs {
		summaries[id] = emptyPullRequestsSummary()
	}
	if len(transcriptIDs) == 0 {
		return summaries, nil
	}
	rows, err := h.queries.ListPullRequestCandidatesByTranscripts(ctx, sqlc.ListPullRequestCandidatesByTranscriptsParams{
		ViewerID:      viewerPgID(viewer),
		TranscriptIds: transcriptIDs,
		States:        []string{string(promptattach.Attached)},
	})
	if err != nil {
		return nil, err
	}
	for _, c := range readablePullRequests(viewerPgID(viewer), candidatesFromTranscriptRows(rows)) {
		summary, requested := summaries[c.transcriptID]
		if !requested {
			continue
		}
		summary.Count++
		if len(summary.Recent) < pullRequestSummaryRecentLimit {
			summary.Recent = append(summary.Recent, pullRequestRefOf(c))
		}
		summaries[c.transcriptID] = summary
	}
	return summaries, nil
}

// collectivePullRequestCountFor counts the distinct attached pull requests the
// viewer may read among the transcripts a collective's totals count. A viewer
// the collective does not let read its transcripts is withheld those
// transcripts, and so what they are attached to: they are counted zero without
// asking.
func (h *Handler) collectivePullRequestCountFor(ctx context.Context, viewer *AuthUser, groupID pgtype.UUID, canRead bool) (int32, error) {
	if !canRead {
		return 0, nil
	}
	rows, err := h.queries.ListAttachedPullRequestCandidatesByGroup(ctx, sqlc.ListAttachedPullRequestCandidatesByGroupParams{
		ViewerID: viewerPgID(viewer),
		GroupID:  groupID,
	})
	if err != nil {
		return 0, err
	}
	return countReadablePullRequests(viewerPgID(viewer), candidatesFromGroupRows(rows)), nil
}

// fillCollectivePullRequestSummaries sets the pull request summary on every
// collective row of a page with one query. A row is replaced rather than
// mutated in place, so no summary can reach a row another request holds.
func (h *Handler) fillCollectivePullRequestSummaries(ctx context.Context, items []schema.VillageSessionListItem) error {
	ids := make([]pgtype.UUID, 0, len(items))
	for _, item := range items {
		if item.Transcript == nil || item.Transcript.Collective == nil {
			continue
		}
		id, err := uuid.Parse(string(item.Transcript.Collective.ID))
		if err != nil {
			return err
		}
		ids = append(ids, toPgUUID(id))
	}
	if len(ids) == 0 {
		return nil
	}
	summaries, err := h.pullRequestSummaries(ctx, GetUser(ctx), ids)
	if err != nil {
		return err
	}
	for i := range items {
		if items[i].Transcript == nil || items[i].Transcript.Collective == nil {
			continue
		}
		id, _ := uuid.Parse(string(items[i].Transcript.Collective.ID))
		row := *items[i].Transcript
		collective := *row.Collective
		collective.PullRequests = summaries[toPgUUID(id)]
		row.Collective = &collective
		items[i].Transcript = &row
	}
	return nil
}

// GET /api/v1/transcripts/{id}/pulls (AuthOptional)
//
// The pull requests whose attachment includes this transcript, attached or
// detached, that the caller may read, newest first. Each row names the pull
// request, its title and head branch, and whether it is attached - nothing of
// the attachment's own bookkeeping. A caller who may not read the transcript
// gets 404, never 403.
func (h *Handler) ListTranscriptPullRequests(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid transcript ID")
		return
	}
	transcript, err := h.queries.GetTranscriptByID(r.Context(), toPgUUID(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "Transcript not found")
		return
	}
	// The transcript's own read check. Access through a repository's readers is
	// not consulted, so this read never asks GitHub who the caller is and is
	// never throttled by that question.
	user := GetUser(r.Context())
	if !h.canViewTranscript(r.Context(), user, transcript) {
		writeError(w, http.StatusNotFound, "Transcript not found")
		return
	}

	rows, err := h.queries.ListPullRequestCandidatesByTranscripts(r.Context(), sqlc.ListPullRequestCandidatesByTranscriptsParams{
		ViewerID:      viewerPgID(user),
		TranscriptIds: []pgtype.UUID{transcript.ID},
		States:        []string{string(promptattach.Attached), string(promptattach.Detached)},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read this transcript's pull requests; retry the request")
		return
	}
	readable := readablePullRequests(viewerPgID(user), candidatesFromTranscriptRows(rows))
	details := h.pullRequestDetailsFor(r.Context(), readable)

	response := schema.VillageTranscriptPullRequestsResponse{PullRequests: make([]schema.VillageTranscriptPullRequest, 0, len(readable))}
	for i, c := range readable {
		response.PullRequests = append(response.PullRequests, schema.VillageTranscriptPullRequest{
			Owner: c.owner, Name: c.name, Number: c.number,
			Title: details[i].title, HeadRef: details[i].headRef,
			State: schema.VillagePullRequestAttachmentState(c.state),
		})
	}
	if err := response.Validate(); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read this transcript's pull requests; retry the request")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// GET /api/v1/users/me/stats (AuthRequired)
//
// Totals over every transcript the caller published, and the distinct attached
// pull requests those transcripts are bound to that the caller may read.
func (h *Handler) GetMyStats(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	totals, err := h.queries.GetOwnerTranscriptTotals(r.Context(), user.PgID())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read your totals; retry the request")
		return
	}
	rows, err := h.queries.ListAttachedPullRequestCandidatesByOwner(r.Context(), sqlc.ListAttachedPullRequestCandidatesByOwnerParams{
		ViewerID: user.PgID(),
		OwnerID:  user.PgID(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read your totals; retry the request")
		return
	}
	stats := schema.VillageUserStats{
		TotalTranscripts: totals.TotalTranscripts,
		TotalTurns:       totals.TotalTurns,
		TotalDurationMs:  totals.TotalDurationMs,
		TotalTokens:      totals.TotalTokens,
		PullRequestCount: countReadablePullRequests(user.PgID(), candidatesFromOwnerRows(rows)),
	}
	if err := stats.Validate(); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read your totals; retry the request")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}
