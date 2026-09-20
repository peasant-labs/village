//go:build integration

package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// attachmentDigestOf reads the digest an attachment currently advertises.
func attachmentDigestOf(t *testing.T, ctx context.Context, h *Handler, attachmentID pgtype.UUID) string {
	t.Helper()
	row, err := h.queries.GetPullRequestAttachment(ctx, attachmentID)
	if err != nil {
		t.Fatalf("read attachment: %v", err)
	}
	return string(row.Digest)
}

// TestNarrowingAnAttachedTranscriptStopsAdvertisingIt_RealPostgres proves the
// pull request stops claiming prompts its readers can no longer open.
//
// Attaching to a public repository widens a transcript to public, so the digest
// advertises it. The owner then makes it private. The digest must drop the row
// and say why, the pull request must be edited rather than left as it was, and
// the binding must survive: detach still has to restore the visibility it
// recorded.
func TestNarrowingAnAttachedTranscriptStopsAdvertisingIt_RealPostgres(t *testing.T) {
	t.Parallel()
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 992004)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "widgets-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	// A public repository, so the attachment widens its transcripts to public.
	groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, false, "informational")
	sha := "abc1234000000000000000000000000000000004"
	transcriptID := attachmentSeedTranscript(t, ctx, pool, blobs, owner,
		"git@github.com:acme/"+repoName+".git", sha, "private", "feat/x", time.Now().Add(-time.Hour))

	attachment := attachmentCreatePreview(t, ctx, h, groupID, owner, "acme", repoName, sha, 7)
	fake.setPullCommits(sha)

	rec := attachmentServe(t, attachmentRouter(h), http.MethodPost, "/api/v1/pulls/acme/"+repoName+"/7/confirm", owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm status = %d (%s), want 200", rec.Code, rec.Body.String())
	}

	transcriptKey := uuidFromPg(transcriptID).String()
	if digest := attachmentDigestOf(t, ctx, h, attachment.ID); !strings.Contains(digest, transcriptKey) {
		t.Fatalf("the confirmed digest must advertise the attached transcript; digest=%s", digest)
	}
	var visibility string
	if err := pool.QueryRow(ctx, `SELECT visibility FROM transcripts WHERE id = $1`, transcriptID).Scan(&visibility); err != nil {
		t.Fatalf("read visibility: %v", err)
	}
	if visibility != "public" {
		t.Fatalf("visibility = %q, want public: attaching to a public repository widens the transcript", visibility)
	}

	editsBefore := fake.commentEdits

	// The owner narrows it, through the route a person uses.
	router := chi.NewRouter()
	router.Patch("/api/v1/transcripts/{id}", h.UpdateTranscript)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/transcripts/"+transcriptKey,
		bytes.NewReader([]byte(`{"visibility":"private"}`)))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &AuthUser{ID: uuid.UUID(owner.Bytes), Username: "attachment"}))
	updateRec := httptest.NewRecorder()
	router.ServeHTTP(updateRec, req)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("visibility update status = %d (%s), want 200", updateRec.Code, updateRec.Body.String())
	}

	if err := pool.QueryRow(ctx, `SELECT visibility FROM transcripts WHERE id = $1`, transcriptID).Scan(&visibility); err != nil {
		t.Fatalf("read visibility after the update: %v", err)
	}
	if visibility != "private" {
		t.Fatalf("visibility = %q, want private: the owner's own change must stand", visibility)
	}

	digest := attachmentDigestOf(t, ctx, h, attachment.ID)
	if strings.Contains(digest, transcriptKey) {
		t.Errorf("the digest must stop advertising a transcript its readers can no longer open; digest=%s", digest)
	}
	if fake.commentEdits <= editsBefore {
		t.Errorf("the pull request must be edited, not left advertising the old set; edits=%d", fake.commentEdits)
	}
	if !strings.Contains(fake.lastCommentBody, "No prompts are available for this pull request.") {
		t.Errorf("the comment must say no prompts are left, since this attachment binds only the one; body=%s", fake.lastCommentBody)
	}
	if strings.Contains(fake.lastCommentBody, "author") {
		t.Errorf("the comment must not attribute the change to anyone; body=%s", fake.lastCommentBody)
	}
	if !strings.Contains(fake.lastCheckText, "No prompts are available for this pull request.") {
		t.Errorf("the check must say no prompts are left, since this attachment binds only the one; summary=%s", fake.lastCheckText)
	}
	if fake.lastCheckConclusion != "neutral" {
		t.Errorf("conclusion = %q with nothing left to verify, want neutral; the check is what a reviewer reads before merging", fake.lastCheckConclusion)
	}
	if strings.Contains(fake.lastCheckText, "author") {
		t.Errorf("the check must not attribute the change to anyone; summary=%s", fake.lastCheckText)
	}

	// The binding survives, so detach can still restore what it recorded.
	binding, err := h.queries.GetPullRequestAttachmentTranscript(ctx, sqlc.GetPullRequestAttachmentTranscriptParams{AttachmentID: attachment.ID, TranscriptID: transcriptID})
	if err != nil {
		t.Fatalf("the binding must survive a visibility change: %v", err)
	}
	if binding.PreviousVisibility != "private" {
		t.Errorf("previous_visibility = %q, want the value recorded at attach", binding.PreviousVisibility)
	}

	// Detaching then restores nothing wider than the owner now wants.
	detachRec := attachmentServe(t, attachmentRouter(h), http.MethodDelete, "/api/v1/pulls/acme/"+repoName+"/7", owner)
	if detachRec.Code != http.StatusOK {
		t.Fatalf("detach status = %d (%s), want 200", detachRec.Code, detachRec.Body.String())
	}
	if err := pool.QueryRow(ctx, `SELECT visibility FROM transcripts WHERE id = $1`, transcriptID).Scan(&visibility); err != nil {
		t.Fatalf("read visibility after detach: %v", err)
	}
	if visibility != "private" {
		t.Errorf("visibility = %q after detach, want private: detaching must not re-publish what the owner narrowed", visibility)
	}
}

