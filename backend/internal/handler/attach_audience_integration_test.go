//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/attach-audience.yaml
var attachAudienceYAML []byte

type attachAudienceReaders struct {
	Owner            string `yaml:"owner"`
	Member           string `yaml:"member"`
	RepositoryReader string `yaml:"repository_reader"`
	Anonymous        string `yaml:"anonymous"`
}

type attachAudienceStatuses struct {
	Owner            int `yaml:"owner"`
	Member           int `yaml:"member"`
	RepositoryReader int `yaml:"repository_reader"`
	Anonymous        int `yaml:"anonymous"`
}

type attachAudienceCase struct {
	Name          string `yaml:"name"`
	Repository    string `yaml:"repository"`
	Transcript    string `yaml:"transcript"`
	OwnerThenSets string `yaml:"owner_then_sets"`
	Expect        struct {
		Visibility          string                 `yaml:"visibility"`
		LinkingShareLive    bool                   `yaml:"linking_share_live"`
		Reads               attachAudienceStatuses `yaml:"reads"`
		Page                attachAudienceReaders  `yaml:"page"`
		ListedOnPullRequest bool                   `yaml:"listed_on_pull_request"`
		Check               string                 `yaml:"check"`
	} `yaml:"expect"`
}

// requiredAttachAudienceCases names every row: both repository kinds crossed
// with the three visibilities, and a new binding's detach keeping the owner's
// later choice in each direction.
var requiredAttachAudienceCases = []string{
	"public_repository_private_transcript",
	"public_repository_shared_transcript",
	"public_repository_public_transcript",
	"private_repository_private_transcript",
	"private_repository_shared_transcript",
	"private_repository_public_transcript",
	"a_new_binding_keeps_the_owners_later_widening",
	"a_new_binding_keeps_the_owners_later_narrowing",
}

var attachAudiencePageOutcomes = []string{"prompts", "no_prompts", "not_found"}

func loadAttachAudienceCases(t *testing.T) []attachAudienceCase {
	t.Helper()
	cases, err := decodeFixtureRows[attachAudienceCase](attachAudienceYAML)
	if err != nil {
		t.Fatalf("load testdata/attach-audience.yaml: %v", err)
	}
	present := map[string]struct{}{}
	for _, c := range cases {
		if _, repeated := present[c.Name]; c.Name == "" || repeated {
			t.Fatalf("attach-audience row name %q is empty or repeated", c.Name)
		}
		present[c.Name] = struct{}{}
		if c.Repository != "public" && c.Repository != "private" {
			t.Fatalf("row %q: repository %q is neither public nor private", c.Name, c.Repository)
		}
		for _, visibility := range []string{c.Transcript, c.Expect.Visibility} {
			if !containsString(shareAttemptVisibilities, visibility) {
				t.Fatalf("row %q: visibility %q is not one of %v", c.Name, visibility, shareAttemptVisibilities)
			}
		}
		if c.OwnerThenSets != "" && !containsString(shareAttemptVisibilities, c.OwnerThenSets) {
			t.Fatalf("row %q: owner_then_sets %q is not one of %v", c.Name, c.OwnerThenSets, shareAttemptVisibilities)
		}
		for _, outcome := range []string{c.Expect.Page.Owner, c.Expect.Page.Member, c.Expect.Page.RepositoryReader, c.Expect.Page.Anonymous} {
			if !containsString(attachAudiencePageOutcomes, outcome) {
				t.Fatalf("row %q: page outcome %q is not one of %v", c.Name, outcome, attachAudiencePageOutcomes)
			}
		}
		if c.Expect.Check != "success" && c.Expect.Check != "neutral" {
			t.Fatalf("row %q: check %q is neither success nor neutral", c.Name, c.Expect.Check)
		}
	}
	assertExactTitleFixtureNames(t, "attach-audience", present, requiredAttachAudienceCases)
	return cases
}

