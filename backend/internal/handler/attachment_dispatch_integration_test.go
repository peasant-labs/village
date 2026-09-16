//go:build integration

package handler

import (
	"context"
	"fmt"
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

// TestIssueCommentFromAuthorAttaches_RealPostgres is the command path end to
// end: the author comments `/peasant attach` and the accepted transcript is
// attached, shared, and posted for.
func TestIssueCommentFromAuthorAttaches_RealPostgres(t *testing.T) {
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
	if attachment.State != "attached" {
		t.Fatalf("state = %q, want attached for a private repository without a preview preference", attachment.State)
	}
	if !attachment.GroupID.Valid || attachment.GroupID != groupID {
		t.Fatalf("attachment group = %v, want the linking collective", attachment.GroupID)
	}
	var visibility string
	if err := pool.QueryRow(ctx, "SELECT visibility FROM transcripts WHERE id = $1", transcriptID).Scan(&visibility); err != nil {
		t.Fatal(err)
	}
	if visibility != "shared" {
		t.Fatalf("visibility = %q, want shared", visibility)
	}
	fake.mu.Lock()
	comments, checks := fake.commentCreates, fake.checkCreates
	fake.mu.Unlock()
	if comments != 1 || checks != 1 {
		t.Fatalf("comment creates = %d and check creates = %d, want one each", comments, checks)
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

// TestCheckRunButtonAttachesForTheAuthor proves the check run's own buttons reach
// the same policy as a comment.
func TestCheckRunButtonAttachesForTheAuthor(t *testing.T) {
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
	if attachment.State != "attached" {
		t.Fatalf("state = %q, want attached", attachment.State)
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

	// Attach first through the author's comment.
	fake.setPullRequest(sha, 993005)
	fake.setPullCommits(sha)
	h.githubDispatcher = promptCommandDispatcher{h: h}
	if err := dispatchEvent(t, h, "issue_comment", issueCommentEvent(repoName, 25, 993005, "NONE", "/peasant attach")); err != nil {
		t.Fatal(err)
	}
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
	edits, updates := fake.commentEdits, fake.checkUpdates
	fake.mu.Unlock()
	if edits != 1 || updates != 1 {
		t.Fatalf("comment edits = %d and check updates = %d, want one each", edits, updates)
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
