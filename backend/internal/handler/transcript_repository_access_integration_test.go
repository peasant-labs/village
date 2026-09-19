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
func attachPrivateRepoTranscript(t *testing.T, h *Handler, pool *pgxpool.Pool, blobs *recordingTranscriptBlobStore, fake *attachmentGitHubFake, author pgtype.UUID, number int, sha string) (pgtype.UUID, string, pgtype.UUID) {
	t.Helper()
	ctx := context.Background()
	repoName := "readers-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, author, "acme", repoName, true, "informational")
	transcriptID := attachmentSeedTranscript(t, ctx, pool, blobs, author,
		"git@github.com:acme/"+repoName+".git", sha, "private", "", time.Now().Add(-time.Hour))

	attachmentCreatePreview(t, ctx, h, groupID, author, "acme", repoName, sha, number)
	fake.setPullCommits(sha)
	attachmentConfirm(t, h, author, "acme", repoName, number)
	return transcriptID, repoName, groupID
}

// TestRepositoryReadersOpenAttachedPrompts_RealPostgres is the private path end
// to end: a reader outside the collective, whom every collected check refuses, is
// admitted to an attached transcript when GitHub says they may read the
// repository it is attached to.
//
// Nothing about the admission is recorded, so the same reader is refused again
// the moment GitHub's answer changes — which is what makes the grant live rather
// than a row that would need revoking. A refusal alone is remembered, briefly, so
// that repeated probes do not each cost a GitHub call; an admission is asked
// every time, so an owner's withdrawal is not held behind a cache.
func TestRepositoryReadersOpenAttachedPrompts_RealPostgres(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	author := attachmentInsertOwner(t, ctx, pool, 994001)
	defer cleanupOwners(t, ctx, pool, author)
	transcriptID, _, _ := attachPrivateRepoTranscript(t, h, pool, blobs, fake, author, 7, "abc9999000000000000000000000000000000001")

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

	// A GitHub failure denies rather than admits. This runs before any refusal is
	// remembered, so the answer comes from the failed question itself.
	fake.failRepoReads(true)
	if rec := transcriptViewAs(t, h, readerAuth, transcriptID); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d while GitHub was failing, want 404: an access question that cannot be answered must deny", rec.Code)
	}
	fake.failRepoReads(false)

	// GitHub refusing is the collective-only refusal, unchanged, and it takes
	// effect with nothing else happening: an admission is never remembered.
	fake.setRepoReader("994002", "reader-login", "none")
	if rec := transcriptViewAs(t, h, readerAuth, transcriptID); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d for a reader GitHub refuses, want 404: a reader with no repository access is refused exactly as a stranger is", rec.Code)
	}

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
	transcriptID, repoName, _ := attachPrivateRepoTranscript(t, h, pool, blobs, fake, author, 11, "abc9999000000000000000000000000000000011")

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

// TestRepositoryReadersOpenThePullRequestPage_RealPostgres is the page half of
// the private path: a repository's own readers can open the pull request that
// lists the prompts, which is where the app's comment and check send them, so
// being admitted to the transcripts is reachable rather than needing the link
// already. A preview stays the author's own review step.
func TestRepositoryReadersOpenThePullRequestPage_RealPostgres(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	author := attachmentInsertOwner(t, ctx, pool, 994021)
	defer cleanupOwners(t, ctx, pool, author)
	authorAuth := &AuthUser{ID: uuid.UUID(author.Bytes), Username: "attachment-author"}
	_, repoName, _ := attachPrivateRepoTranscript(t, h, pool, blobs, fake, author, 21, "abc9999000000000000000000000000000000021")

	reader := attachmentInsertOwner(t, ctx, pool, 994022)
	defer cleanupOwners(t, ctx, pool, reader)
	readerAuth := &AuthUser{ID: uuid.UUID(reader.Bytes), Username: "repo-reader"}

	// A reader GitHub admits opens the page, digest included: the prompts are what
	// they were admitted to, so listing them is the same grant.
	fake.setRepoReader("994022", "reader-login", "read")
	rec := pageAs(t, h, readerAuth, "acme", repoName, 21)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d for a repository reader, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var response schema.VillagePullRequestAttachmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode the page a repository reader opened: %v", err)
	}
	if response.Digest == nil {
		t.Fatal("the page a repository reader opened carries no digest, so the prompts they were admitted to are not what they can see")
	}

	// GitHub refusing is the collective-only refusal, unchanged.
	fake.setRepoReader("994022", "reader-login", "none")
	if rec := pageAs(t, h, readerAuth, "acme", repoName, 21); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d for a reader GitHub refuses, want 404", rec.Code)
	}

	// A preview is the author's own review step. Repository access does not open a
	// digest the author has not confirmed, and the author still sees their own.
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

