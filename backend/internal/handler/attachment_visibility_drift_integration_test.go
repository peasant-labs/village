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
	if !strings.Contains(fake.lastCommentBody, "no longer listed here") {
		t.Errorf("the comment must say a row is gone; body=%s", fake.lastCommentBody)
	}
	if strings.Contains(fake.lastCommentBody, "author") {
		t.Errorf("the comment must not attribute the change to anyone; body=%s", fake.lastCommentBody)
	}
	if !strings.Contains(fake.lastCheckText, "no longer listed here") {
		t.Errorf("the check must say a row is gone; summary=%s", fake.lastCheckText)
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
