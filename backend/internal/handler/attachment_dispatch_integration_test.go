//go:build integration

package handler

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/github"
)

// The webhook-driven entry points: a comment, a check-run button, and a push.
// Each test dispatches the event the way the receiver does, so the dispatcher's
// decode, the authorization table, and the lifecycle are exercised together
// against real PostgreSQL.

func dispatchEvent(t *testing.T, h *Handler, eventType, payload string) error {
	t.Helper()
	dispatcher := h.githubDispatcher
	if dispatcher == nil {
		dispatcher = noopGitHubDispatcher{}
	}
	return github.Dispatch(context.Background(), dispatcher, github.Event{
		Type:       eventType,
		DeliveryID: uuid.NewString(),
		Payload:    []byte(payload),
	})
}

func issueCommentEvent(repoName string, number int, senderID int64, association, body string) string {
	return fmt.Sprintf(`{
		"action": "created",
		"issue": {"number": %d, "user": {"id": 1001, "login": "author"}, "pull_request": {"url": "https://api.github.com/x"}},
		"comment": {"id": 5, "body": %q, "author_association": %q, "user": {"id": %d, "login": "sender"}},
		"repository": {"id": 4242, "name": %q, "owner": {"id": 9, "login": "acme"}},
		"sender": {"id": %d, "login": "sender"},
		"installation": {"id": 4242}
	}`, number, body, association, senderID, repoName, senderID)
}

func checkRunEvent(repoName string, number int, senderID int64, identifier string) string {
	return fmt.Sprintf(`{
		"action": "requested_action",
		"requested_action": {"identifier": %q},
		"check_run": {"id": 11, "head_sha": "abc1234", "pull_requests": [{"number": %d}]},
		"repository": {"id": 4242, "name": %q, "owner": {"id": 9, "login": "acme"}},
		"sender": {"id": %d, "login": "sender"},
		"installation": {"id": 4242}
	}`, identifier, number, repoName, senderID)
}

func pullRequestEvent(repoName string, number int, authorID int64, headSHA string) string {
	return fmt.Sprintf(`{
		"action": "synchronize",
		"number": %d,
		"pull_request": {
			"number": %d,
			"user": {"id": %d, "login": "author"},
			"head": {"ref": "feat/x", "sha": %q, "repo": {"id": 4242, "name": %q, "owner": {"id": 1001, "login": "author"}}},
			"base": {"ref": "main", "sha": "base", "repo": {"id": 4242, "name": %q, "owner": {"id": 9, "login": "acme"}}},
			"merged": false
		},
		"repository": {"id": 4242, "name": %q, "owner": {"id": 9, "login": "acme"}},
		"sender": {"id": %d, "login": "author"},
		"installation": {"id": 4242}
	}`, number, number, authorID, headSHA, repoName, repoName, repoName, authorID)
}