// governanceEventCount counts every governance audit row one transcript has, so
// a test can prove an action appended none.
func governanceEventCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, transcriptID pgtype.UUID) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM transcript_governance_events_audit WHERE transcript_id = $1`, transcriptID).Scan(&count); err != nil {
		t.Fatalf("count the transcript's governance events: %v", err)
	}
	return count
}

// latestShareStatus is the status of the latest attempt for one pair, or "" when
// the pair has none.
func latestShareStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, transcriptID, groupID pgtype.UUID) string {
	t.Helper()
	var status string
	err := pool.QueryRow(ctx, `
		SELECT status FROM transcript_share_attempts WHERE transcript_id = $1 AND group_id = $2
		ORDER BY event_num DESC LIMIT 1`, transcriptID, groupID).Scan(&status)
	if err != nil && !strings.Contains(err.Error(), "no rows") {
		t.Fatalf("read the latest share attempt: %v", err)
	}
	return status
}

// attachAudienceWorld is one row's repository, collective, transcript, and
// readers.
type attachAudienceWorld struct {
	h            *Handler
	pool         *pgxpool.Pool
	fake         *attachmentGitHubFake
	repoName     string
	number       int
	groupID      pgtype.UUID
	transcriptID pgtype.UUID
	title        string
	owner        *AuthUser
	member       *AuthUser
	reader       *AuthUser
}

func newAttachAudienceWorld(t *testing.T, c attachAudienceCase, githubBase int64) *attachAudienceWorld {
	t.Helper()
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, githubBase)
	member := attachmentInsertOwner(t, ctx, pool, githubBase+1)
	reader := attachmentInsertOwner(t, ctx, pool, githubBase+2)
	t.Cleanup(func() { cleanupOwners(t, context.Background(), pool, owner, member, reader) })

	w := &attachAudienceWorld{
		h: h, pool: pool, fake: fake, number: 7,
		repoName: "audience-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8],
		title:    "audience title " + uuid.NewString()[:8],
		owner:    &AuthUser{ID: uuid.UUID(owner.Bytes), Username: "audience-owner"},
		member:   &AuthUser{ID: uuid.UUID(member.Bytes), Username: "audience-member"},
		reader:   &AuthUser{ID: uuid.UUID(reader.Bytes), Username: "audience-reader"},
	}
	w.groupID = attachmentLinkCollective(t, ctx, pool, owner, "acme", w.repoName, c.Repository == "private", "informational")
	if _, err := pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, 'member')`, w.groupID, member); err != nil {
		t.Fatalf("add the collective member: %v", err)
	}
	// On a private repository GitHub admits the reader; on a public one anybody
	// may read it, so GitHub is never asked.
	fake.setRepoReader(fmt.Sprintf("%d", githubBase+2), "audience-reader", "read")

	sha := fmt.Sprintf("a%039d", githubBase)
	w.transcriptID = attachmentSeedTranscript(t, ctx, pool, blobs, owner,
		"git@github.com:acme/"+w.repoName+".git", sha, c.Transcript, "", time.Now().Add(-time.Hour))
	if _, err := pool.Exec(ctx, `UPDATE transcripts SET title = $2 WHERE id = $1`, w.transcriptID, w.title); err != nil {
		t.Fatalf("title the transcript: %v", err)
	}
	if c.Transcript == dbVisibilityShared {
		if _, err := pool.Exec(ctx, `
			INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status)
			VALUES ($1, $2, 1, 'approved')`, w.transcriptID, w.groupID); err != nil {
			t.Fatalf("share the transcript with the linking collective: %v", err)
		}
	}

	preview := attachmentCreatePreview(t, ctx, h, w.groupID, owner, "acme", w.repoName, sha, w.number)
	fake.setPullCommits(sha)
	// The author's click computes and stores the preview digest, as production
	// does, so the preview page has something to show its author.
	repo, err := h.resolveAttachmentRepository(ctx, h.queries, preview)
	if err != nil {
		t.Fatalf("resolve the attachment's repository: %v", err)
	}
	if err := h.authorAttachOrPreview(ctx, preview, repo, sha, false); err != nil {
		t.Fatalf("compute the preview: %v", err)
	}
	return w
}