// transcriptAnnotationsAs drives the annotation routes as the given viewer.
func transcriptAnnotationsAs(t *testing.T, h *Handler, user *AuthUser, transcriptID pgtype.UUID, method string) *httptest.ResponseRecorder {
	t.Helper()
	id := uuid.UUID(transcriptID.Bytes).String()
	var body *strings.Reader
	if method == http.MethodPost {
		body = strings.NewReader(`{"turn_index":0,"turns":[]}`)
	} else {
		body = strings.NewReader("")
	}
	r := httptest.NewRequest(method, "/api/v1/transcripts/"+id+"/annotations", body)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, UserContextKey, user)
	w := httptest.NewRecorder()
	if method == http.MethodPost {
		h.CreateTranscriptAnnotation(w, r.WithContext(ctx))
	} else {
		h.ListTranscriptAnnotations(w, r.WithContext(ctx))
	}
	return w
}

// TestRepositoryReadEndsWhenTheOwnerNarrows_RealPostgres pins the grant to the
// share the attach opened. The binding outlives an owner narrowing the
// transcript — it has to, so detach can restore what the attach recorded — so the
// narrowing must end the repository readers' access by itself.
func TestRepositoryReadEndsWhenTheOwnerNarrows_RealPostgres(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	author := attachmentInsertOwner(t, ctx, pool, 994031)
	defer cleanupOwners(t, ctx, pool, author)
	authorAuth := &AuthUser{ID: uuid.UUID(author.Bytes), Username: "attachment-author"}
	transcriptID, _, _ := attachPrivateRepoTranscript(t, h, pool, blobs, fake, author, 31, "abc9999000000000000000000000000000000031")

	reader := attachmentInsertOwner(t, ctx, pool, 994032)
	defer cleanupOwners(t, ctx, pool, reader)
	readerAuth := &AuthUser{ID: uuid.UUID(reader.Bytes), Username: "repo-reader"}
	fake.setRepoReader("994032", "reader-login", "read")

	if rec := transcriptViewAs(t, h, readerAuth, transcriptID); rec.Code != http.StatusOK {
		t.Fatalf("status = %d before the narrowing, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	if rec := transcriptVisibilityPatch(t, h, authorAuth, transcriptID, "private"); rec.Code != http.StatusOK {
		t.Fatalf("narrow status = %d (%s), want 200", rec.Code, rec.Body.String())
	}

	if rec := transcriptViewAs(t, h, readerAuth, transcriptID); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d after the owner narrowed the transcript to private, want 404: the grant is the share the attach opened, and the owner has withdrawn it", rec.Code)
	}
}

// TestRepositoryReadEndsWhenTheOwnerUnshares_RealPostgres is the other half: the
// share is retracted without the transcript's tier changing, which the binding
// and the visibility both survive, and the access must end anyway.
func TestRepositoryReadEndsWhenTheOwnerUnshares_RealPostgres(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	author := attachmentInsertOwner(t, ctx, pool, 994041)
	defer cleanupOwners(t, ctx, pool, author)
	authorAuth := &AuthUser{ID: uuid.UUID(author.Bytes), Username: "attachment-author"}
	transcriptID, repoName, groupID := attachPrivateRepoTranscript(t, h, pool, blobs, fake, author, 41, "abc9999000000000000000000000000000000041")
	_ = repoName

	reader := attachmentInsertOwner(t, ctx, pool, 994042)
	defer cleanupOwners(t, ctx, pool, reader)
	readerAuth := &AuthUser{ID: uuid.UUID(reader.Bytes), Username: "repo-reader"}
	fake.setRepoReader("994042", "reader-login", "read")

	if rec := transcriptViewAs(t, h, readerAuth, transcriptID); rec.Code != http.StatusOK {
		t.Fatalf("status = %d before the unshare, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// The owner retracts the collective's share through the mounted route.
	id := uuid.UUID(transcriptID.Bytes).String()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/transcripts/"+id+"/share/"+uuid.UUID(groupID.Bytes).String(), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	rctx.URLParams.Add("groupID", uuid.UUID(groupID.Bytes).String())
	ctx2 := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
	ctx2 = context.WithValue(ctx2, UserContextKey, authorAuth)
	w := httptest.NewRecorder()
	h.UnshareTranscript(w, r.WithContext(ctx2))
	if w.Code != http.StatusOK {
		t.Fatalf("unshare status = %d (%s), want 200", w.Code, w.Body.String())
	}

	if rec := transcriptViewAs(t, h, readerAuth, transcriptID); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d after the owner retracted the share, want 404: a repository reader is admitted by the attachment's share and no further", rec.Code)
	}
}

// TestRepositoryReadsDoNotWrite_RealPostgres keeps the grant to reads. It rides
// the same predicate the annotation write uses, and a non-member repository
// reader must not be handed a write on somebody else's transcript.
func TestRepositoryReadsDoNotWrite_RealPostgres(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	author := attachmentInsertOwner(t, ctx, pool, 994051)
	defer cleanupOwners(t, ctx, pool, author)
	transcriptID, _, _ := attachPrivateRepoTranscript(t, h, pool, blobs, fake, author, 51, "abc9999000000000000000000000000000000051")

	reader := attachmentInsertOwner(t, ctx, pool, 994052)
	defer cleanupOwners(t, ctx, pool, reader)
	readerAuth := &AuthUser{ID: uuid.UUID(reader.Bytes), Username: "repo-reader"}
	fake.setRepoReader("994052", "reader-login", "read")

	if rec := transcriptViewAs(t, h, readerAuth, transcriptID); rec.Code != http.StatusOK {
		t.Fatalf("status = %d for the transcript itself, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	// Reading what has been written about the prompts is a read.
	if rec := transcriptAnnotationsAs(t, h, readerAuth, transcriptID, http.MethodGet); rec.Code != http.StatusOK {
		t.Fatalf("status = %d reading annotations as a repository reader, want 200", rec.Code)
	}
	// Writing one is not: the grant opens the prompts, it does not give a
	// non-member a label on another person's work.
	if rec := transcriptAnnotationsAs(t, h, readerAuth, transcriptID, http.MethodPost); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d writing an annotation as a repository reader, want 404: repository access is a grant to read", rec.Code)
	}
}

// TestRepositoryRefusalsAreRememberedBriefly_RealPostgres proves the refusal
// cache does the one job it has: a stranger probing one pull request repeatedly
// costs one GitHub question, not one per probe.
func TestRepositoryRefusalsAreRememberedBriefly_RealPostgres(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	author := attachmentInsertOwner(t, ctx, pool, 994061)
	defer cleanupOwners(t, ctx, pool, author)
	transcriptID, _, _ := attachPrivateRepoTranscript(t, h, pool, blobs, fake, author, 61, "abc9999000000000000000000000000000000061")

	reader := attachmentInsertOwner(t, ctx, pool, 994062)
	defer cleanupOwners(t, ctx, pool, reader)
	readerAuth := &AuthUser{ID: uuid.UUID(reader.Bytes), Username: "repo-reader"}
	fake.setRepoReader("994062", "reader-login", "none")

	if rec := transcriptViewAs(t, h, readerAuth, transcriptID); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d for a reader GitHub refuses, want 404", rec.Code)
	}
	asked := fake.permissionAskCount()
	if asked == 0 {
		t.Fatal("the fake was never asked for a permission, so the refusal did not come from GitHub")
	}
	if rec := transcriptViewAs(t, h, readerAuth, transcriptID); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d on the repeat read, want 404", rec.Code)
	}
	if again := fake.permissionAskCount(); again != asked {
		t.Fatalf("GitHub was asked again for a refusal it had just given (%d -> %d): repeated probes of one pull request must not each cost a call", asked, again)
	}
}

