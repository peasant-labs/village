//go:build integration

package handler

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/prompts-toggle.yaml
var promptsToggleYAML []byte

type promptsToggleCase struct {
	Name   string   `yaml:"name"`
	Toggle bool     `yaml:"toggle"`
	Steps  []string `yaml:"steps"`
	Expect struct {
		State          string   `yaml:"state"`
		CommentCreates int      `yaml:"comment_creates"`
		CommentEdits   int      `yaml:"comment_edits"`
		CommentDeletes int      `yaml:"comment_deletes"`
		CheckCreates   int      `yaml:"check_creates"`
		CheckUpdates   int      `yaml:"check_updates"`
		ArtifactIDs    string   `yaml:"artifact_ids"`
		CheckHeads     []string `yaml:"check_heads"`
		Comment        string   `yaml:"comment"`
		DetailsURL     bool     `yaml:"details_url"`
	} `yaml:"expect"`
}

// requiredPromptsToggleCases names every row: the toggle on and off, crossed
// with a preview, an attach, and a "not now" on the preview.
var requiredPromptsToggleCases = []string{
	"off_confirm_keeps_the_preview_id",
	"off_refresh_keeps_both_posted_ids",
	"off_push_and_refresh_keep_both_posted_ids",
	"reenabled_refresh_creates_a_check_for_the_new_head",
	"off_detach_resets_the_posted_check",
	"off_not_now_deletes_the_posted_preview",

	"on_preview_posts_the_preview_comment",
	"on_attached_edits_the_preview_comment_and_posts_the_check",
	"on_not_now_deletes_the_preview_comment",
	"off_preview_posts_nothing",
	"off_attached_posts_nothing",
	"off_not_now_posts_nothing",
}

func loadPromptsToggleCases(t *testing.T) []promptsToggleCase {
	t.Helper()
	cases, err := decodeFixtureRows[promptsToggleCase](promptsToggleYAML)
	if err != nil {
		t.Fatalf("load testdata/prompts-toggle.yaml: %v", err)
	}
	present := map[string]struct{}{}
	for _, c := range cases {
		if _, repeated := present[c.Name]; c.Name == "" || repeated {
			t.Fatalf("prompts-toggle row name %q is empty or repeated", c.Name)
		}
		present[c.Name] = struct{}{}
		if len(c.Steps) == 0 || c.Steps[0] != "click" {
			t.Fatalf("row %q: steps %v must start with the author's click", c.Name, c.Steps)
		}
		for _, step := range c.Steps[1:] {
			if step != "confirm" && step != "not_now" && step != "flip_off" && step != "flip_on" && step != "push" && step != "refresh" {
				t.Fatalf("row %q: step %q is not a recognized action", c.Name, step)
			}
		}
	}
	assertExactTitleFixtureNames(t, "prompts-toggle", present, requiredPromptsToggleCases)
	return cases
}