func (w *attachAudienceWorld) pageOutcome(t *testing.T, viewer *AuthUser) string {
	t.Helper()
	rec := pageAs(t, w.h, viewer, "acme", w.repoName, w.number)
	switch rec.Code {
	case http.StatusNotFound:
		return "not_found"
	case http.StatusOK:
	default:
		t.Fatalf("page status = %d (%s), want 200 or 404", rec.Code, rec.Body.String())
	}
	var response schema.VillagePullRequestAttachmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode the page: %v", err)
	}
	body := rec.Body.String()
	hasPrompt := strings.Contains(body, "please attach my prompts")
	hasTitle := strings.Contains(body, w.title)
	if hasPrompt != hasTitle {
		t.Fatalf("the page shows the prompt (%t) and the title (%t) to the same viewer differently; both follow who can read the transcript: %s", hasPrompt, hasTitle, body)
	}
	if response.Attachment.State == schema.VillagePullRequestAttachmentState("attached") && len(response.Transcripts) != 1 {
		t.Fatalf("the page lists %d transcripts, want the one bound, readable or not", len(response.Transcripts))
	}
	for _, transcript := range response.Transcripts {
		if (transcript.SessionStart != nil) != hasPrompt {
			t.Fatalf("session start admission differs from prompt admission: %s", body)
		}
	}
	if hasPrompt {
		return "prompts"
	}
	return "no_prompts"
}

