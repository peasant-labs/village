//go:build integration

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/schema"
)

// A reader GitHub admits to a private repository may open the page for its pull
// request, which they can already read on GitHub. That admits them to the page
// and to nothing else: attaching never changes who can read a transcript, so
// they read a transcript, and see its title and prompts on the page, only when
// the transcript's own audience includes them.

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

// attachPrivateRepoTranscript attaches one transcript at the given visibility to
// a pull request in a linked PRIVATE repository, the way production reaches it
// (preview, then the author's confirm), and titles it so a test can look for the
// title. It returns the transcript id and the repository name.
func attachPrivateRepoTranscript(t *testing.T, h *Handler, pool *pgxpool.Pool, blobs *recordingTranscriptBlobStore, fake *attachmentGitHubFake, author pgtype.UUID, number int, sha, visibility string) (pgtype.UUID, string) {
	t.Helper()
	ctx := context.Background()
	repoName := "readers-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, author, "acme", repoName, true, "informational")
	transcriptID := attachmentSeedTranscript(t, ctx, pool, blobs, author,
		"git@github.com:acme/"+repoName+".git", sha, visibility, "", time.Now().Add(-time.Hour))
	if _, err := pool.Exec(ctx, `UPDATE transcripts SET title = 'readers title' WHERE id = $1`, transcriptID); err != nil {
		t.Fatalf("title the transcript: %v", err)
	}
	if visibility == dbVisibilityShared {
		if _, err := pool.Exec(ctx, `
			INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status)
			VALUES ($1, $2, 1, 'approved')`, transcriptID, groupID); err != nil {
			t.Fatalf("share the transcript with the linking collective: %v", err)
		}
	}

	attachmentCreatePreview(t, ctx, h, groupID, author, "acme", repoName, sha, number)
	fake.setPullCommits(sha)
	attachmentConfirm(t, h, author, "acme", repoName, number)
	return transcriptID, repoName
}

// pageAs drives the mounted route the app's own comment and check link to, as
// the given viewer.
func pageAs(t *testing.T, h *Handler, user *AuthUser, owner, name string, number int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/pulls/%s/%s/%d", owner, name, number), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("owner", owner)
	rctx.URLParams.Add("name", name)
	rctx.URLParams.Add("number", strconv.Itoa(number))
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
	if user != nil {
		ctx = context.WithValue(ctx, UserContextKey, user)
	}
	w := httptest.NewRecorder()
	h.GetPullRequestAttachment(w, r.WithContext(ctx))
	return w
}

// transcriptVisibilityPatch narrows or widens a transcript through the mounted
// route its owner uses.
func transcriptVisibilityPatch(t *testing.T, h *Handler, user *AuthUser, transcriptID pgtype.UUID, visibility string) *httptest.ResponseRecorder {
	t.Helper()
	id := uuid.UUID(transcriptID.Bytes).String()
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/transcripts/"+id, strings.NewReader(`{"visibility":"`+visibility+`"}`))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, UserContextKey, user)
	w := httptest.NewRecorder()
	h.UpdateTranscript(w, r.WithContext(ctx))
	return w
}

// transcriptAnnotationsAs drives the annotation read route as the given viewer.
func transcriptAnnotationsAs(t *testing.T, h *Handler, user *AuthUser, transcriptID pgtype.UUID) *httptest.ResponseRecorder {
	t.Helper()
	id := uuid.UUID(transcriptID.Bytes).String()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+id+"/annotations", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, UserContextKey, user)
	w := httptest.NewRecorder()
	h.ListTranscriptAnnotations(w, r.WithContext(ctx))
	return w
}

