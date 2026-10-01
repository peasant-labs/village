//go:build integration

package handler

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/github"
	"github.com/peasant-labs/village/backend/internal/promptattach"
)

//go:embed testdata/github_webhook/auto-attach.yaml
var autoAttachYAML []byte

type autoAttachCase struct {
	Name                string `yaml:"name"`
	Author              string `yaml:"author"`
	Member              bool   `yaml:"member"`
	Linked              bool   `yaml:"linked"`
	Fork                bool   `yaml:"fork"`
	TranscriptMatches   bool   `yaml:"transcript_matches"`
	Deliveries          int    `yaml:"deliveries"`
	ThenPublishesAMatch bool   `yaml:"then_publishes_a_match"`
	StartsAs            string `yaml:"starts_as"`
	Expect              struct {
		State          string `yaml:"state"`
		CommentsPosted int    `yaml:"comments_posted"`
		CommentEdits   int    `yaml:"comment_edits"`
	} `yaml:"expect"`
}

// requiredAutoAttachCases names every row: the author's choice, their account
// and membership, the repository's link, a fork, a redelivery, and a pull
// request that waits for the publish that completes it.
var requiredAutoAttachCases = []string{
	"opted_in_author_is_attached_without_a_preview",
	"opted_out_author_gets_no_attachment",
	"unknown_author_gets_no_attachment",
	"unlinked_repository_gets_no_attachment",
	"author_outside_the_linking_collective_gets_no_attachment",
	"fork_pull_request_is_attached_from_its_head_repository",
	"redelivery_posts_no_second_comment",
	"nothing_matching_yet_waits_for_a_publish",
	"a_waiting_pull_request_is_completed_by_a_later_publish",
	"a_pull_request_the_author_previewed_is_left_alone",
	"a_pull_request_the_author_detached_is_left_alone",
}

func loadAutoAttachCases(t *testing.T) []autoAttachCase {
	t.Helper()
	cases, err := decodeFixtureRows[autoAttachCase](autoAttachYAML)
	if err != nil {
		t.Fatalf("load testdata/github_webhook/auto-attach.yaml: %v", err)
	}
	present := map[string]struct{}{}
	for _, c := range cases {
		if _, repeated := present[c.Name]; c.Name == "" || repeated {
			t.Fatalf("auto-attach row name %q is empty or repeated", c.Name)
		}
		present[c.Name] = struct{}{}
		if !containsString([]string{"opted_in", "opted_out", "unknown"}, c.Author) {
			t.Fatalf("row %q: author %q is not opted_in, opted_out, or unknown", c.Name, c.Author)
		}
		if !containsString([]string{"attached", "waiting", "preview", "detached", "none"}, c.Expect.State) {
			t.Fatalf("row %q: state %q is not attached, waiting, preview, detached, or none", c.Name, c.Expect.State)
		}
		if c.StartsAs != "" && c.StartsAs != "preview" && c.StartsAs != "detached" {
			t.Fatalf("row %q: starts_as %q is neither preview nor detached", c.Name, c.StartsAs)
		}
		if c.Deliveries < 1 {
			t.Fatalf("row %q: deliveries %d, want at least one", c.Name, c.Deliveries)
		}
	}
	assertExactTitleFixtureNames(t, "auto-attach", present, requiredAutoAttachCases)
	return cases
}

// openedPullRequestEvent is a pull_request "opened" payload. From a fork, its
// head repository is the author's fork of the base repository.
func openedPullRequestEvent(repoName string, number int, authorID int64, headSHA string, fork bool) string {
	headID, headOwner, headName := 4242, "acme", repoName
	if fork {
		headID, headOwner, headName = 55, "author", "fork-of-"+repoName
	}
	return fmt.Sprintf(`{
		"action": "opened",
		"number": %d,
		"pull_request": {
			"number": %d,
			"user": {"id": %d, "login": "author"},
			"head": {"ref": "feat/x", "sha": %q, "repo": {"id": %d, "name": %q, "full_name": "%s/%s", "owner": {"id": 1001, "login": %q}}},
			"base": {"ref": "main", "sha": "base", "repo": {"id": 4242, "name": %q, "full_name": "acme/%s", "owner": {"id": 9, "login": "acme"}}},
			"merged": false
		},
		"repository": {"id": 4242, "name": %q, "owner": {"id": 9, "login": "acme"}},
		"sender": {"id": %d, "login": "author"},
		"installation": {"id": 4242}
	}`, number, number, authorID, headSHA, headID, headName, headOwner, headName, headOwner, repoName, repoName, repoName, authorID)
}

