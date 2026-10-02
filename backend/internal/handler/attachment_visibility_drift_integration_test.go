//go:build integration

package handler

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/promptattach"
)

// The pull request's comment and check list a bound transcript's prompts only
// while anyone can read the transcript, and the page shows them only to a
// viewer who can. Both follow the transcript's own audience as its owner moves
// it, without a second click. These tests drive the owner's own routes and
// read what the pull request says afterwards.

// attachmentDigestOf reads the digest an attachment stores.
func attachmentDigestOf(t *testing.T, ctx context.Context, h *Handler, attachmentID pgtype.UUID) string {
	t.Helper()
	row, err := h.queries.GetPullRequestAttachment(ctx, attachmentID)
	if err != nil {
		t.Fatalf("read attachment: %v", err)
	}
	return string(row.Digest)
}

const (
	attachedPrompt = "please attach my prompts"
	unlistedNote   = "1 attached transcript is not listed here."
)

// postedState is what the fake GitHub last received.
type postedState struct {
	comment, check, conclusion string
	edits                      int
}

func postedNow(fake *attachmentGitHubFake) postedState {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return postedState{comment: fake.lastCommentBody, check: fake.lastCheckText, conclusion: fake.lastCheckConclusion, edits: fake.commentEdits}
}

// assertListed pins whether the comment and the check carry the prompt, and the
// note that says otherwise.
func (p postedState) assertListed(t *testing.T, listed bool, conclusion string) {
	t.Helper()
	for surface, text := range map[string]string{"comment": p.comment, "check": p.check} {
		if got := strings.Contains(text, attachedPrompt); got != listed {
			t.Errorf("the %s carries the prompt = %t, want %t: %s", surface, got, listed, text)
		}
		if got := strings.Contains(text, unlistedNote); got == listed {
			t.Errorf("the %s says a transcript is not listed = %t, want %t: %s", surface, got, !listed, text)
		}
		if strings.Contains(text, "author") {
			t.Errorf("the %s attributes the change to someone: %s", surface, text)
		}
	}
	if p.conclusion != conclusion {
		t.Errorf("conclusion = %q, want %q", p.conclusion, conclusion)
	}
}

