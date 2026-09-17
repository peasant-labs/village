//go:build integration

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// transcriptViewAs drives the mounted read route a digest links to, as the given
// reader. A nil user is an anonymous reader.
func transcriptViewAs(t *testing.T, h *Handler, user *AuthUser, transcriptID pgtype.UUID) *httptest.ResponseRecorder {
	t.Helper()
	id := uuid.UUID(transcriptID.Bytes).String()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+id, nil)
	r = withChiURLParam(r, "id", id)
	if user != nil {
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, user))
	}
	w := httptest.NewRecorder()
	h.GetTranscript(w, r)
	return w
}

// attachPrivateRepoTranscript builds the state the private path exists for: one
// transcript, bound and attached to a pull request in a linked PRIVATE
// repository, with the attachment reached the way production reaches it (preview,
// then the author's confirm). It returns the transcript id and the repository
// name so a test can ask who may read it.
func attachPrivateRepoTranscript(t *testing.T, h *Handler, pool *pgxpool.Pool, blobs *recordingTranscriptBlobStore, fake *attachmentGitHubFake, author pgtype.UUID, number int, sha string) (pgtype.UUID, string) {
	t.Helper()
	ctx := context.Background()
	repoName := "readers-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, author, "acme", repoName, true, "informational")
	transcriptID := attachmentSeedTranscript(t, ctx, pool, blobs, author,
		"git@github.com:acme/"+repoName+".git", sha, "private", "", time.Now().Add(-time.Hour))

	attachmentCreatePreview(t, ctx, h, groupID, author, "acme", repoName, sha, number)
	fake.setPullCommits(sha)
	attachmentConfirm(t, h, author, "acme", repoName, number)
	return transcriptID, repoName
}

// TestRepositoryReadersOpenAttachedPrompts_RealPostgres is the private path end
// to end: a reader outside the collective, whom every collected check refuses, is
// admitted to an attached transcript when GitHub says they may read the
// repository it is attached to.
//
// Nothing about the admission is recorded, so the same reader is refused again
// the moment GitHub's answer changes — which is what makes the grant live rather
// than a row that would need revoking.
func TestRepositoryReadersOpenAttachedPrompts_RealPostgres(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	author := attachmentInsertOwner(t, ctx, pool, 994001)
	defer cleanupOwners(t, ctx, pool, author)
	transcriptID, _ := attachPrivateRepoTranscript(t, h, pool, blobs, fake, author, 7, "abc9999000000000000000000000000000000001")

	reader := attachmentInsertOwner(t, ctx, pool, 994002)
	defer cleanupOwners(t, ctx, pool, reader)
	readerAuth := &AuthUser{ID: uuid.UUID(reader.Bytes), Username: "repo-reader"}

	// Every collected check refuses this reader: they are not the owner, not in
	// the collective, and the transcript is not public. Only GitHub admits them.
	fake.setRepoReader("994002", "reader-login", "read")
	if rec := transcriptViewAs(t, h, readerAuth, transcriptID); rec.Code != http.StatusOK {
		t.Fatalf("status = %d for a reader GitHub admits to the repository, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// Accepting any permission at or above read: admin and write are not a
	// different answer from read.
	fake.setRepoReader("994002", "reader-login", "admin")
	if rec := transcriptViewAs(t, h, readerAuth, transcriptID); rec.Code != http.StatusOK {
		t.Fatalf("status = %d for a reader GitHub calls admin, want 200", rec.Code)
	}

	// GitHub refusing is the collective-only refusal, unchanged, and it takes
	// effect with nothing else happening: the answer is not remembered.
	fake.setRepoReader("994002", "reader-login", "none")
	if rec := transcriptViewAs(t, h, readerAuth, transcriptID); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d for a reader GitHub refuses, want 404: a reader with no repository access is refused exactly as a stranger is", rec.Code)
	}

	// A GitHub failure denies rather than admits.
	fake.setRepoReader("994002", "reader-login", "read")
	fake.failRepoReads(true)
	if rec := transcriptViewAs(t, h, readerAuth, transcriptID); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d while GitHub was failing, want 404: an access question that cannot be answered must deny", rec.Code)
	}
	fake.failRepoReads(false)

	// A reader who signed in through another provider has no GitHub identity to
	// ask about, so nothing can answer for them.
	if _, err := pool.Exec(ctx, `UPDATE users SET provider = 'gitlab', provider_user_id = 'gitlab-repo-reader' WHERE id = $1`, reader); err != nil {
		t.Fatal(err)
	}
	if rec := transcriptViewAs(t, h, readerAuth, transcriptID); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d for a reader with no GitHub identity, want 404", rec.Code)
	}
}

// TestRepositoryReadIsScopedToTheAttachment_RealPostgres proves the grant is the
// ATTACHMENT, not the reader's access to the repository: admitted through one
// attachment does not open a sibling transcript from the same repository, and
// detaching closes the attachment's own transcripts again.
func TestRepositoryReadIsScopedToTheAttachment_RealPostgres(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	author := attachmentInsertOwner(t, ctx, pool, 994011)
	defer cleanupOwners(t, ctx, pool, author)
	transcriptID, repoName := attachPrivateRepoTranscript(t, h, pool, blobs, fake, author, 11, "abc9999000000000000000000000000000000011")

	// A second transcript from the same repository that no attachment binds.
	sibling := attachmentSeedTranscript(t, ctx, pool, blobs, author,
		"git@github.com:acme/"+repoName+".git", "ddd9999000000000000000000000000000000012", "private", "", time.Now().Add(-time.Hour))

	reader := attachmentInsertOwner(t, ctx, pool, 994012)
	defer cleanupOwners(t, ctx, pool, reader)
	readerAuth := &AuthUser{ID: uuid.UUID(reader.Bytes), Username: "repo-reader"}
	fake.setRepoReader("994012", "reader-login", "read")

	if rec := transcriptViewAs(t, h, readerAuth, transcriptID); rec.Code != http.StatusOK {
		t.Fatalf("status = %d for the attached transcript, want 200: the reader is admitted through this attachment (body: %s)", rec.Code, rec.Body.String())
	}
	if rec := transcriptViewAs(t, h, readerAuth, sibling); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d for a sibling transcript no attachment binds, want 404: repository access admits a reader to the attachment, never to the repository's other prompts", rec.Code)
	}

	// Detaching ends it: the transcript is no longer attached to anything, so
	// there is nothing for the reader to have been admitted through.
	rec := attachmentServe(t, attachmentRouter(h), http.MethodDelete, "/api/v1/pulls/acme/"+repoName+"/11", author)
	if rec.Code != http.StatusOK {
		t.Fatalf("detach status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if rec := transcriptViewAs(t, h, readerAuth, transcriptID); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d after detach, want 404: a detached attachment admits nobody", rec.Code)
	}
}