// TestOpenedPullRequestFollowsTheAuthorsAutoAttachChoice_RealPostgres drives
// every row of testdata/github_webhook/auto-attach.yaml through the production
// dispatcher over real PostgreSQL, with the fake GitHub standing in for the
// pull request's commits and the posting.
func TestOpenedPullRequestFollowsTheAuthorsAutoAttachChoice_RealPostgres(t *testing.T) {
	for i, c := range loadAutoAttachCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			h, pool, blobs, fake := attachmentTestHandler(t)
			h.githubDispatcher = promptCommandDispatcher{h: h}
			ctx := context.Background()
			authorGitHubID := 996400 + int64(i)*10
			owner := attachmentInsertOwner(t, ctx, pool, authorGitHubID)
			defer cleanupOwners(t, ctx, pool, owner)

			repoName := "auto-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
			sha := fmt.Sprintf("d%039d", authorGitHubID)
			var groupID pgtype.UUID
			if c.Linked {
				groupID = attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")
				if c.Member {
					if _, err := pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, 'owner')`, groupID, owner); err != nil {
						t.Fatalf("make the author a member of the linking collective: %v", err)
					}
				}
			}
			if c.Author == "opted_in" {
				if _, err := pool.Exec(ctx, `UPDATE users SET auto_attach_pull_requests = true WHERE id = $1`, owner); err != nil {
					t.Fatalf("opt the author in: %v", err)
				}
			}
			prAuthor := authorGitHubID
			if c.Author == "unknown" {
				// Somebody no Village account belongs to opens the pull request.
				prAuthor = authorGitHubID + 1
			}

			remote := "git@github.com:acme/" + repoName + ".git"
			if c.Fork {
				remote = "git@github.com:author/fork-of-" + repoName + ".git"
			}
			recorded := sha
			if !c.TranscriptMatches {
				recorded = fmt.Sprintf("e%039d", authorGitHubID)
			}
			transcriptID := attachmentSeedTranscript(t, ctx, pool, blobs, owner, remote, recorded, "private", "feat/x", time.Now().Add(-time.Hour))
			fake.setPullCommits(sha)
			switch c.StartsAs {
			case "preview":
				attachmentCreatePreview(t, ctx, h, groupID, owner, "acme", repoName, sha, 7)
			case "detached":
				created := attachmentCreatePreview(t, ctx, h, groupID, owner, "acme", repoName, sha, 7)
				if _, err := promptattach.Transition(ctx, h.queries, created.ID, promptattach.Detached); err != nil {
					t.Fatalf("detach the attachment the author made: %v", err)
				}
			}

			for delivery := 0; delivery < c.Deliveries; delivery++ {
				if err := dispatchEvent(t, h, "pull_request", openedPullRequestEvent(repoName, 7, prAuthor, sha, c.Fork)); err != nil {
					t.Fatalf("dispatch delivery %d: %v", delivery+1, err)
				}
			}
			if c.ThenPublishesAMatch {
				if code, body := attachmentPublish(t, h, owner, "attachment-owner", remote, sha); code != http.StatusCreated {
					t.Fatalf("publish a matching session: status = %d (%s), want 201", code, body)
				}
			}

			state := "none"
			attachment, err := h.queries.GetPullRequestAttachmentForPull(ctx, sqlc.GetPullRequestAttachmentForPullParams{Lower: "acme", Lower_2: strings.ToLower(repoName), Number: 7})
			switch {
			case err == nil:
				state = attachment.State
			case !errors.Is(err, pgx.ErrNoRows):
				t.Fatalf("read the pull request's attachment: %v", err)
			}
			if state != c.Expect.State {
				t.Fatalf("attachment state = %q, want %q", state, c.Expect.State)
			}
			fake.mu.Lock()
			comments, edits := fake.commentCreates, fake.commentEdits
			fake.mu.Unlock()
			if comments != c.Expect.CommentsPosted || edits != c.Expect.CommentEdits {
				t.Fatalf("sticky comments created = %d and edited = %d, want %d and %d", comments, edits, c.Expect.CommentsPosted, c.Expect.CommentEdits)
			}

			if visibility := readTranscriptVisibility(t, ctx, pool, transcriptID); visibility != "private" {
				t.Errorf("visibility = %q, want private: linking never changes who can read a transcript", visibility)
			}
			var attempts int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcript_share_attempts WHERE transcript_id = $1`, transcriptID).Scan(&attempts); err != nil {
				t.Fatal(err)
			}
			if attempts != 0 {
				t.Errorf("linking appended %d share attempts, want none", attempts)
			}
			if state == "attached" {
				bindings, err := h.queries.ListPullRequestAttachmentTranscripts(ctx, attachment.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, binding := range bindings {
					if binding.AttachWidened {
						t.Errorf("an automatic link made a binding marked widened: %+v", binding)
					}
				}
				if len(bindings) == 0 {
					t.Error("an attached pull request binds no transcript")
				}
			}
		})
	}
}