// TestIssueCommentFromAuthorPreviews_RealPostgres is the command path end to
// end: the author comments `/peasant attach` and the accepted transcript is
// previewed — nothing widened, nothing posted — until the author confirms.
func TestIssueCommentFromAuthorPreviews_RealPostgres(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 993001)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "comment-" + fmt.Sprintf("%d", time.Now().UnixNano())[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")
	sha := "abc1234000000000000000000000000000000009"
	transcriptID := attachmentSeedTranscript(t, ctx, pool, blobs, owner, "git@github.com:acme/"+repoName+".git", sha, "private", "feat/x", time.Now().Add(-time.Hour))

	fake.setPullRequest(sha, 993001)
	fake.setPullCommits(sha)
	h.githubDispatcher = promptCommandDispatcher{h: h}

	if err := dispatchEvent(t, h, "issue_comment", issueCommentEvent(repoName, 21, 993001, "NONE", "/peasant attach")); err != nil {
		t.Fatalf("dispatch the attach comment: %v", err)
	}

	attachment, err := h.queries.GetPullRequestAttachmentForPull(ctx, sqlc.GetPullRequestAttachmentForPullParams{Lower: "acme", Lower_2: repoName, Number: 21})
	if err != nil {
		t.Fatalf("the author's comment created no attachment: %v", err)
	}
	if attachment.State != "preview" {
		t.Fatalf("state = %q, want preview: a person's click asks the author to confirm before any visibility changes", attachment.State)
	}
	if !attachment.GroupID.Valid || attachment.GroupID != groupID {
		t.Fatalf("attachment group = %v, want the linking collective", attachment.GroupID)
	}
	var visibility string
	if err := pool.QueryRow(ctx, "SELECT visibility FROM transcripts WHERE id = $1", transcriptID).Scan(&visibility); err != nil {
		t.Fatal(err)
	}
	if visibility != "private" {
		t.Fatalf("visibility = %q before the author confirmed, want private: a preview widens and shares nothing", visibility)
	}
	fake.mu.Lock()
	comments, checks := fake.commentCreates, fake.checkCreates
	fake.mu.Unlock()
	if comments != 0 || checks != 0 {
		t.Fatalf("comment creates = %d and check creates = %d before the author confirmed, want none: nothing is posted until they do", comments, checks)
	}
}

// TestIssueCommentFromNonAuthorRecordsARequest proves a repository member's
// request records who asked without exposing anything.
func TestIssueCommentFromNonAuthorRecordsARequest(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 993002)
	defer cleanupOwners(t, ctx, pool, owner)
	member := attachmentInsertOwner(t, ctx, pool, 2002)
	defer cleanupOwners(t, ctx, pool, member)

	repoName := "request-" + fmt.Sprintf("%d", time.Now().UnixNano())[:8]
	attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")
	sha := "bbb1234000000000000000000000000000000010"
	transcriptID := attachmentSeedTranscript(t, ctx, pool, blobs, owner, "git@github.com:acme/"+repoName+".git", sha, "private", "", time.Now().Add(-time.Hour))

	fake.setPullRequest(sha, 993002)
	fake.setPullCommits(sha)
	h.githubDispatcher = promptCommandDispatcher{h: h}

	if err := dispatchEvent(t, h, "issue_comment", issueCommentEvent(repoName, 22, 2002, "MEMBER", "/peasant attach")); err != nil {
		t.Fatalf("dispatch the member's comment: %v", err)
	}

	attachment, err := h.queries.GetPullRequestAttachmentForPull(ctx, sqlc.GetPullRequestAttachmentForPullParams{Lower: "acme", Lower_2: repoName, Number: 22})
	if err != nil {
		t.Fatalf("the member's comment created no attachment: %v", err)
	}
	if attachment.State != "requested" {
		t.Fatalf("state = %q, want requested", attachment.State)
	}
	if !attachment.RequesterGithubID.Valid || attachment.RequesterGithubID.Int64 != 2002 {
		t.Fatalf("requester = %v, want 2002 recorded", attachment.RequesterGithubID)
	}
	var visibility string
	if err := pool.QueryRow(ctx, "SELECT visibility FROM transcripts WHERE id = $1", transcriptID).Scan(&visibility); err != nil {
		t.Fatal(err)
	}
	if visibility != "private" {
		t.Fatalf("a request changed visibility to %q", visibility)
	}
	fake.mu.Lock()
	comments := fake.commentCreates
	fake.mu.Unlock()
	if comments != 0 {
		t.Fatalf("a request posted %d comments, want none", comments)
	}
}

