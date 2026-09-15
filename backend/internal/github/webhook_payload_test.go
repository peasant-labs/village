package github

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"io"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/payloads.yaml
var payloadsYAML []byte

// requiredPayloadCaseNames is the name manifest for testdata/payloads.yaml: the
// two event shapes, the pull-request-as-issue discriminator, and the fork
// distinction. Exact membership, never a count, so a deleted case is named.
var requiredPayloadCaseNames = []string{
	"issue-comment-on-a-pull-request-by-its-author",
	"issue-comment-on-a-plain-issue",
	"pull-request-from-a-fork",
	"pull-request-from-the-same-repository",
}

type payloadExpectation struct {
	IsPullRequest  bool   `yaml:"is_pull_request"`
	Number         int    `yaml:"number"`
	SenderID       int64  `yaml:"sender_id"`
	AuthorID       int64  `yaml:"author_id"`
	Association    string `yaml:"association"`
	CommentID      int64  `yaml:"comment_id"`
	CommentBody    string `yaml:"comment_body"`
	RepositoryID   int64  `yaml:"repository_id"`
	InstallationID int64  `yaml:"installation_id"`
	IsFork         bool   `yaml:"is_fork"`
	HeadRef        string `yaml:"head_ref"`
	BaseRepoName   string `yaml:"base_repo_name"`
	HeadRepoName   string `yaml:"head_repo_name"`
}

type payloadCase struct {
	Name    string             `yaml:"name"`
	Payload string             `yaml:"payload"`
	Expect  payloadExpectation `yaml:"expect"`
}

type payloadFixture struct {
	Cases []payloadCase `yaml:"cases"`
}

func loadPayloadCases(t *testing.T) []payloadCase {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(payloadsYAML))
	decoder.KnownFields(true)
	var fixture payloadFixture
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("decode strict payloads fixture: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("payloads fixture must contain exactly one YAML document: %v", err)
	}

	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" {
			t.Fatal("payloads fixture has a case with an empty name")
		}
		if seen[c.Name] {
			t.Fatalf("payloads fixture repeats case name %q", c.Name)
		}
		seen[c.Name] = true
	}
	declared := make(map[string]bool, len(requiredPayloadCaseNames))
	for _, name := range requiredPayloadCaseNames {
		declared[name] = true
	}
	var missing, undeclared []string
	for name := range declared {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	for name := range seen {
		if !declared[name] {
			undeclared = append(undeclared, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(undeclared)
	if len(missing) > 0 {
		t.Fatalf("testdata/payloads.yaml no longer carries %v, which requiredPayloadCaseNames declares: each "+
			"case pins a wire shape the decision reads. Restore the row under its exact name.", missing)
	}
	if len(undeclared) > 0 {
		t.Fatalf("testdata/payloads.yaml carries %v, which requiredPayloadCaseNames does not declare: an "+
			"undeclared case is unprotected, so add each new name to the manifest in the same change.", undeclared)
	}
	return fixture.Cases
}

// decodeInstalls installs all four decoding cases. The payload carries the
// event type implicitly (an issue object versus a pull_request object), so the
// test decodes into both shapes and reports which one answered.
func TestWebhookPayloadDecoding(t *testing.T) {
	for _, tc := range loadPayloadCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			raw := []byte(tc.Payload)

			var pr WebhookPullRequestPayload
			if err := json.Unmarshal(raw, &pr); err != nil {
				t.Fatalf("decode pull_request payload: %v", err)
			}
			if pr.PullRequest.Number != 0 {
				assertPullRequestPayload(t, pr, tc.Expect)
				return
			}

			var comment WebhookIssueCommentPayload
			if err := json.Unmarshal(raw, &comment); err != nil {
				t.Fatalf("decode issue_comment payload: %v", err)
			}
			if comment.Issue.Number == 0 {
				t.Fatal("payload decoded as neither a pull_request nor an issue_comment event")
			}
			assertIssueCommentPayload(t, comment, tc.Expect)
		})
	}
}

func assertIssueCommentPayload(t *testing.T, p WebhookIssueCommentPayload, want payloadExpectation) {
	t.Helper()
	if p.IsPullRequest() != want.IsPullRequest {
		t.Errorf("IsPullRequest() = %v, want %v", p.IsPullRequest(), want.IsPullRequest)
	}
	if p.Issue.Number != want.Number {
		t.Errorf("issue number = %d, want %d", p.Issue.Number, want.Number)
	}
	if p.Sender.ID != want.SenderID {
		t.Errorf("sender id = %d, want %d", p.Sender.ID, want.SenderID)
	}
	if p.Issue.User.ID != want.AuthorID {
		t.Errorf("issue user (pull request author) id = %d, want %d", p.Issue.User.ID, want.AuthorID)
	}
	if p.Comment.AuthorAssociation != want.Association {
		t.Errorf("author_association = %q, want %q", p.Comment.AuthorAssociation, want.Association)
	}
	if p.Comment.ID != want.CommentID {
		t.Errorf("comment id = %d, want %d", p.Comment.ID, want.CommentID)
	}
	if p.Comment.Body != want.CommentBody {
		t.Errorf("comment body = %q, want %q", p.Comment.Body, want.CommentBody)
	}
	if p.Repository.ID != want.RepositoryID {
		t.Errorf("repository id = %d, want %d", p.Repository.ID, want.RepositoryID)
	}
	if p.Installation == nil || p.Installation.ID != want.InstallationID {
		t.Errorf("installation = %+v, want id %d", p.Installation, want.InstallationID)
	}
}

func assertPullRequestPayload(t *testing.T, p WebhookPullRequestPayload, want payloadExpectation) {
	t.Helper()
	if p.Number != want.Number {
		t.Errorf("number = %d, want %d", p.Number, want.Number)
	}
	if p.Sender.ID != want.SenderID {
		t.Errorf("sender id = %d, want %d", p.Sender.ID, want.SenderID)
	}
	if p.PullRequest.User.ID != want.AuthorID {
		t.Errorf("pull request author id = %d, want %d", p.PullRequest.User.ID, want.AuthorID)
	}
	if p.IsFork() != want.IsFork {
		t.Errorf("IsFork() = %v, want %v", p.IsFork(), want.IsFork)
	}
	if p.PullRequest.Head.Ref != want.HeadRef {
		t.Errorf("head ref = %q, want %q", p.PullRequest.Head.Ref, want.HeadRef)
	}
	if p.PullRequest.Base.Repo.Name != want.BaseRepoName {
		t.Errorf("base repo = %q, want %q", p.PullRequest.Base.Repo.Name, want.BaseRepoName)
	}
	if p.PullRequest.Head.Repo.Name != want.HeadRepoName {
		t.Errorf("head repo = %q, want %q", p.PullRequest.Head.Repo.Name, want.HeadRepoName)
	}
}