// TestRepositoryAccessChecksAreRateLimited_RealPostgres bounds the GitHub question
// per viewer. It costs two calls from a quota shared with every other
// GitHub-backed feature and the URL it hangs on is guessable, so a viewer who
// varies the repository past their burst is refused — and told that is why,
// rather than being handed the refusal a repository would have given — while
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
	var transcripts []pgtype.UUID
	for i := 0; i < 3; i++ {
		transcriptID, _, _ := attachPrivateRepoTranscript(t, h, pool, blobs, fake, author, 71+i,
			fmt.Sprintf("abc7777000000000000000000000000000000%02d", i))
		transcripts = append(transcripts, transcriptID)
	}

	reader := attachmentInsertOwner(t, ctx, pool, 995002)
	defer cleanupOwners(t, ctx, pool, reader)
	readerAuth := &AuthUser{ID: uuid.UUID(reader.Bytes), Username: "repo-reader"}
	fake.setRepoReader("995002", "reader-login", "read")

	// The first two reads spend the burst, each asking GitHub and being admitted.
	for i := 0; i < 2; i++ {
		if rec := transcriptViewAs(t, h, readerAuth, transcripts[i]); rec.Code != http.StatusOK {
			t.Fatalf("read %d: status = %d, want 200 (body: %s)", i+1, rec.Code, rec.Body.String())
		}
	}
	asked := fake.permissionAskCount()
	if asked != 2 {
		t.Fatalf("GitHub was asked %d time(s) for two reads, want 2: a question is what a token is spent on", asked)
	}

	// The third is over the burst: refused, told why, and it costs nothing.
	rec := transcriptViewAs(t, h, readerAuth, transcripts[2])
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d over the burst, want 429: a throttled check says so rather than looking like a repository that said no", rec.Code)
	}
	if rec := transcriptViewAs(t, h, readerAuth, transcripts[2]); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d on a repeat over the burst, want 429", rec.Code)
	}
	if again := fake.permissionAskCount(); again != asked {
		t.Fatalf("GitHub was asked %d more time(s) while throttled, want none: a bounded viewer must not cost calls", again-asked)
	}

	// One viewer's burst is not another's.
	other := attachmentInsertOwner(t, ctx, pool, 995003)
	defer cleanupOwners(t, ctx, pool, other)
	otherAuth := &AuthUser{ID: uuid.UUID(other.Bytes), Username: "other-reader"}
	fake.setRepoReader("995003", "other-login", "read")
	if rec := transcriptViewAs(t, h, otherAuth, transcripts[2]); rec.Code != http.StatusOK {
		t.Fatalf("status = %d for a second viewer, want 200: the burst is per viewer", rec.Code)
	}
}