// TestRepositoryReadersDoNotReadAttachedTranscripts_RealPostgres is the grant
// that is gone. A shared transcript attached to a private repository's pull
// request stays its collective's: a reader GitHub admits to the repository, at
// any permission, is refused the transcript and its annotations exactly as a
// stranger is, and the transcript routes never ask GitHub at all. The page still
// opens for them, without the transcript's title or prompts.
func TestRepositoryReadersDoNotReadAttachedTranscripts_RealPostgres(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	author := attachmentInsertOwner(t, ctx, pool, 994001)
	defer cleanupOwners(t, ctx, pool, author)
	transcriptID, repoName := attachPrivateRepoTranscript(t, h, pool, blobs, fake, author, 7, "abc9999000000000000000000000000000000001", dbVisibilityShared)

	reader := attachmentInsertOwner(t, ctx, pool, 994002)
	defer cleanupOwners(t, ctx, pool, reader)
	readerAuth := &AuthUser{ID: uuid.UUID(reader.Bytes), Username: "repo-reader"}

	for _, permission := range []string{"read", "admin"} {
		fake.setRepoReader("994002", "reader-login", permission)
		if rec := transcriptViewAs(t, h, readerAuth, transcriptID); rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d for a reader GitHub admits at %s, want 404: repository access is not a grant to read a transcript (body: %s)", rec.Code, permission, rec.Body.String())
		}
		if rec := transcriptAnnotationsAs(t, h, readerAuth, transcriptID); rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d reading annotations as a reader GitHub admits at %s, want 404", rec.Code, permission)
		}
	}
	if asked := fake.permissionAskCount(); asked != 0 {
		t.Fatalf("the transcript routes asked GitHub %d time(s); they never ask about a repository", asked)
	}

	rec := pageAs(t, h, readerAuth, "acme", repoName, 7)
	if rec.Code != http.StatusOK {
		t.Fatalf("page status = %d for a reader GitHub admits, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var response schema.VillagePullRequestAttachmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode the page: %v", err)
	}
	if strings.Contains(rec.Body.String(), attachedPrompt) || strings.Contains(rec.Body.String(), "readers title") {
		t.Fatalf("the page shows a repository reader the title or prompts of a transcript they cannot read: %s", rec.Body.String())
	}
	if len(response.Transcripts) != 1 || response.Transcripts[0].Title != nil {
		t.Fatalf("transcripts = %+v, want the one bound, listed without its title", response.Transcripts)
	}
	if response.Digest == nil || len(response.Digest.Items) != 0 || response.Digest.Header.PromptCount != 0 {
		t.Fatalf("digest = %+v, want an empty chain for a reader who can open none of it", response.Digest)
	}
}

// TestRepositoryReadersOpenThePullRequestPage_RealPostgres is who the page
// admits on a private repository and what it shows them. A public transcript's
// prompts are anyone's, so an admitted reader sees them; GitHub refusing,
// failing, or having no identity to ask about is the collective-only refusal;
// and a preview stays the author's own review step.
func TestRepositoryReadersOpenThePullRequestPage_RealPostgres(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	author := attachmentInsertOwner(t, ctx, pool, 994021)
	defer cleanupOwners(t, ctx, pool, author)
	authorAuth := &AuthUser{ID: uuid.UUID(author.Bytes), Username: "attachment-author"}
	_, repoName := attachPrivateRepoTranscript(t, h, pool, blobs, fake, author, 21, "abc9999000000000000000000000000000000021", dbVisibilityPublic)

	reader := attachmentInsertOwner(t, ctx, pool, 994022)
	defer cleanupOwners(t, ctx, pool, reader)
	readerAuth := &AuthUser{ID: uuid.UUID(reader.Bytes), Username: "repo-reader"}

	fake.setRepoReader("994022", "reader-login", "read")
	rec := pageAs(t, h, readerAuth, "acme", repoName, 21)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d for a repository reader, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), attachedPrompt) {
		t.Fatalf("the page must show an admitted reader the prompts of a public transcript: %s", rec.Body.String())
	}

	// A GitHub failure denies rather than admits. This runs before any refusal
	// is remembered, so the answer comes from the failed question itself.
	fake.failRepoReads(true)
	if rec := pageAs(t, h, readerAuth, "acme", repoName, 21); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d while GitHub was failing, want 404: an access question that cannot be answered must deny", rec.Code)
	}
	fake.failRepoReads(false)

	fake.setRepoReader("994022", "reader-login", "none")
	if rec := pageAs(t, h, readerAuth, "acme", repoName, 21); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d for a reader GitHub refuses, want 404", rec.Code)
	}

	other := attachmentInsertOwner(t, ctx, pool, 994023)
	defer cleanupOwners(t, ctx, pool, other)
	if _, err := pool.Exec(ctx, `UPDATE users SET provider = 'gitlab', provider_user_id = 'gitlab-repo-reader' WHERE id = $1`, other); err != nil {
		t.Fatal(err)
	}
	fake.setRepoReader("994023", "other-login", "read")
	if rec := pageAs(t, h, &AuthUser{ID: uuid.UUID(other.Bytes), Username: "other"}, "acme", repoName, 21); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d for a reader with no GitHub identity, want 404: nothing can answer for them", rec.Code)
	}

	// A preview is the author's own review step. Repository access does not open
	// a digest the author has not confirmed, and the author still sees their own.
	fake.setRepoReader("994022", "reader-login", "read")
	previewRepo := "readers-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	previewGroup := attachmentLinkCollective(t, ctx, pool, author, "acme", previewRepo, true, "informational")
	previewSHA := "abc9999000000000000000000000000000000022"
	attachmentSeedTranscript(t, ctx, pool, blobs, author, "git@github.com:acme/"+previewRepo+".git", previewSHA, "private", "", time.Now().Add(-time.Hour))
	attachmentCreatePreview(t, ctx, h, previewGroup, author, "acme", previewRepo, previewSHA, 22)
	if rec := pageAs(t, h, readerAuth, "acme", previewRepo, 22); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d for a repository reader on a preview, want 404: a preview is the author's own review step", rec.Code)
	}
	if rec := pageAs(t, h, authorAuth, "acme", previewRepo, 22); rec.Code != http.StatusOK {
		t.Fatalf("status = %d for the author on their own preview, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestRepositoryRefusalsAreRememberedBriefly_RealPostgres proves the refusal
// cache does the one job it has: a stranger probing one pull request page
// repeatedly costs one GitHub question, not one per probe.
func TestRepositoryRefusalsAreRememberedBriefly_RealPostgres(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	author := attachmentInsertOwner(t, ctx, pool, 994061)
	defer cleanupOwners(t, ctx, pool, author)
	_, repoName := attachPrivateRepoTranscript(t, h, pool, blobs, fake, author, 61, "abc9999000000000000000000000000000000061", dbVisibilityPrivate)

	reader := attachmentInsertOwner(t, ctx, pool, 994062)
	defer cleanupOwners(t, ctx, pool, reader)
	readerAuth := &AuthUser{ID: uuid.UUID(reader.Bytes), Username: "repo-reader"}
	fake.setRepoReader("994062", "reader-login", "none")

	if rec := pageAs(t, h, readerAuth, "acme", repoName, 61); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d for a reader GitHub refuses, want 404", rec.Code)
	}
	asked := fake.permissionAskCount()
	if asked == 0 {
		t.Fatal("the fake was never asked for a permission, so the refusal did not come from GitHub")
	}
	if rec := pageAs(t, h, readerAuth, "acme", repoName, 61); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d on the repeat read, want 404", rec.Code)
	}
	if again := fake.permissionAskCount(); again != asked {
		t.Fatalf("GitHub was asked again for a refusal it had just given (%d -> %d): repeated probes of one pull request must not each cost a call", asked, again)
	}
}