// TestWideningAnAttachedTranscriptRestoresIt_RealPostgres proves the trigger
// works in both directions.
//
// Narrowing withdraws the row; widening it back must put the row where it was,
// rather than leaving the pull request advertising less than the transcripts it
// holds until some later refresh happens to notice.
func TestWideningAnAttachedTranscriptRestoresIt_RealPostgres(t *testing.T) {
	t.Parallel()
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 992005)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "widgets-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, false, "informational")
	sha := "abc1234000000000000000000000000000000005"
	transcriptID := attachmentSeedTranscript(t, ctx, pool, blobs, owner,
		"git@github.com:acme/"+repoName+".git", sha, "private", "feat/x", time.Now().Add(-time.Hour))

	// A number of its own: the attachment an owner/repository/number names is
	// created under a conflict key of the GitHub repository id and the number,
	// and the test helper shares one repository id across its cases.
	attachment := attachmentCreatePreview(t, ctx, h, groupID, owner, "acme", repoName, sha, 8)
	fake.setPullCommits(sha)
	if rec := attachmentServe(t, attachmentRouter(h), http.MethodPost, "/api/v1/pulls/acme/"+repoName+"/8/confirm", owner); rec.Code != http.StatusOK {
		t.Fatalf("confirm status = %d (%s), want 200", rec.Code, rec.Body.String())
	}

	transcriptKey := uuidFromPg(transcriptID).String()
	router := chi.NewRouter()
	router.Patch("/api/v1/transcripts/{id}", h.UpdateTranscript)
	setVisibility := func(value string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/transcripts/"+transcriptKey,
			bytes.NewReader([]byte(`{"visibility":"`+value+`"}`)))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &AuthUser{ID: uuid.UUID(owner.Bytes), Username: "attachment"}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("set visibility %s: status = %d (%s), want 200", value, rec.Code, rec.Body.String())
		}
	}

	setVisibility("private")
	if digest := attachmentDigestOf(t, ctx, h, attachment.ID); strings.Contains(digest, transcriptKey) {
		t.Fatalf("the digest must drop a transcript that fell below what the repository requires; digest=%s", digest)
	}

	setVisibility("public")
	if digest := attachmentDigestOf(t, ctx, h, attachment.ID); !strings.Contains(digest, transcriptKey) {
		t.Errorf("the digest must advertise the transcript again once the owner widens it back; digest=%s", digest)
	}
	if strings.Contains(fake.lastCommentBody, "no longer listed here") {
		t.Errorf("the comment must not keep saying a row is gone after it came back; body=%s", fake.lastCommentBody)
	}
}