// TestAttachingNeverChangesWhoCanRead_RealPostgres drives every row of
// testdata/attach-audience.yaml through the mounted routes over real PostgreSQL:
// the author's confirm, the transcript read, the pull request page, the owner's
// PATCH, and the author's detach. What each reader can read is the transcript's
// own audience before, during, and after the attachment.
func TestAttachingNeverChangesWhoCanRead_RealPostgres(t *testing.T) {
	for i, c := range loadAttachAudienceCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			ctx := context.Background()
			w := newAttachAudienceWorld(t, c, 996100+int64(i)*10)

			// The preview is the author's own review step: they see their prompts,
			// and nobody else's page carries them.
			if rec := pageAs(t, w.h, w.owner, "acme", w.repoName, w.number); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "please attach my prompts") {
				t.Fatalf("the author's own preview: status %d, want 200 with their prompt: %s", rec.Code, rec.Body.String())
			}
			for _, viewer := range []*AuthUser{w.member, w.reader, nil} {
				if rec := pageAs(t, w.h, viewer, "acme", w.repoName, w.number); strings.Contains(rec.Body.String(), "please attach my prompts") {
					t.Fatalf("a preview's prompts reached a viewer who is not its author: %s", rec.Body.String())
				}
			}

			eventsBefore := governanceEventCount(t, ctx, w.pool, w.transcriptID)
			shareBefore := latestShareStatus(t, ctx, w.pool, w.transcriptID, w.groupID)
			attachmentConfirm(t, w.h, pgtype.UUID{Bytes: w.owner.ID, Valid: true}, "acme", w.repoName, w.number)

			if events := governanceEventCount(t, ctx, w.pool, w.transcriptID); events != eventsBefore {
				t.Fatalf("attaching appended %d governance events; attaching never changes who can read a transcript", events-eventsBefore)
			}
			if share := latestShareStatus(t, ctx, w.pool, w.transcriptID, w.groupID); share != shareBefore {
				t.Fatalf("attaching moved the linking collective's share from %q to %q; attaching opens no share", shareBefore, share)
			}
			binding, err := w.h.queries.GetPullRequestAttachmentTranscript(ctx, sqlc.GetPullRequestAttachmentTranscriptParams{
				AttachmentID: w.attachmentID(t), TranscriptID: w.transcriptID,
			})
			if err != nil {
				t.Fatalf("read the binding: %v", err)
			}
			if binding.AttachWidened || binding.PreviousVisibility != c.Transcript {
				t.Fatalf("binding = widened %t at %q, want not widened at the transcript's own %q", binding.AttachWidened, binding.PreviousVisibility, c.Transcript)
			}

			if c.OwnerThenSets != "" {
				if rec := transcriptVisibilityPatch(t, w.h, w.owner, w.transcriptID, c.OwnerThenSets); rec.Code != http.StatusOK {
					t.Fatalf("the owner's PATCH to %s: status = %d (%s), want 200", c.OwnerThenSets, rec.Code, rec.Body.String())
				}
			}

			if visibility := readTranscriptVisibility(t, ctx, w.pool, w.transcriptID); visibility != c.Expect.Visibility {
				t.Fatalf("visibility = %q, want %q", visibility, c.Expect.Visibility)
			}
			if live := shareAttemptIsLive(latestShareStatus(t, ctx, w.pool, w.transcriptID, w.groupID)); live != c.Expect.LinkingShareLive {
				t.Fatalf("the linking collective's share is live = %t, want %t", live, c.Expect.LinkingShareLive)
			}

			for _, read := range []struct {
				who    string
				viewer *AuthUser
				want   int
				page   string
			}{
				{"owner", w.owner, c.Expect.Reads.Owner, c.Expect.Page.Owner},
				{"member", w.member, c.Expect.Reads.Member, c.Expect.Page.Member},
				{"repository_reader", w.reader, c.Expect.Reads.RepositoryReader, c.Expect.Page.RepositoryReader},
				{"anonymous", nil, c.Expect.Reads.Anonymous, c.Expect.Page.Anonymous},
			} {
				if rec := transcriptViewAs(t, w.h, read.viewer, w.transcriptID); rec.Code != read.want {
					t.Errorf("%s reading the transcript: status = %d, want %d", read.who, rec.Code, read.want)
				}
				if got := w.pageOutcome(t, read.viewer); got != read.page {
					t.Errorf("%s on the pull request page sees %s, want %s", read.who, got, read.page)
				}
			}

			w.fake.mu.Lock()
			comment, summary, conclusion := w.fake.lastCommentBody, w.fake.lastCheckText, w.fake.lastCheckConclusion
			w.fake.mu.Unlock()
			for surface, text := range map[string]string{"comment": comment, "check": summary} {
				if listed := strings.Contains(text, "please attach my prompts"); listed != c.Expect.ListedOnPullRequest {
					t.Errorf("the pull request's %s carries the prompt = %t, want %t: %s", surface, listed, c.Expect.ListedOnPullRequest, text)
				}
				if strings.Contains(text, w.title) {
					t.Errorf("the pull request's %s carries the transcript's title: %s", surface, text)
				}
				if unlisted := strings.Contains(text, "1 attached transcript is not listed here."); unlisted == c.Expect.ListedOnPullRequest {
					t.Errorf("the pull request's %s says a transcript is not listed = %t, want %t: %s", surface, unlisted, !c.Expect.ListedOnPullRequest, text)
				}
			}
			if conclusion != c.Expect.Check {
				t.Errorf("check conclusion = %q, want %q", conclusion, c.Expect.Check)
			}

			// Detaching changes nothing a new binding did not change.
			eventsBefore = governanceEventCount(t, ctx, w.pool, w.transcriptID)
			shareBefore = latestShareStatus(t, ctx, w.pool, w.transcriptID, w.groupID)
			if rec := attachmentServe(t, attachmentRouter(w.h), http.MethodDelete, fmt.Sprintf("/api/v1/pulls/acme/%s/%d", w.repoName, w.number), pgtype.UUID{Bytes: w.owner.ID, Valid: true}); rec.Code != http.StatusOK {
				t.Fatalf("detach status = %d (%s), want 200", rec.Code, rec.Body.String())
			}
			if visibility := readTranscriptVisibility(t, ctx, w.pool, w.transcriptID); visibility != c.Expect.Visibility {
				t.Errorf("visibility = %q after the detach, want %q: a binding that changed nothing changes nothing when it goes", visibility, c.Expect.Visibility)
			}
			if share := latestShareStatus(t, ctx, w.pool, w.transcriptID, w.groupID); share != shareBefore {
				t.Errorf("the detach moved the linking collective's share from %q to %q", shareBefore, share)
			}
			if events := governanceEventCount(t, ctx, w.pool, w.transcriptID); events != eventsBefore {
				t.Errorf("the detach appended %d governance events", events-eventsBefore)
			}
			kept, err := w.h.queries.GetPullRequestAttachmentTranscript(ctx, sqlc.GetPullRequestAttachmentTranscriptParams{
				AttachmentID: w.attachmentID(t), TranscriptID: w.transcriptID,
			})
			if err != nil {
				t.Fatalf("the detached pull request must keep its binding: %v", err)
			}
			if kept.AttachWidened {
				t.Error("a detached new binding reads as widened")
			}
			// A detached digest describes prompts that are no longer attached, so
			// only its author is still served it.
			for _, viewer := range []*AuthUser{w.member, w.reader, nil} {
				if rec := pageAs(t, w.h, viewer, "acme", w.repoName, w.number); strings.Contains(rec.Body.String(), "please attach my prompts") {
					t.Errorf("a detached pull request served its prompts to a viewer who is not its author: %s", rec.Body.String())
				}
			}
		})
	}
}

