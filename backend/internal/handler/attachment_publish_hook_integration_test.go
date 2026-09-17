//go:build integration

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/promptattach"
)

// The publish hook: a stored transcript completes a waiting request or refreshes
// an attached attachment for the same repository, through the same acceptance
// policy a click uses. These tests publish through the real handler, so the hook
// runs exactly where it does in production.

// attachmentPublish stores one transcript carrying a git remote and a recorded
// commit, which is what makes it a candidate for the attachment's repository.
func attachmentPublish(t *testing.T, h *Handler, owner pgtype.UUID, username, remote, commitSHA string) (int, string) {
	t.Helper()
	return attachmentPublishSession(t, h, owner, username, remote, commitSHA, uuid.NewString(), attachmentPublicationContent())
}

// attachmentPublishSession publishes under a chosen session id and body, so a
// test can publish the same session twice and drive the republish path: the
// second publish replaces the content of a transcript that already exists.
func attachmentPublishSession(t *testing.T, h *Handler, owner pgtype.UUID, username, remote, commitSHA, sessionID string, content []byte) (int, string) {
	t.Helper()
	remoteCopy := remote
	branch := "feat/x"
	metadata := schema.PublishRequest{
		Identity:    schema.SessionIdentity{SessionID: schema.SessionID(sessionID), SchemaVersion: 2},
		Model:       schema.ModelInfo{Harness: schema.HarnessClaudeCode, Model: "attachment-test"},
		Timestamp:   schema.TimestampInfo{Start: 1700000000000, End: 1700000060000},
		Source:      schema.SourceInfo{FilePath: "/fixtures/attachment.jsonl", Format: "jsonl"},
		Git:         schema.GitContext{Remote: &remoteCopy, Branch: &branch, Commits: []schema.CommitInfo{{Hash: commitSHA, Message: "m", AuthorName: "A", AuthorEmail: "a@x.io", CommitTime: 1700000000000, AuthorTime: 1700000000000}}},
		Project:     schema.ProjectContext{Hash: testProjectHash, Name: "attachment-fixture"},
		Stats:       schema.SessionStats{TurnCount: 2, ToolCallCount: 0, DurationMs: 1000, TokensIn: 60, TokensOut: 40},
		Diagnostics: schema.DiagnosticsInfo{Warnings: []schema.DiagnosticEntry{}},
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal attachment publish metadata: %v", err)
	}
	body, boundary := multipartBody(t, map[string]string{"metadata": string(metadataJSON)}, string(content))
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(context.WithValue(context.Background(), UserContextKey, &AuthUser{ID: uuid.UUID(owner.Bytes), Username: username}))
	w := httptest.NewRecorder()
	h.PublishTranscript(w, r)
	return w.Code, w.Body.String()
}

func attachmentWaiting(t *testing.T, ctx context.Context, h *Handler, groupID, owner pgtype.UUID, repoName, sha string, number int) sqlc.PullRequestAttachment {
	t.Helper()
	attachment, err := h.queries.CreatePullRequestAttachment(ctx, sqlc.CreatePullRequestAttachmentParams{
		GroupID: groupID, RepoOwner: "acme", RepoName: repoName, GithubRepoID: 4242, Number: int32(number),
		HeadSha: sha, BaseRemote: "acme/" + repoName, HeadRemote: "acme/" + repoName, AuthorID: owner,
	})
	if err != nil {
		t.Fatalf("create waiting attachment: %v", err)
	}
	moved, err := promptattach.Transition(ctx, h.queries, attachment.ID, promptattach.Waiting)
	if err != nil {
		t.Fatalf("move attachment to waiting: %v", err)
	}
	return moved
}