// TestRepublishedNarrowingDropsTheDigestRow_RealPostgres covers the path that
// had no test: a republish narrows a transcript before replacing its content,
// and that narrowing commits even though the publish hook cannot see it —
// nothing new is accepted for the attachment and the head has not moved. The
// digest must stop advertising the transcript, without a second click.
func TestRepublishedNarrowingDropsTheDigestRow_RealPostgres(t *testing.T) {
	t.Parallel()
	h, pool, _, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 992006)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "widgets-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, false, "informational")
	sha := "abc1234000000000000000000000000000000006"
	remote := "git@github.com:acme/" + repoName + ".git"
	sessionID := uuid.NewString()

	code, body := attachmentPublishSession(t, h, owner, "attachment-owner", remote, sha, sessionID, attachmentPublicationContent())
	if code != http.StatusCreated {
		t.Fatalf("first publish status = %d (%s), want 201", code, body)
	}

	var transcriptID pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM transcripts WHERE owner_id = $1 AND local_id = $2`, owner, sessionID).Scan(&transcriptID); err != nil {
		t.Fatalf("read the published transcript: %v", err)
	}

	attachment := attachmentCreatePreview(t, ctx, h, groupID, owner, "acme", repoName, sha, 9)
	fake.setPullCommits(sha)
	if rec := attachmentServe(t, attachmentRouter(h), http.MethodPost, "/api/v1/pulls/acme/"+repoName+"/9/confirm", owner); rec.Code != http.StatusOK {
		t.Fatalf("confirm status = %d (%s), want 200", rec.Code, rec.Body.String())
	}

	transcriptKey := uuidFromPg(transcriptID).String()
	if digest := attachmentDigestOf(t, ctx, h, attachment.ID); !strings.Contains(digest, transcriptKey) {
		t.Fatalf("the confirmed digest must advertise the attached transcript; digest=%s", digest)
	}

	// The same session, republished with different content: the handler replaces
	// the stored content, and narrows the transcript before it does.
	revised := bytes.Replace(attachmentPublicationContent(),
		[]byte("please attach my prompts"), []byte("please attach my prompts, revised"), 1)
	code, body = attachmentPublishSession(t, h, owner, "attachment-owner", remote, sha, sessionID, revised)
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("republish status = %d (%s), want 201 or 200", code, body)
	}

	if digest := attachmentDigestOf(t, ctx, h, attachment.ID); strings.Contains(digest, transcriptKey) {
		t.Errorf("a republish that narrowed the transcript must drop it from the digest; digest=%s", digest)
	}
	if !strings.Contains(fake.lastCommentBody, "No prompts are available for this pull request.") {
		t.Errorf("the comment must say no prompts are left, since this attachment binds only the one; body=%s", fake.lastCommentBody)
	}
	var visibility string
	if err := pool.QueryRow(ctx, `SELECT visibility FROM transcripts WHERE id = $1`, transcriptID).Scan(&visibility); err != nil {
		t.Fatalf("read visibility after the republish: %v", err)
	}
	if visibility != "private" {
		t.Errorf("visibility = %q after the republish, want private", visibility)
	}
	binding, err := h.queries.GetPullRequestAttachmentTranscript(ctx, sqlc.GetPullRequestAttachmentTranscriptParams{
		AttachmentID: attachment.ID, TranscriptID: transcriptID,
	})
	if err != nil {
		t.Fatalf("the binding must survive the republish: %v", err)
	}
	if binding.PreviousVisibility != "private" {
		t.Errorf("previous_visibility = %q, want the value recorded at attach", binding.PreviousVisibility)
	}
}

// TestAllDroppedPromptsAreNeutralAndSaidPlainly_RealPostgres is the state where
// nothing is left: every bound transcript has been narrowed below what the
// repository requires, so the digest has no rows under it. The check is what a
// reviewer looks at before merging, so a required check must stop reporting
// success, and what it says must be that no prompts are available rather than how
// many went — a count invites the reader to work out which, and the reason one
// left describes its owner's action.
func TestAllDroppedPromptsAreNeutralAndSaidPlainly_RealPostgres(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	author := attachmentInsertOwner(t, ctx, pool, 995001)
	defer cleanupOwners(t, ctx, pool, author)
	authorAuth := &AuthUser{ID: uuid.UUID(author.Bytes), Username: "attachment-author"}

	repoName := "all-dropped-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, author, "acme", repoName, true, "required")
	sha := "aaa5555000000000000000000000000000000001"
	first := attachmentSeedTranscript(t, ctx, pool, blobs, author,
		"git@github.com:acme/"+repoName+".git", sha, "private", "", time.Now().Add(-2*time.Hour))
	second := attachmentSeedTranscript(t, ctx, pool, blobs, author,
		"git@github.com:acme/"+repoName+".git", sha, "private", "", time.Now().Add(-time.Hour))

	attachmentCreatePreview(t, ctx, h, groupID, author, "acme", repoName, sha, 7)
	fake.setPullCommits(sha)
	attachmentConfirm(t, h, author, "acme", repoName, 7)

	// With both bound transcripts advertised, a required check passes.
	if fake.lastCheckConclusion != "success" {
		t.Fatalf("conclusion = %q with prompts attached, want success", fake.lastCheckConclusion)
	}

	// The owner narrows one of them. The check stays green, because what is left
	// is what the review is about, and the reader is told a row is gone without
	// being told which or why.
	if rec := transcriptVisibilityPatch(t, h, authorAuth, first, "private"); rec.Code != http.StatusOK {
		t.Fatalf("narrow status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if fake.lastCheckConclusion != "success" {
		t.Fatalf("conclusion = %q with one of two prompts left, want success: the remaining prompt is what the review is about", fake.lastCheckConclusion)
	}
	if !strings.Contains(fake.lastCheckText, "1 transcript is no longer listed here.") {
		t.Errorf("the check must say a row is gone, without saying which; summary=%s", fake.lastCheckText)
	}

	// The owner narrows the second. Now the attachment is still attached and has
	// nothing left to advertise.
	if rec := transcriptVisibilityPatch(t, h, authorAuth, second, "private"); rec.Code != http.StatusOK {
		t.Fatalf("narrow status = %d (%s), want 200", rec.Code, rec.Body.String())
	}

	if fake.lastCheckConclusion != "neutral" {
		t.Fatalf("conclusion = %q with nothing left to verify, want neutral: a required check reporting success over an empty digest tells a reviewer the opposite of what the pull request advertises", fake.lastCheckConclusion)
	}
	const said = "No prompts are available for this pull request."
	if !strings.Contains(fake.lastCheckText, said) {
		t.Errorf("the check must say no prompts are available; summary=%s", fake.lastCheckText)
	}
	if !strings.Contains(fake.lastCommentBody, said) {
		t.Errorf("the comment must say it too, or a reader of the pull request is told nothing; body=%s", fake.lastCommentBody)
	}
	if strings.Contains(fake.lastCheckText, "no longer listed here") || strings.Contains(fake.lastCommentBody, "no longer listed here") {
		t.Errorf("nothing is left, so a count of what went is not what to say; summary=%s", fake.lastCheckText)
	}
	if strings.Contains(fake.lastCheckText, "0 sessions") || strings.Contains(fake.lastCommentBody, "0 sessions") {
		t.Errorf("the digest rendered a header with no rows under it, which reads as a rendering fault rather than a state; summary=%s", fake.lastCheckText)
	}
}