// TestAutoAttachSurvivesTheWebhookGivingUp_RealPostgres covers a delivery whose
// request is cancelled while the comment is being posted, which is what GitHub
// giving up on a slow delivery does. The attach must still record what it
// posted, and the redelivery that follows must not post a second comment.
func TestAutoAttachSurvivesTheWebhookGivingUp_RealPostgres(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	h.githubDispatcher = promptCommandDispatcher{h: h}
	ctx := context.Background()
	authorGitHubID := int64(996491)
	owner := attachmentInsertOwner(t, ctx, pool, authorGitHubID)
	defer cleanupOwners(t, ctx, pool, owner)

	repoName := "auto-cancel-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	sha := fmt.Sprintf("d%039d", authorGitHubID)
	groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, true, "informational")
	if _, err := pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, 'owner')`, groupID, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET auto_attach_pull_requests = true WHERE id = $1`, owner); err != nil {
		t.Fatal(err)
	}
	attachmentSeedTranscript(t, ctx, pool, blobs, owner, "git@github.com:acme/"+repoName+".git", sha, "private", "feat/x", time.Now().Add(-time.Hour))
	fake.setPullCommits(sha)

	requestCtx, giveUp := context.WithCancel(ctx)
	defer giveUp()
	fake.mu.Lock()
	fake.beforeCommentWrite = giveUp
	fake.mu.Unlock()
	payload := openedPullRequestEvent(repoName, 7, authorGitHubID, sha, false)
	if err := github.Dispatch(requestCtx, h.githubDispatcher, github.Event{Type: "pull_request", DeliveryID: uuid.NewString(), Payload: []byte(payload)}); err != nil {
		t.Fatalf("the delivery whose request was cancelled mid-post: %v", err)
	}
	if requestCtx.Err() == nil {
		t.Fatal("the request was never cancelled, so the case this test describes never happened")
	}

	attachment, err := h.queries.GetPullRequestAttachmentForPull(ctx, sqlc.GetPullRequestAttachmentForPullParams{Lower: "acme", Lower_2: strings.ToLower(repoName), Number: 7})
	if err != nil {
		t.Fatalf("read the attachment: %v", err)
	}
	if attachment.State != "attached" || !attachment.CommentID.Valid {
		t.Fatalf("attachment = %s with comment recorded %t, want attached with its comment recorded", attachment.State, attachment.CommentID.Valid)
	}
	if err := dispatchEvent(t, h, "pull_request", payload); err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	fake.mu.Lock()
	creates := fake.commentCreates
	fake.mu.Unlock()
	if creates != 1 {
		t.Fatalf("sticky comments created = %d after the redelivery, want 1", creates)
	}
}