// TestPublishedTranscriptCompletesAWaitingAttachment is the hooked-repository
// case: nobody clicks again, and the publish completes the request the author
// had already consented to.
func TestPublishedTranscriptCompletesAWaitingAttachment(t *testing.T) {
	h, pool, _, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 993006)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "publish-wait-" + fmt.Sprintf("%d", time.Now().UnixNano())[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")
	sha := "aaa1234000000000000000000000000000000021"
	attachment := attachmentWaiting(t, ctx, h, groupID, owner, repoName, sha, 31)

	fake.setPullRequest(sha, 993006)
	fake.setPullCommits(sha)

	code, body := attachmentPublish(t, h, owner, "attachment-owner", "git@github.com:acme/"+repoName+".git", sha)
	if code != http.StatusCreated {
		t.Fatalf("publish status = %d (%s), want 201", code, body)
	}

	updated, err := h.queries.GetPullRequestAttachment(ctx, attachment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != "attached" {
		t.Fatalf("state = %q after the publish, want attached", updated.State)
	}
	if !updated.CommentID.Valid {
		t.Error("no sticky comment was recorded")
	}
	fake.mu.Lock()
	comments := fake.commentCreates
	fake.mu.Unlock()
	if comments != 1 {
		t.Fatalf("comment creates = %d, want 1", comments)
	}
}

// TestPublishedUnrelatedSessionLeavesTheRequestWaiting is the acceptance the
// hook exists to enforce: a transcript that the policy does not accept completes
// nothing and exposes nothing, even though its remote names the repository.
func TestPublishedUnrelatedSessionLeavesTheRequestWaiting(t *testing.T) {
	h, pool, _, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 993007)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "publish-unrelated-" + fmt.Sprintf("%d", time.Now().UnixNano())[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")
	sha := "bbb1234000000000000000000000000000000022"
	_ = groupID
	attachment := attachmentWaiting(t, ctx, h, groupID, owner, repoName, sha, 32)

	// The pull request carries a different commit, so the published transcript's
	// recorded commit does not resolve into it. The published transcript also
	// records the pull request's OWN branch name, which is exactly the
	// coincidence the acceptance names: a branch is discovery, never acceptance,
	// so a same-repository session on the same branch still completes nothing.
	fake.setPullRequest(sha, 993007)
	fake.setPullCommits("9991234000000000000000000000000000000023")

	code, body := attachmentPublish(t, h, owner, "attachment-owner", "git@github.com:acme/"+repoName+".git", "8881234000000000000000000000000000000024")
	if code != http.StatusCreated {
		t.Fatalf("publish status = %d (%s), want 201", code, body)
	}

	updated, err := h.queries.GetPullRequestAttachment(ctx, attachment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != "waiting" {
		t.Fatalf("state = %q, want waiting: an unrelated session must not complete the request", updated.State)
	}
	if updated.CommentID.Valid || len(updated.Digest) > 0 {
		t.Fatalf("attachment = %+v, want no digest and no comment", updated)
	}
	var bindings int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM pull_request_attachment_transcripts WHERE attachment_id = $1", attachment.ID).Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	if bindings != 0 {
		t.Fatalf("bindings = %d, want none", bindings)
	}
	fake.mu.Lock()
	comments := fake.commentCreates
	fake.mu.Unlock()
	if comments != 0 {
		t.Fatalf("an unrelated session posted %d comments", comments)
	}
}

// TestPublishedAcceptedSessionRefreshesAnAttachedAttachment proves an attached
// attachment extends when a later publish is accepted: the new transcript is
// widened and the digest, edited in place, grows to include it.
func TestPublishedAcceptedSessionRefreshesAnAttachedAttachment(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 993008)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "publish-refresh-" + fmt.Sprintf("%d", time.Now().UnixNano())[:8]
	attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")
	first := "ccc1234000000000000000000000000000000025"
	second := "ddd1234000000000000000000000000000000026"
	attachmentSeedTranscript(t, ctx, pool, blobs, owner, "git@github.com:acme/"+repoName+".git", first, "private", "", time.Now().Add(-2*time.Hour))
	secondTranscript := attachmentSeedTranscript(t, ctx, pool, blobs, owner, "git@github.com:acme/"+repoName+".git", second, "private", "", time.Now().Add(-time.Hour))

	// Attach through the author's comment first.
	fake.setPullRequest(first, 993008)
	fake.setPullCommits(first)
	h.githubDispatcher = promptCommandDispatcher{h: h}
	if err := dispatchEvent(t, h, "issue_comment", issueCommentEvent(repoName, 33, 993008, "NONE", "/peasant attach")); err != nil {
		t.Fatal(err)
	}
	attached, err := h.queries.GetPullRequestAttachmentForPull(ctx, sqlc.GetPullRequestAttachmentForPullParams{Lower: "acme", Lower_2: repoName, Number: 33})
	if err != nil {
		t.Fatal(err)
	}
	if attached.State != "attached" {
		t.Fatalf("state = %q before the publish, want attached", attached.State)
	}
	before := attached.Digest

	// A later publish on the same repository whose commit IS in the pull request
	// is accepted and extends the digest.
	fake.setPullCommits(first, second)
	code, body := attachmentPublish(t, h, owner, "attachment-owner", "git@github.com:acme/"+repoName+".git", second)
	if code != http.StatusCreated {
		t.Fatalf("publish status = %d (%s), want 201", code, body)
	}

	updated, err := h.queries.GetPullRequestAttachment(ctx, attached.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != "attached" {
		t.Fatalf("state = %q, want attached", updated.State)
	}
	if string(updated.Digest) == string(before) {
		t.Fatal("the digest did not change after an accepted publish")
	}
	if !strings.Contains(string(updated.Digest), uuid.UUID(secondTranscript.Bytes).String()) {
		t.Fatalf("the digest does not mention the newly accepted transcript: %s", string(updated.Digest))
	}

	var addedVisibility string
	if err := pool.QueryRow(ctx, "SELECT visibility FROM transcripts WHERE id = $1", secondTranscript).Scan(&addedVisibility); err != nil {
		t.Fatal(err)
	}
	if addedVisibility != "shared" {
		t.Fatalf("the newly accepted transcript visibility = %q, want shared", addedVisibility)
	}
	var bindings int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM pull_request_attachment_transcripts WHERE attachment_id = $1", attached.ID).Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	// Three: the two seeded transcripts and the published one, whose recorded
	// commit is also in the pull request, so the same policy accepts it too.
	if bindings != 3 {
		t.Fatalf("bindings = %d, want the two seeded transcripts and the published one", bindings)
	}
	fake.mu.Lock()
	edits := fake.commentEdits
	fake.mu.Unlock()
	if edits != 1 {
		t.Fatalf("comment edits = %d, want 1 (edited in place, not reposted)", edits)
	}
}

// TestPublishedUnrelatedSessionDoesNotRepost proves the other half: an unrelated
// publish on an attached pull request does not edit the digest at all.
func TestPublishedUnrelatedSessionDoesNotRepost(t *testing.T) {
	h, pool, _, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 993009)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "publish-noop-" + fmt.Sprintf("%d", time.Now().UnixNano())[:8]
	attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")
	sha := "eee1234000000000000000000000000000000027"

	fake.setPullRequest(sha, 993009)
	fake.setPullCommits(sha)
	h.githubDispatcher = promptCommandDispatcher{h: h}
	if err := dispatchEvent(t, h, "issue_comment", issueCommentEvent(repoName, 34, 993009, "NONE", "/peasant attach")); err != nil {
		t.Fatal(err)
	}
	attached, err := h.queries.GetPullRequestAttachmentForPull(ctx, sqlc.GetPullRequestAttachmentForPullParams{Lower: "acme", Lower_2: repoName, Number: 34})
	if err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	editsBefore := fake.commentEdits
	fake.mu.Unlock()

	// The published transcript's commit is not in the pull request.
	code, body := attachmentPublish(t, h, owner, "attachment-owner", "git@github.com:acme/"+repoName+".git", "7771234000000000000000000000000000000028")
	if code != http.StatusCreated {
		t.Fatalf("publish status = %d (%s), want 201", code, body)
	}

	updated, err := h.queries.GetPullRequestAttachment(ctx, attached.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(updated.Digest) != string(attached.Digest) {
		t.Fatal("an unrelated publish changed the digest")
	}
	fake.mu.Lock()
	editsAfter := fake.commentEdits
	fake.mu.Unlock()
	if editsAfter != editsBefore {
		t.Fatalf("an unrelated publish edited the comment %d times, want none", editsAfter-editsBefore)
	}
}