// TestIssueCommentFromStrangerIsIgnored proves a contributor with no standing
// changes nothing at all.
func TestIssueCommentFromStrangerIsIgnored(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 993003)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "stranger-" + fmt.Sprintf("%d", time.Now().UnixNano())[:8]
	attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")
	sha := "ccc1234000000000000000000000000000000011"
	attachmentSeedTranscript(t, ctx, pool, blobs, owner, "git@github.com:acme/"+repoName+".git", sha, "private", "", time.Now().Add(-time.Hour))

	fake.setPullRequest(sha, 993003)
	fake.setPullCommits(sha)
	h.githubDispatcher = promptCommandDispatcher{h: h}

	if err := dispatchEvent(t, h, "issue_comment", issueCommentEvent(repoName, 23, 3003, "CONTRIBUTOR", "/peasant attach")); err != nil {
		t.Fatalf("dispatch the stranger's comment: %v", err)
	}
	if _, err := h.queries.GetPullRequestAttachmentForPull(ctx, sqlc.GetPullRequestAttachmentForPullParams{Lower: "acme", Lower_2: repoName, Number: 23}); err == nil {
		t.Fatal("a stranger's comment created an attachment")
	}
}

// TestCheckRunButtonPreviewsForTheAuthor proves the check run's own buttons reach
// the same policy as a comment: a click asks first. The menu no longer offers an
// attach action, so this is the click a run created before its removal still
// delivers.
func TestCheckRunButtonPreviewsForTheAuthor(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 993004)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "button-" + fmt.Sprintf("%d", time.Now().UnixNano())[:8]
	attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")
	sha := "ddd1234000000000000000000000000000000012"
	attachmentSeedTranscript(t, ctx, pool, blobs, owner, "git@github.com:acme/"+repoName+".git", sha, "private", "", time.Now().Add(-time.Hour))

	fake.setPullRequest(sha, 993004)
	fake.setPullCommits(sha)
	h.githubDispatcher = promptCommandDispatcher{h: h}

	if err := dispatchEvent(t, h, "check_run", checkRunEvent(repoName, 24, 993004, "attach")); err != nil {
		t.Fatalf("dispatch the attach button: %v", err)
	}
	attachment, err := h.queries.GetPullRequestAttachmentForPull(ctx, sqlc.GetPullRequestAttachmentForPullParams{Lower: "acme", Lower_2: repoName, Number: 24})
	if err != nil {
		t.Fatalf("the button created no attachment: %v", err)
	}
	if attachment.State != "preview" {
		t.Fatalf("state = %q, want preview: the button asks the author to confirm like every other click", attachment.State)
	}
}