func (w *attachAudienceWorld) attachmentID(t *testing.T) pgtype.UUID {
	t.Helper()
	attachment, err := w.h.queries.GetPullRequestAttachmentForPull(context.Background(), sqlc.GetPullRequestAttachmentForPullParams{
		Lower: "acme", Lower_2: strings.ToLower(w.repoName), Number: int32(w.number),
	})
	if err != nil {
		t.Fatalf("read the attachment: %v", err)
	}
	return attachment.ID
}

// TestThePageShowsEachReaderOnlyWhatTheyCanRead_RealPostgres binds two
// transcripts with different audiences to one pull request, so a reader who can
// open one of them must not be handed the other's title or prompts through it.
func TestThePageShowsEachReaderOnlyWhatTheyCanRead_RealPostgres(t *testing.T) {
	h, pool, blobs, fake := attachmentTestHandler(t)
	ctx := context.Background()
	owner := attachmentInsertOwner(t, ctx, pool, 996191)
	defer cleanupOwners(t, ctx, pool, owner)
	ownerAuth := &AuthUser{ID: uuid.UUID(owner.Bytes), Username: "mixed-owner"}

	repoName := "mixed-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	groupID := attachmentLinkCollective(t, ctx, pool, owner, "acme", repoName, false, "informational")
	sha := "abc1234000000000000000000000000000000191"
	remote := "git@github.com:acme/" + repoName + ".git"
	seed := func(visibility, prompt, title string, startedAgo time.Duration) {
		t.Helper()
		content := bytes.Replace(attachmentPublicationContent(), []byte("please attach my prompts"), []byte(prompt), 1)
		id := attachmentSeedTranscriptWith(t, ctx, pool, blobs, owner, remote, sha, visibility, "", time.Now().Add(-startedAgo), content)
		if _, err := pool.Exec(ctx, `UPDATE transcripts SET title = $2 WHERE id = $1`, id, title); err != nil {
			t.Fatalf("title a transcript: %v", err)
		}
	}
	seed("public", "the public prompt", "the public title", 2*time.Hour)
	seed("private", "the private prompt", "the private title", time.Hour)
	attachmentCreatePreview(t, ctx, h, groupID, owner, "acme", repoName, sha, 7)
	fake.setPullCommits(sha)
	attachmentConfirm(t, h, owner, "acme", repoName, 7)

	for _, view := range []struct {
		who            string
		viewer         *AuthUser
		seesPrivateRow bool
	}{
		{"anonymous", nil, false},
		{"owner", ownerAuth, true},
	} {
		rec := pageAs(t, h, view.viewer, "acme", repoName, 7)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s page status = %d, want 200", view.who, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "the public prompt") || !strings.Contains(body, "the public title") {
			t.Errorf("%s does not see the public transcript's prompt and title: %s", view.who, body)
		}
		for _, needle := range []string{"the private prompt", "the private title"} {
			if got := strings.Contains(body, needle); got != view.seesPrivateRow {
				t.Errorf("%s sees %q = %t, want %t: %s", view.who, needle, got, view.seesPrivateRow, body)
			}
		}
		var response schema.VillagePullRequestAttachmentResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Transcripts) != 2 {
			t.Errorf("%s sees %d bound transcripts, want both, readable or not", view.who, len(response.Transcripts))
		}
	}
}