// TestNarrowingAListedTranscriptStopsListingIt_RealPostgres proves the pull
// request stops listing prompts the moment the owner makes a public transcript
// private: the comment is edited rather than left as it was, and says a
// transcript is not listed without saying which or why. The binding and the
// visibility it recorded survive, and detaching leaves the owner's narrowing.
func TestNarrowingAListedTranscriptStopsListingIt_RealPostgres(t *testing.T) {
	t.Parallel()
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 992004)
	defer cleanupOwners(t, ctx, pool, owner)
	ownerAuth := &AuthUser{ID: uuid.UUID(owner.Bytes), Username: "attachment"}

	repoName := "widgets-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, false, "informational")
	sha := "abc1234000000000000000000000000000000004"
	transcriptID := attachmentSeedTranscript(t, ctx, pool, blobs, owner,
		"git@github.com:acme/"+repoName+".git", sha, "public", "feat/x", time.Now().Add(-time.Hour))
	attachment := attachmentCreatePreview(t, ctx, h, groupID, owner, "acme", repoName, sha, 7)
	fake.setPullCommits(sha)
	attachmentConfirm(t, h, owner, "acme", repoName, 7)
	postedNow(fake).assertListed(t, true, "success")

	before := postedNow(fake)
	if rec := transcriptVisibilityPatch(t, h, ownerAuth, transcriptID, "private"); rec.Code != http.StatusOK {
		t.Fatalf("narrow status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	after := postedNow(fake)
	if after.edits <= before.edits {
		t.Errorf("the pull request must be edited, not left listing the prompt; edits=%d", after.edits)
	}
	after.assertListed(t, false, "neutral")

	binding, err := h.queries.GetPullRequestAttachmentTranscript(ctx, sqlc.GetPullRequestAttachmentTranscriptParams{AttachmentID: attachment.ID, TranscriptID: transcriptID})
	if err != nil {
		t.Fatalf("the binding must survive a visibility change: %v", err)
	}
	if binding.PreviousVisibility != "public" {
		t.Errorf("previous_visibility = %q, want the value recorded at attach", binding.PreviousVisibility)
	}

	if rec := attachmentServe(t, attachmentRouter(h), http.MethodDelete, "/api/v1/pulls/acme/"+repoName+"/7", owner); rec.Code != http.StatusOK {
		t.Fatalf("detach status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if visibility := readTranscriptVisibility(t, ctx, pool, transcriptID); visibility != "private" {
		t.Errorf("visibility = %q after detach, want private: detaching must not re-publish what the owner narrowed", visibility)
	}
}

// TestWideningAnAttachedTranscriptListsIt_RealPostgres proves the other
// direction: a private transcript attached to a public repository is not listed,
// is listed once its owner makes it public, and stops again when they narrow it.
func TestWideningAnAttachedTranscriptListsIt_RealPostgres(t *testing.T) {
	t.Parallel()
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 992005)
	defer cleanupOwners(t, ctx, pool, owner)
	ownerAuth := &AuthUser{ID: uuid.UUID(owner.Bytes), Username: "attachment"}

	repoName := "widgets-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, false, "informational")
	sha := "abc1234000000000000000000000000000000005"
	transcriptID := attachmentSeedTranscript(t, ctx, pool, blobs, owner,
		"git@github.com:acme/"+repoName+".git", sha, "private", "feat/x", time.Now().Add(-time.Hour))
	// A number of its own: the attachment is keyed by the GitHub repository id
	// and the number, and the test helper shares one repository id.
	attachmentCreatePreview(t, ctx, h, groupID, owner, "acme", repoName, sha, 8)
	fake.setPullCommits(sha)
	attachmentConfirm(t, h, owner, "acme", repoName, 8)
	postedNow(fake).assertListed(t, false, "neutral")

	if rec := transcriptVisibilityPatch(t, h, ownerAuth, transcriptID, "public"); rec.Code != http.StatusOK {
		t.Fatalf("widen status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	postedNow(fake).assertListed(t, true, "success")

	if rec := transcriptVisibilityPatch(t, h, ownerAuth, transcriptID, "private"); rec.Code != http.StatusOK {
		t.Fatalf("narrow status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	postedNow(fake).assertListed(t, false, "neutral")
}

// republishedAttachment publishes a session, makes it public, attaches it to a
// pull request in a public repository, and returns what a republish test needs
// to republish the same session and read the attachment back.
type republishedAttachment struct {
	h            *Handler
	pool         *pgxpool.Pool
	fake         *attachmentGitHubFake
	owner        pgtype.UUID
	remote       string
	sha          string
	sessionID    string
	transcriptID pgtype.UUID
	attachment   sqlc.PullRequestAttachment
}

func newRepublishedAttachment(t *testing.T, githubID int64, number int, sha string) *republishedAttachment {
	t.Helper()
	h, pool, _, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, githubID)
	t.Cleanup(func() { cleanupOwners(t, context.Background(), pool, owner) })

	repoName := "widgets-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, false, "informational")
	a := &republishedAttachment{h: h, pool: pool, fake: fake, owner: owner, remote: "git@github.com:acme/" + repoName + ".git", sha: sha, sessionID: uuid.NewString()}

	if code, body := attachmentPublishSession(t, h, owner, "attachment-owner", a.remote, sha, a.sessionID, attachmentPublicationContent()); code != http.StatusCreated {
		t.Fatalf("first publish status = %d (%s), want 201", code, body)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM transcripts WHERE owner_id = $1 AND local_id = $2`, owner, a.sessionID).Scan(&a.transcriptID); err != nil {
		t.Fatalf("read the published transcript: %v", err)
	}
	if rec := transcriptVisibilityPatch(t, h, &AuthUser{ID: uuid.UUID(owner.Bytes), Username: "attachment-owner"}, a.transcriptID, "public"); rec.Code != http.StatusOK {
		t.Fatalf("the owner's PATCH to public: status = %d (%s), want 200", rec.Code, rec.Body.String())
	}

	a.attachment = attachmentCreatePreview(t, ctx, h, groupID, owner, "acme", repoName, sha, number)
	fake.setPullCommits(sha)
	attachmentConfirm(t, h, owner, "acme", repoName, number)
	postedNow(fake).assertListed(t, true, "success")
	return a
}

func (a *republishedAttachment) key() string { return uuidFromPg(a.transcriptID).String() }

// republish sends the same session again with a revised prompt, so the stored
// content is replaced and the transcript is narrowed before it is.
func (a *republishedAttachment) republish(t *testing.T) (int, string) {
	t.Helper()
	revised := bytes.Replace(attachmentPublicationContent(),
		[]byte(attachedPrompt), []byte(attachedPrompt+", revised"), 1)
	return attachmentPublishSession(t, a.h, a.owner, "attachment-owner", a.remote, a.sha, a.sessionID, revised)
}

func (a *republishedAttachment) visibility(t *testing.T) string {
	t.Helper()
	return readTranscriptVisibility(t, context.Background(), a.pool, a.transcriptID)
}

func (a *republishedAttachment) assertBindingSurvives(t *testing.T) {
	t.Helper()
	binding, err := a.h.queries.GetPullRequestAttachmentTranscript(context.Background(), sqlc.GetPullRequestAttachmentTranscriptParams{
		AttachmentID: a.attachment.ID, TranscriptID: a.transcriptID,
	})
	if err != nil {
		t.Fatalf("the binding must survive the republish: %v", err)
	}
	if binding.PreviousVisibility != "public" {
		t.Errorf("previous_visibility = %q, want the value recorded at attach", binding.PreviousVisibility)
	}
}

// TestRepublishKeepsTheListedRow_RealPostgres covers the ordinary republish of
// a listed transcript. The republish narrows the transcript before replacing its
// content and restores it in the same transaction as the receipt, so the pull
// request keeps listing it, and the refresh that follows the narrowing reposts
// the revised prompt rather than withdrawing the row.
func TestRepublishKeepsTheListedRow_RealPostgres(t *testing.T) {
	t.Parallel()
	a := newRepublishedAttachment(t, 992061, 9, "abc1234000000000000000000000000000000006")

	code, body := a.republish(t)
	if code != http.StatusOK {
		t.Fatalf("republish status = %d (%s), want 200", code, body)
	}
	if visibility := a.visibility(t); visibility != "public" {
		t.Errorf("visibility = %q after the republish, want public: the republish must put back the visibility it found", visibility)
	}
	if digest := attachmentDigestOf(t, context.Background(), a.h, a.attachment.ID); !strings.Contains(digest, attachedPrompt+", revised") {
		t.Errorf("the stored digest must carry the republished prompt; digest=%s", digest)
	}
	posted := postedNow(a.fake)
	posted.assertListed(t, true, "success")
	if !strings.Contains(posted.comment, attachedPrompt+", revised") {
		t.Errorf("the comment must list the republished prompt; body=%s", posted.comment)
	}
	a.assertBindingSurvives(t)
}

// TestUnconfirmedRepublishStopsListingTheTranscript_RealPostgres covers the one
// outcome that leaves the narrowing in place: PostgreSQL does not confirm the
// replacement's commit, so nothing can tell whether the restore committed with
// it and the transcript stays private. That narrowing is invisible to the
// publish hook (nothing new is accepted and the head has not moved), so the pull
// request must still stop listing the transcript, without a second click.
func TestUnconfirmedRepublishStopsListingTheTranscript_RealPostgres(t *testing.T) {
	t.Parallel()
	a := newRepublishedAttachment(t, 992062, 12, "abc1234000000000000000000000000000000062")
	installRepublishCommitRefusal(t, context.Background(), a.pool, a.transcriptID)

	code, body := a.republish(t)
	if code != http.StatusInternalServerError {
		t.Fatalf("republish status = %d (%s), want 500: the commit was refused", code, body)
	}
	if visibility := a.visibility(t); visibility != "private" {
		t.Errorf("visibility = %q after an unconfirmed republish, want private: the fail-safe answer", visibility)
	}
	postedNow(a.fake).assertListed(t, false, "neutral")
	a.assertBindingSurvives(t)
}

// sharedAttachedWorld is a transcript on a private repository's pull request,
// shared with the linking collective and, when asked, with a second one, and a
// member of the linking collective only.
type sharedAttachedWorld struct {
	h            *Handler
	pool         *pgxpool.Pool
	fake         *attachmentGitHubFake
	owner        *AuthUser
	member       *AuthUser
	repoName     string
	groupID      pgtype.UUID
	otherGroupID pgtype.UUID
	transcriptID pgtype.UUID
}

func newSharedAttachedWorld(t *testing.T, githubBase int64, alsoOther bool, mode string) *sharedAttachedWorld {
	t.Helper()
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, githubBase)
	member := attachmentInsertOwner(t, ctx, pool, githubBase+1)
	t.Cleanup(func() { cleanupOwners(t, context.Background(), pool, owner, member) })
	w := &sharedAttachedWorld{
		h: h, pool: pool, fake: fake,
		owner:    &AuthUser{ID: uuid.UUID(owner.Bytes), Username: "shared-owner"},
		member:   &AuthUser{ID: uuid.UUID(member.Bytes), Username: "shared-member"},
		repoName: "shared-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8],
	}
	w.groupID = attachmentLinkCollective(t, ctx, pool, owner, "acme", w.repoName, true, mode)
	if _, err := pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, 'member')`, w.groupID, member); err != nil {
		t.Fatalf("add the member: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO groups (name, created_by) VALUES ($1, $2) RETURNING id`, "shared-other-"+uuid.NewString(), owner).Scan(&w.otherGroupID); err != nil {
		t.Fatalf("insert the second collective: %v", err)
	}
	sha := fmt.Sprintf("c%039d", githubBase)
	w.transcriptID = attachmentSeedTranscript(t, ctx, pool, blobs, owner, "git@github.com:acme/"+w.repoName+".git", sha, "shared", "", time.Now().Add(-time.Hour))
	groups := []pgtype.UUID{w.groupID}
	if alsoOther {
		groups = append(groups, w.otherGroupID)
	}
	for _, groupID := range groups {
		if _, err := pool.Exec(ctx, `
			INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status)
			VALUES ($1, $2, 1, 'approved')`, w.transcriptID, groupID); err != nil {
			t.Fatalf("share the transcript: %v", err)
		}
	}
	attachmentCreatePreview(t, ctx, h, w.groupID, owner, "acme", w.repoName, sha, 7)
	fake.setPullCommits(sha)
	attachmentConfirm(t, h, owner, "acme", w.repoName, 7)
	return w
}

func (w *sharedAttachedWorld) memberSeesPrompts(t *testing.T) bool {
	t.Helper()
	rec := pageAs(t, w.h, w.member, "acme", w.repoName, 7)
	if rec.Code != http.StatusOK {
		t.Fatalf("the member's page status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	return strings.Contains(rec.Body.String(), attachedPrompt)
}

// TestUnsharingTheLinkingCollectiveEndsItsMembersPrompts_RealPostgres is the
// stale-listing case the old rule had: the owner withdraws the linking
// collective while a second collective keeps the transcript shared. Its
// visibility does not move, yet the linking collective's members can no longer
// open it, so the pull request page must stop showing them its prompts at once,
// and the comment, which never listed a shared transcript, has nothing to drop.
func TestUnsharingTheLinkingCollectiveEndsItsMembersPrompts_RealPostgres(t *testing.T) {
	w := newSharedAttachedWorld(t, 992071, true, "informational")
	ctx := context.Background()
	if !w.memberSeesPrompts(t) {
		t.Fatal("the linking collective's member must see the prompts before the unshare, or the test proves nothing")
	}
	postedNow(w.fake).assertListed(t, false, "success")

	id := uuid.UUID(w.transcriptID.Bytes).String()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/transcripts/"+id+"/share/"+uuid.UUID(w.groupID.Bytes).String(), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	rctx.URLParams.Add("groupID", uuid.UUID(w.groupID.Bytes).String())
	r = r.WithContext(context.WithValue(context.WithValue(r.Context(), chi.RouteCtxKey, rctx), UserContextKey, w.owner))
	rec := httptest.NewRecorder()
	w.h.UnshareTranscript(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("unshare status = %d (%s), want 200", rec.Code, rec.Body.String())
	}

	if visibility := readTranscriptVisibility(t, ctx, w.pool, w.transcriptID); visibility != "shared" {
		t.Fatalf("visibility = %q, want shared: the second collective still holds a live share", visibility)
	}
	if code := transcriptViewAs(t, w.h, w.member, w.transcriptID).Code; code != http.StatusNotFound {
		t.Fatalf("the linking collective's member reads the transcript: status = %d, want 404", code)
	}
	if w.memberSeesPrompts(t) {
		t.Error("the pull request page still shows the prompts to a member who can no longer open the transcript")
	}
}

// TestResharingRestoresTheMembersPrompts_RealPostgres is the owner's way back
// from a narrowing that left the collective's share live, such as a republish
// whose commit could not be confirmed: sharing the transcript to that collective
// again flips it back to shared, and the collective's members see its prompts on
// the page again, while the check says so without waiting for a later refresh.
func TestResharingRestoresTheMembersPrompts_RealPostgres(t *testing.T) {
	w := newSharedAttachedWorld(t, 992063, false, "informational")
	key := uuidFromPg(w.transcriptID).String()
	if !w.memberSeesPrompts(t) {
		t.Fatal("the member must see the prompts before the narrowing, or the test proves nothing")
	}

	if rec := transcriptVisibilityPatch(t, w.h, w.owner, w.transcriptID, "private"); rec.Code != http.StatusOK {
		t.Fatalf("narrow status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if w.memberSeesPrompts(t) {
		t.Fatal("the page still shows a private transcript's prompts to a member")
	}
	postedNow(w.fake).assertListed(t, false, "neutral")

	body := []byte(`{"group_ids":["` + uuid.UUID(w.groupID.Bytes).String() + `"]}`)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/"+key+"/share", bytes.NewReader(body))
	r = withChiURLParam(r, "id", key)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, w.owner))
	rec := httptest.NewRecorder()
	w.h.ShareTranscript(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("reshare status = %d (%s), want 200: the collective's share is still live, so sharing again is how the owner restores its access", rec.Code, rec.Body.String())
	}
	if visibility := readTranscriptVisibility(t, context.Background(), w.pool, w.transcriptID); visibility != "shared" {
		t.Fatalf("visibility = %q after the reshare, want shared", visibility)
	}
	if !w.memberSeesPrompts(t) {
		t.Error("the member must see the prompts again once the reshare restored their access")
	}
	postedNow(w.fake).assertListed(t, false, "success")
}

// TestCheckFollowsWhetherAnyReviewerCanRead_RealPostgres is the check's rule
// with a required mode, where its conclusion gates merging. It passes while
// anyone besides the author can read an attached transcript, stays passing when
// one of two is narrowed, and turns neutral once only the author can read what is
// attached. The comment and check say how many attached transcripts they do not
// list, never which or why, and never render a header with no rows under it.
func TestCheckFollowsWhetherAnyReviewerCanRead_RealPostgres(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	author := attachmentInsertOwner(t, ctx, pool, 995011)
	t.Cleanup(func() { cleanupOwners(t, ctx, pool, author) })
	reader := attachmentInsertOwner(t, ctx, pool, 995012)
	t.Cleanup(func() { cleanupOwners(t, ctx, pool, reader) })
	authorAuth := &AuthUser{ID: uuid.UUID(author.Bytes), Username: "attachment-author"}

	repoName := "all-unlisted-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, author, "acme", repoName, true, "required")
	sha := "aaa5555000000000000000000000000000000001"
	first := attachmentSeedTranscript(t, ctx, pool, blobs, author,
		"git@github.com:acme/"+repoName+".git", sha, "shared", "", time.Now().Add(-2*time.Hour))
	second := attachmentSeedTranscript(t, ctx, pool, blobs, author,
		"git@github.com:acme/"+repoName+".git", sha, "shared", "", time.Now().Add(-time.Hour))
	// A shared label by itself grants nothing. Give a different accepted member
	// an approved share to each transcript, then prove the canonical read sees it.
	if _, err := pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, 'member')`, groupID, reader); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status) VALUES ($1, $3, 1, 'approved'), ($2, $3, 1, 'approved')`, first, second, groupID); err != nil {
		t.Fatal(err)
	}
	readerAuth := &AuthUser{ID: uuid.UUID(reader.Bytes)}
	for _, id := range []pgtype.UUID{first, second} {
		row, err := h.queries.GetTranscriptByID(ctx, id)
		if err != nil || !h.canViewTranscript(ctx, readerAuth, row) {
			t.Fatalf("the non-author accepted member cannot read a seeded shared transcript: %v", err)
		}
	}
	attachmentCreatePreview(t, ctx, h, groupID, author, "acme", repoName, sha, 7)
	fake.setPullCommits(sha)
	attachmentConfirm(t, h, author, "acme", repoName, 7)

	posted := postedNow(fake)
	if posted.conclusion != "success" {
		t.Fatalf("conclusion = %q with shared prompts attached, want success", posted.conclusion)
	}
	if !strings.Contains(posted.check, "2 attached transcripts are not listed here.") {
		t.Errorf("the check must count the attached transcripts it does not list; summary=%s", posted.check)
	}

	if rec := transcriptVisibilityPatch(t, h, authorAuth, first, "private"); rec.Code != http.StatusOK {
		t.Fatalf("narrow status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if conclusion := postedNow(fake).conclusion; conclusion != "success" {
		t.Fatalf("conclusion = %q with one shared transcript left, want success: its collective can still review it", conclusion)
	}

	if rec := transcriptVisibilityPatch(t, h, authorAuth, second, "private"); rec.Code != http.StatusOK {
		t.Fatalf("narrow status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	posted = postedNow(fake)
	if posted.conclusion != "neutral" {
		t.Fatalf("conclusion = %q with only the author able to read what is attached, want neutral: a required check reporting success tells a reviewer the opposite of what they can read", posted.conclusion)
	}
	for surface, text := range map[string]string{"comment": posted.comment, "check": posted.check} {
		if !strings.Contains(text, "2 attached transcripts are not listed here.") {
			t.Errorf("the %s must count the attached transcripts it does not list; %s", surface, text)
		}
		if strings.Contains(text, "0 sessions") {
			t.Errorf("the %s rendered a header with no rows under it, which reads as a rendering fault rather than a state; %s", surface, text)
		}
	}
}

// TestAnAttachReconcilesANarrowingDuringItsPost_RealPostgres is the race an
// attach would otherwise lose: the owner narrows a public transcript while the
// attach is posting the comment that lists it. The owner's repost finds no
// attached attachment yet, so the attach itself must notice once it is attached
// and repost without the prompt.
func TestAnAttachReconcilesANarrowingDuringItsPost_RealPostgres(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 992081)
	defer cleanupOwners(t, ctx, pool, owner)
	ownerAuth := &AuthUser{ID: uuid.UUID(owner.Bytes), Username: "attachment"}

	repoName := "race-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, false, "informational")
	sha := "abc1234000000000000000000000000000000081"
	transcriptID := attachmentSeedTranscript(t, ctx, pool, blobs, owner,
		"git@github.com:acme/"+repoName+".git", sha, "public", "feat/x", time.Now().Add(-time.Hour))
	attachmentCreatePreview(t, ctx, h, groupID, owner, "acme", repoName, sha, 7)
	fake.setPullCommits(sha)

	narrowed := false
	fake.mu.Lock()
	fake.beforeCommentWrite = func() {
		if rec := transcriptVisibilityPatch(t, h, ownerAuth, transcriptID, "private"); rec.Code == http.StatusOK {
			narrowed = true
		}
	}
	fake.mu.Unlock()
	attachmentConfirm(t, h, owner, "acme", repoName, 7)
	if !narrowed {
		t.Fatal("the owner's narrowing did not land during the post, so the race this test describes never happened")
	}
	postedNow(fake).assertListed(t, false, "neutral")
}

// TestReattachingBindsAfresh_RealPostgres covers a second attach cycle. Detach
// keeps its bindings so the detached pull request still lists them; the next
// attach clears them and binds again, so the binding records the visibility the
// transcript holds at that second attach, not the first.
func TestReattachingBindsAfresh_RealPostgres(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 992091)
	defer cleanupOwners(t, ctx, pool, owner)
	ownerAuth := &AuthUser{ID: uuid.UUID(owner.Bytes), Username: "attachment"}

	repoName := "reattach-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, false, "informational")
	sha := "abc1234000000000000000000000000000000091"
	transcriptID := attachmentSeedTranscript(t, ctx, pool, blobs, owner,
		"git@github.com:acme/"+repoName+".git", sha, "private", "feat/x", time.Now().Add(-time.Hour))
	attachment := attachmentCreatePreview(t, ctx, h, groupID, owner, "acme", repoName, sha, 7)
	fake.setPullCommits(sha)
	attachmentConfirm(t, h, owner, "acme", repoName, 7)
	if rec := attachmentServe(t, attachmentRouter(h), http.MethodDelete, "/api/v1/pulls/acme/"+repoName+"/7", owner); rec.Code != http.StatusOK {
		t.Fatalf("detach status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if rec := transcriptVisibilityPatch(t, h, ownerAuth, transcriptID, "public"); rec.Code != http.StatusOK {
		t.Fatalf("widen status = %d (%s), want 200", rec.Code, rec.Body.String())
	}

	if _, err := promptattach.Transition(ctx, h.queries, attachment.ID, promptattach.Preview); err != nil {
		t.Fatalf("preview again: %v", err)
	}
	attachmentConfirm(t, h, owner, "acme", repoName, 7)
	bindings, err := h.queries.ListPullRequestAttachmentTranscripts(ctx, attachment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[0].PreviousVisibility != "public" {
		t.Fatalf("bindings = %+v after attaching again, want one binding recorded at public and not widened", bindings)
	}
	postedNow(fake).assertListed(t, true, "success")
}