// TestPullRequestPushRefreshesAnAttachedAttachment proves a push recomputes the
// digest for the new head without a second click: the comment is edited, not
// reposted, and the check is updated for the new SHA.
func TestPullRequestPushRefreshesAnAttachedAttachment(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 993005)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "push-" + fmt.Sprintf("%d", time.Now().UnixNano())[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")
	sha := "eee1234000000000000000000000000000000013"
	next := "fff1234000000000000000000000000000000014"
	transcriptID := attachmentSeedTranscript(t, ctx, pool, blobs, owner, "git@github.com:acme/"+repoName+".git", sha, "private", "", time.Now().Add(-time.Hour))

	// Preview through the author's comment, then confirm, which is how an
	// attachment becomes attached: a click asks first.
	fake.setPullRequest(sha, 993005)
	fake.setPullCommits(sha)
	h.githubDispatcher = promptCommandDispatcher{h: h}
	if err := dispatchEvent(t, h, "issue_comment", issueCommentEvent(repoName, 25, 993005, "NONE", "/peasant attach")); err != nil {
		t.Fatal(err)
	}
	attachmentConfirm(t, h, owner, "acme", repoName, 25)
	attached, err := h.queries.GetPullRequestAttachmentForPull(ctx, sqlc.GetPullRequestAttachmentForPullParams{Lower: "acme", Lower_2: repoName, Number: 25})
	if err != nil {
		t.Fatal(err)
	}
	if attached.State != "attached" {
		t.Fatalf("state = %q before the push, want attached", attached.State)
	}
	before, err := h.queries.GetPullRequestAttachment(ctx, attached.ID)
	if err != nil {
		t.Fatal(err)
	}

	// The push carries the same recorded commit, so the transcript stays
	// accepted and the digest is recomputed for the new head SHA.
	fake.setPullRequest(next, 993005)
	fake.setPullCommits(sha)
	if err := dispatchEvent(t, h, "pull_request", pullRequestEvent(repoName, 25, 993005, next)); err != nil {
		t.Fatalf("dispatch the push: %v", err)
	}

	after, err := h.queries.GetPullRequestAttachment(ctx, attached.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "attached" {
		t.Fatalf("state = %q after the push, want attached", after.State)
	}
	if after.HeadSha != next {
		t.Fatalf("head sha = %q, want the pushed %q", after.HeadSha, next)
	}
	if before.CommentID != after.CommentID {
		t.Fatalf("comment id changed from %v to %v, want the same edited comment", before.CommentID, after.CommentID)
	}
	if string(before.Digest) == string(after.Digest) && before.HeadSha == after.HeadSha {
		t.Fatal("the refresh changed nothing")
	}
	fake.mu.Lock()
	edits, updates, creates := fake.commentEdits, fake.checkUpdates, fake.checkCreates
	createdSHAs := append([]string(nil), fake.checkCreateSHAs...)
	fake.mu.Unlock()
	if edits != 1 {
		t.Fatalf("comment edits = %d, want the one sticky comment edited in place", edits)
	}
	// A check run's head SHA is fixed at creation, so the new head needs a NEW
	// run: an update would leave the new commit with no check at all.
	if creates != 2 || updates != 0 {
		t.Fatalf("check creates = %d and updates = %d, want a new run for the new head and no update", creates, updates)
	}
	if len(createdSHAs) != 2 || createdSHAs[1] != next {
		t.Fatalf("check runs were created for %v, want the second for the pushed head %q", createdSHAs, next)
	}
	// The transcript was already bound and stays exactly as it was.
	var bindings int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM pull_request_attachment_transcripts WHERE attachment_id = $1", attached.ID).Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	if bindings != 1 {
		t.Fatalf("bindings = %d, want the one bound transcript", bindings)
	}
	var visibility string
	if err := pool.QueryRow(ctx, "SELECT visibility FROM transcripts WHERE id = $1", transcriptID).Scan(&visibility); err != nil {
		t.Fatal(err)
	}
	if visibility != "shared" {
		t.Fatalf("visibility = %q, want shared", visibility)
	}
	var derived string
	if err := pool.QueryRow(ctx, `
		SELECT status FROM transcript_shares WHERE transcript_id = $1 AND group_id = $2
	`, transcriptID, groupID).Scan(&derived); err != nil {
		t.Fatal(err)
	}
	if derived != "approved" {
		t.Fatalf("derived share = %q, want approved", derived)
	}
}

// TestCheckRunRefreshButtonRecomputes proves the Refresh button is real: it is
// the author's recovery path when a publish-driven refresh failed, and it must
// not be silently dropped.
func TestCheckRunRefreshButtonRecomputes(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 993010)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "refresh-button-" + fmt.Sprintf("%d", time.Now().UnixNano())[:8]
	attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")
	sha := "5551234000000000000000000000000000000035"
	attachmentSeedTranscript(t, ctx, pool, blobs, owner, "git@github.com:acme/"+repoName+".git", sha, "private", "", time.Now().Add(-time.Hour))

	fake.setPullRequest(sha, 993010)
	fake.setPullCommits(sha)
	h.githubDispatcher = promptCommandDispatcher{h: h}
	if err := dispatchEvent(t, h, "issue_comment", issueCommentEvent(repoName, 51, 993010, "NONE", "/peasant attach")); err != nil {
		t.Fatal(err)
	}
	attachmentConfirm(t, h, owner, "acme", repoName, 51)
	attached, err := h.queries.GetPullRequestAttachmentForPull(ctx, sqlc.GetPullRequestAttachmentForPullParams{Lower: "acme", Lower_2: repoName, Number: 51})
	if err != nil {
		t.Fatal(err)
	}
	// A refresh with nothing changed is a no-op by design; move the head so the
	// button has work to do, which is the state a failed publish-driven refresh
	// leaves behind.
	moved := "aaa9999000000000000000000000000000000039"
	fake.setPullRequest(moved, 993010)
	fake.setPullCommits(sha)

	fake.mu.Lock()
	editsBefore := fake.commentEdits
	fake.mu.Unlock()

	if err := dispatchEvent(t, h, "check_run", checkRunEvent(repoName, 51, 993010, "refresh")); err != nil {
		t.Fatalf("dispatch the refresh button: %v", err)
	}

	fake.mu.Lock()
	editsAfter := fake.commentEdits
	fake.mu.Unlock()
	if editsAfter != editsBefore+1 {
		t.Fatalf("refresh button edited the comment %d times, want once: the button is a recovery path and must act", editsAfter-editsBefore)
	}
	updated, err := h.queries.GetPullRequestAttachment(ctx, attached.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != "attached" {
		t.Fatalf("state = %q after refresh, want attached", updated.State)
	}
}