// TestThePromptsToggleGatesEveryPost_RealPostgres drives each row of
// testdata/prompts-toggle.yaml through the production paths (the author's
// `/peasant attach` comment through the webhook dispatcher, then the mounted
// confirm or detach route) over real PostgreSQL, and reads what the fake GitHub
// received. Off posts and edits no digest. Explicit detach removes artifacts
// an earlier enabled post left, while the attachment itself still moves. Explicit detach removes artifacts an earlier enabled post left.
func TestThePromptsToggleGatesEveryPost_RealPostgres(t *testing.T) {
	for i, c := range loadPromptsToggleCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			h, pool, blobs, fake := attachmentTestHandler(t)
			h.githubDispatcher = promptCommandDispatcher{h: h}
			ctx := context.Background()
			authorGitHubID := 997100 + int64(i)*10
			owner := attachmentInsertOwner(t, ctx, pool, authorGitHubID)
			defer cleanupOwners(t, ctx, pool, owner)

			repoName := "toggle-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
			groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")
			if _, err := pool.Exec(ctx, `UPDATE groups SET post_prompts_check = $2 WHERE id = $1`, groupID, c.Toggle); err != nil {
				t.Fatalf("set the collective's prompts toggle: %v", err)
			}
			sha := fmt.Sprintf("e%039d", authorGitHubID)
			attachmentSeedTranscript(t, ctx, pool, blobs, owner, "git@github.com:acme/"+repoName+".git", sha, "private", "feat/x", time.Now().Add(-time.Hour))
			fake.setPullRequest(sha, authorGitHubID)
			fake.setPullCommits(sha)

			const number = 61
			newSHA := fmt.Sprintf("f%039d", authorGitHubID)
			for _, step := range c.Steps {
				switch step {
				case "click":
					if err := dispatchEvent(t, h, "issue_comment", issueCommentEvent(repoName, number, authorGitHubID, "NONE", "/peasant attach")); err != nil {
						t.Fatalf("dispatch the author's click: %v", err)
					}
				case "flip_off", "flip_on":
					if _, err := pool.Exec(ctx, `UPDATE groups SET post_prompts_check = $2 WHERE id = $1`, groupID, step == "flip_on"); err != nil {
						t.Fatal(err)
					}
				case "push":
					fake.setPullRequest(newSHA, authorGitHubID)
					fake.setPullCommits(sha, newSHA)
					if err := dispatchEvent(t, h, "pull_request", pullRequestEvent(repoName, number, authorGitHubID, newSHA)); err != nil {
						t.Fatalf("push: %v", err)
					}
				case "refresh":
					if err := dispatchEvent(t, h, "check_run", checkRunEvent(repoName, number, authorGitHubID, "refresh")); err != nil {
						t.Fatalf("refresh: %v", err)
					}
				case "confirm":
					attachmentConfirm(t, h, owner, "acme", repoName, number)
				case "not_now":
					if rec := attachmentServe(t, attachmentRouter(h), http.MethodDelete, fmt.Sprintf("/api/v1/pulls/acme/%s/%d", repoName, number), owner); rec.Code != http.StatusOK {
						t.Fatalf("not now: status = %d (%s), want 200", rec.Code, rec.Body.String())
					}
				}
			}

			attachment, err := h.queries.GetPullRequestAttachmentForPull(ctx, sqlc.GetPullRequestAttachmentForPullParams{Lower: "acme", Lower_2: repoName, Number: number})
			if err != nil {
				t.Fatalf("read the attachment: %v", err)
			}
			if attachment.State != c.Expect.State {
				t.Fatalf("state = %q, want %q: the toggle gates posting, never the attachment", attachment.State, c.Expect.State)
			}

			fake.mu.Lock()
			creates, edits, deletes := fake.commentCreates, fake.commentEdits, fake.commentDeletes
			checks, updates, body, detailsURL := fake.checkCreates, fake.checkUpdates, fake.lastCommentBody, fake.lastCheckDetailsURL
			heads := append([]string(nil), fake.checkCreateSHAs...)
			fake.mu.Unlock()
			if creates != c.Expect.CommentCreates || edits != c.Expect.CommentEdits || deletes != c.Expect.CommentDeletes {
				t.Fatalf("comments created/edited/deleted = %d/%d/%d, want %d/%d/%d",
					creates, edits, deletes, c.Expect.CommentCreates, c.Expect.CommentEdits, c.Expect.CommentDeletes)
			}
			if checks != c.Expect.CheckCreates || updates != c.Expect.CheckUpdates {
				t.Fatalf("check runs created/updated = %d/%d, want %d/%d", checks, updates, c.Expect.CheckCreates, c.Expect.CheckUpdates)
			}
			if len(heads) != len(c.Expect.CheckHeads) {
				t.Fatalf("check heads = %v, expected %v", heads, c.Expect.CheckHeads)
			}
			for i, expected := range c.Expect.CheckHeads {
				want := sha
				if expected == "new" {
					want = newSHA
				}
				if heads[i] != want {
					t.Fatalf("check %d head = %s, want %s", i, heads[i], want)
				}
			}
			if !strings.HasPrefix(body, c.Expect.Comment) {
				t.Fatalf("last comment = %q, want it to start %q", body, c.Expect.Comment)
			}
			if c.Expect.Comment == "" && body != "" {
				t.Fatalf("a comment was posted with the toggle off: %q", body)
			}
			pullPage := "https://village.example/pulls/acme/" + repoName + "/61"
			if c.Expect.DetailsURL != (detailsURL == pullPage) {
				t.Fatalf("check details_url = %q, want the pull request's page %q = %t", detailsURL, pullPage, c.Expect.DetailsURL)
			}
			if body != "" && !strings.Contains(body, pullPage) {
				t.Fatalf("the comment does not link the pull request's page %q: %s", pullPage, body)
			}
			// The private transcript's prompt reaches no surface, whatever the
			// toggle says.
			if strings.Contains(body, "please attach my prompts") {
				t.Fatalf("the comment carries the private transcript's prompt: %s", body)
			}
			wantComment := c.Expect.ArtifactIDs == "both" || c.Expect.ArtifactIDs == "comment"
			wantCheck := c.Expect.ArtifactIDs == "both"
			if attachment.CommentID.Valid != wantComment || attachment.CheckRunID.Valid != wantCheck || (wantComment && attachment.CommentID.Int64 != 22) || (wantCheck && attachment.CheckRunID.Int64 != int64(10+checks)) {
				t.Fatalf("recorded comment/check = %v/%v, want %s", attachment.CommentID, attachment.CheckRunID, c.Expect.ArtifactIDs)
			}

		})
	}
}