// TestRepositoryAccessChecksAreRateLimited_RealPostgres bounds the GitHub question
// per viewer. It costs two calls from a quota shared with every other
// GitHub-backed feature and the URL it hangs on is guessable, so a viewer who
// varies the repository past their burst is refused, and told that is why
// rather than being handed the refusal a repository would have given, while
// another viewer is untouched.
func TestRepositoryAccessChecksAreRateLimited_RealPostgres(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	author := attachmentInsertOwner(t, ctx, pool, 995001)
	defer cleanupOwners(t, ctx, pool, author)
	// Two questions, so the burst is exhaustible here.
	h.repoAccessLimiter = repositoryAccessLimiter{burst: 2, perSecond: 0.001}

	// Three repositories, because the refusal cache answers for a repository it
	// has already asked about: a second read of one repository costs no question.
	var repos []string
	for i := 0; i < 3; i++ {
		_, repoName := attachPrivateRepoTranscript(t, h, pool, blobs, fake, author, 71+i,
			fmt.Sprintf("abc7777000000000000000000000000000000%02d", i), dbVisibilityPrivate)
		repos = append(repos, repoName)
	}

	reader := attachmentInsertOwner(t, ctx, pool, 995002)
	defer cleanupOwners(t, ctx, pool, reader)
	readerAuth := &AuthUser{ID: uuid.UUID(reader.Bytes), Username: "repo-reader"}
	fake.setRepoReader("995002", "reader-login", "read")

	for i := 0; i < 2; i++ {
		if rec := pageAs(t, h, readerAuth, "acme", repos[i], 71+i); rec.Code != http.StatusOK {
			t.Fatalf("page %d: status = %d, want 200 (body: %s)", i+1, rec.Code, rec.Body.String())
		}
	}
	asked := fake.permissionAskCount()
	if asked != 2 {
		t.Fatalf("GitHub was asked %d time(s) for two pages, want 2: a question is what a token is spent on", asked)
	}

	rec := pageAs(t, h, readerAuth, "acme", repos[2], 73)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d over the burst, want 429: a throttled check says so rather than looking like a repository that said no", rec.Code)
	}
	if rec := pageAs(t, h, readerAuth, "acme", repos[2], 73); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d on a repeat over the burst, want 429", rec.Code)
	}
	if again := fake.permissionAskCount(); again != asked {
		t.Fatalf("GitHub was asked %d more time(s) while throttled, want none: a bounded viewer must not cost calls", again-asked)
	}

	other := attachmentInsertOwner(t, ctx, pool, 995003)
	defer cleanupOwners(t, ctx, pool, other)
	otherAuth := &AuthUser{ID: uuid.UUID(other.Bytes), Username: "other-reader"}
	fake.setRepoReader("995003", "other-login", "read")
	if rec := pageAs(t, h, otherAuth, "acme", repos[2], 73); rec.Code != http.StatusOK {
		t.Fatalf("status = %d for a second viewer, want 200: the burst is per viewer", rec.Code)
	}
}