// TestDeletedCommandCommentDoesNotReexecute proves the action guard: GitHub
// sends the comment body on edit and delete too, and acting on those would
// re-run a command the author withdrew.
func TestDeletedCommandCommentDoesNotReexecute(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 993011)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "deleted-command-" + fmt.Sprintf("%d", time.Now().UnixNano())[:8]
	attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")
	sha := "6661234000000000000000000000000000000036"
	attachmentSeedTranscript(t, ctx, pool, blobs, owner, "git@github.com:acme/"+repoName+".git", sha, "private", "", time.Now().Add(-time.Hour))

	fake.setPullRequest(sha, 993011)
	fake.setPullCommits(sha)
	h.githubDispatcher = promptCommandDispatcher{h: h}

	deleted := strings.Replace(issueCommentEvent(repoName, 52, 993011, "NONE", "/peasant attach"), `"action": "created"`, `"action": "deleted"`, 1)
	if err := dispatchEvent(t, h, "issue_comment", deleted); err != nil {
		t.Fatalf("dispatch the deleted command: %v", err)
	}
	if _, err := h.queries.GetPullRequestAttachmentForPull(ctx, sqlc.GetPullRequestAttachmentForPullParams{Lower: "acme", Lower_2: repoName, Number: 52}); err == nil {
		t.Fatal("deleting a command comment re-executed it")
	}
}

// TestForkPullRequestMatchesTheHeadRepository proves the fork branch is
// reachable: the head repository's own remote is what the matcher can match, so
// a transcript recorded from the fork is accepted and the base name alone is not
// used for it.
func TestForkPullRequestMatchesTheHeadRepository(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 993012)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "fork-" + fmt.Sprintf("%d", time.Now().UnixNano())[:8]
	forkName := "fork-of-" + repoName
	groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")
	_ = groupID
	sha := "7771234000000000000000000000000000000037"
	transcriptID := attachmentSeedTranscript(t, ctx, pool, blobs, owner, "git@github.com:author/"+forkName+".git", sha, "private", "feat/x", time.Now().Add(-time.Hour))

	// The pull request is a fork: head repo author/fork-of-<repo>, base
	// acme/<repo>. The attachment is created by the comment, so its stored head
	// remote is the fork the resolver read, which is what the matcher must match.
	fake.setPullRequestDetail(sha, 993012, "author/"+forkName, "acme/"+repoName)
	fake.setPullCommits(sha)
	h.githubDispatcher = promptCommandDispatcher{h: h}
	if err := dispatchEvent(t, h, "issue_comment", issueCommentEvent(repoName, 53, 993012, "NONE", "/peasant attach")); err != nil {
		t.Fatal(err)
	}
	stored, err := h.queries.GetPullRequestAttachmentForPull(ctx, sqlc.GetPullRequestAttachmentForPullParams{Lower: "acme", Lower_2: repoName, Number: 53})
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != "preview" {
		t.Fatalf("state = %q, want preview: the fork head remote must be matchable, and a click still asks first", stored.State)
	}
	var visibility string
	if err := pool.QueryRow(ctx, "SELECT visibility FROM transcripts WHERE id = $1", transcriptID).Scan(&visibility); err != nil {
		t.Fatal(err)
	}
	if visibility != "private" {
		t.Fatalf("visibility = %q before the author confirmed, want private", visibility)
	}
	_ = fake
}

func pullRequestForkEvent(repoName, forkName string, number int, authorID int64, headSHA string) string {
	return fmt.Sprintf(`{
		"action": "synchronize",
		"number": %d,
		"pull_request": {
			"number": %d,
			"user": {"id": %d, "login": "author"},
			"head": {"ref": "feat/x", "sha": %q, "repo": {"id": 55, "name": %q, "full_name": "author/%s", "owner": {"id": %d, "login": "author"}}},
			"base": {"ref": "main", "sha": "base", "repo": {"id": 4242, "name": %q, "full_name": "acme/%s", "owner": {"id": 9, "login": "acme"}}},
			"merged": false
		},
		"repository": {"id": 4242, "name": %q, "owner": {"id": 9, "login": "acme"}},
		"sender": {"id": %d, "login": "author"},
		"installation": {"id": 4242}
	}`, number, number, authorID, headSHA, forkName, forkName, authorID, repoName, repoName, repoName, authorID)
}

// TestRepeatAttachClickOnAnAttachedAttachmentRefreshes proves a second click is
// not an error. The prompts are already attached, so there is nothing to ask: the
// click refreshes the digest for the current head instead of trying to move an
// attached attachment back to preview, which the state table refuses and the
// webhook handler swallows. A check run outlives a detach and runs created before
// the attach action was removed still offer the button, so this click is
// reachable in production rather than only here.
func TestRepeatAttachClickOnAnAttachedAttachmentRefreshes(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 993014)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "repeat-click-" + fmt.Sprintf("%d", time.Now().UnixNano())[:8]
	attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")
	sha := "fff1234000000000000000000000000000000032"
	attachmentSeedTranscript(t, ctx, pool, blobs, owner, "git@github.com:acme/"+repoName+".git", sha, "private", "", time.Now().Add(-time.Hour))

	fake.setPullRequest(sha, 993014)
	fake.setPullCommits(sha)
	h.githubDispatcher = promptCommandDispatcher{h: h}
	if err := dispatchEvent(t, h, "issue_comment", issueCommentEvent(repoName, 61, 993014, "NONE", "/peasant attach")); err != nil {
		t.Fatal(err)
	}
	attachmentConfirm(t, h, owner, "acme", repoName, 61)

	// Move the head so the refresh has work to do: a refresh with nothing changed
	// is a no-op by design.
	moved := "bbb7777000000000000000000000000000000033"
	fake.setPullRequest(moved, 993014)
	fake.setPullCommits(sha)

	fake.mu.Lock()
	commentsBefore, editsBefore := fake.commentCreates, fake.commentEdits
	fake.mu.Unlock()

	if err := dispatchEvent(t, h, "issue_comment", issueCommentEvent(repoName, 61, 993014, "NONE", "/peasant attach")); err != nil {
		t.Fatalf("a repeat attach click: %v", err)
	}

	stored, err := h.queries.GetPullRequestAttachmentForPull(ctx, sqlc.GetPullRequestAttachmentForPullParams{Lower: "acme", Lower_2: repoName, Number: 61})
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != "attached" {
		t.Fatalf("state = %q after a repeat click, want attached: the prompts were already attached", stored.State)
	}
	fake.mu.Lock()
	commentsAfter, editsAfter := fake.commentCreates, fake.commentEdits
	fake.mu.Unlock()
	if commentsAfter != commentsBefore {
		t.Fatalf("comment creates went from %d to %d: a repeat click edits the digest, it does not post a second comment", commentsBefore, commentsAfter)
	}
	if editsAfter <= editsBefore {
		t.Fatalf("comment edits stayed at %d: the repeat click did not refresh the digest for the new head", editsAfter)
	}
}
