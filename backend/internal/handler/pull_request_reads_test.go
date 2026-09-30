package handler

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/pull-request-visibility.yaml
var pullRequestVisibilityYAML []byte

// requiredPullRequestVisibilityCases is the name manifest for
// testdata/pull-request-visibility.yaml. Exact membership, never a count.
var requiredPullRequestVisibilityCases = []string{
	"public-repository-attached-is-listed-to-anyone",
	"public-repository-detached-is-listed-to-anyone",
	"private-repository-is-omitted-for-an-anonymous-reader",
	"private-repository-attached-is-listed-to-its-author",
	"private-repository-detached-is-listed-to-its-author",
	"private-repository-attached-is-listed-to-a-collective-member",
	"private-repository-detached-is-listed-to-a-collective-member",
	"private-repository-is-omitted-for-a-repository-reader-outside-the-collective",
	"private-repository-is-omitted-for-a-signed-in-stranger",
}

type pullRequestVisibilityCase struct {
	Name    string `yaml:"name"`
	Viewer  string `yaml:"viewer"`
	Private bool   `yaml:"private"`
	State   string `yaml:"state"`
	Listed  bool   `yaml:"listed"`
}

func loadPullRequestVisibilityCases(t *testing.T) []pullRequestVisibilityCase {
	t.Helper()
	file, err := decodeFixtureDocument[struct {
		Cases []pullRequestVisibilityCase `yaml:"cases"`
	}](pullRequestVisibilityYAML)
	if err != nil {
		t.Fatalf("load the pull request visibility fixture: %v", err)
	}
	present := map[string]bool{}
	for _, c := range file.Cases {
		if present[c.Name] {
			t.Fatalf("the pull request visibility fixture repeats %q", c.Name)
		}
		switch c.Viewer {
		case "anonymous", "author", "member", "reader", "stranger":
		default:
			t.Fatalf("case %q names viewer %q, which is not one the fixture defines", c.Name, c.Viewer)
		}
		if c.State != "attached" && c.State != "detached" {
			t.Fatalf("case %q uses state %q; the read lists only attached and detached attachments, and the database proof covers the rest", c.Name, c.State)
		}
		present[c.Name] = true
	}
	assertExactCaseNames(t, "pull-request-visibility", present, requiredPullRequestVisibilityCases)
	return file.Cases
}

// assertExactCaseNames holds a fixture to exact membership against its
// manifest in both directions, so a removed case is named and an added one
// cannot slip in unprotected.
func assertExactCaseNames(t *testing.T, fixture string, present map[string]bool, required []string) {
	t.Helper()
	declared := map[string]bool{}
	for _, name := range required {
		declared[name] = true
	}
	var missing, undeclared []string
	for name := range declared {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	for name := range present {
		if !declared[name] {
			undeclared = append(undeclared, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(undeclared)
	if len(missing) > 0 {
		t.Fatalf("testdata/%s.yaml no longer carries %v, which its manifest declares; restore the case rather than deleting the name", fixture, missing)
	}
	if len(undeclared) > 0 {
		t.Fatalf("testdata/%s.yaml carries %v, which its manifest does not declare; add each new name in the same change", fixture, undeclared)
	}
}

// TestTranscriptPullRequestsApplyTheVisibilityRule drives the production read
// with one candidate per case and asserts whether it is listed, and that GitHub
// was never asked what the viewer may read.
func TestTranscriptPullRequestsApplyTheVisibilityRule(t *testing.T) {
	for _, c := range loadPullRequestVisibilityCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			author := uuid.New()
			viewer := uuid.New()
			transcriptID := uuid.New()
			attachmentID := uuid.New()
			fake := newPullReadsGitHub(t)
			fake.setPull("acme/app", 42, "Tighten the ingest retry", "fix/ingest-retry")
			fake.setReader("4242", "reading-octocat", "acme/app", map[string]string{"reader": "read", "stranger": "none"}[c.Viewer])

			var user *AuthUser
			if c.Viewer != "anonymous" {
				id := viewer
				if c.Viewer == "author" {
					id = author
				}
				user = &AuthUser{ID: id, Username: "viewer"}
			}
			q := &mockQuerier{
				getTranscriptByID: func(_ context.Context, id pgtype.UUID) (sqlc.Transcript, error) {
					return sqlc.Transcript{ID: id, OwnerID: toPgUUID(author), Visibility: dbVisibilityPublic}, nil
				},
				getUserByID: func(_ context.Context, id pgtype.UUID) (sqlc.User, error) {
					return sqlc.User{ID: id, Provider: githubProvider, ProviderUserID: "4242"}, nil
				},
				listPullRequestCandidatesByTranscripts: func(_ context.Context, arg sqlc.ListPullRequestCandidatesByTranscriptsParams) ([]sqlc.ListPullRequestCandidatesByTranscriptsRow, error) {
					if len(arg.TranscriptIds) != 1 || arg.TranscriptIds[0] != toPgUUID(transcriptID) {
						t.Fatalf("the candidate read asked about %v, want the requested transcript", arg.TranscriptIds)
					}
					if strings.Join(arg.States, ",") != "attached,detached" {
						t.Fatalf("the candidate read asked for states %v; the transcript read lists attached and detached only", arg.States)
					}
					if (user == nil) == arg.ViewerID.Valid {
						t.Fatalf("the candidate read carried viewer %+v for viewer %q", arg.ViewerID, c.Viewer)
					}
					return []sqlc.ListPullRequestCandidatesByTranscriptsRow{{
						TranscriptID: toPgUUID(transcriptID),
						PullRequestAttachment: sqlc.PullRequestAttachment{
							ID: toPgUUID(attachmentID), RepoOwner: "acme", RepoName: "app", Number: 42,
							HeadSha: "headsha", State: c.State, AuthorID: toPgUUID(author),
							CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}, UpdatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
						},
						InstallationID: 4242,
						IsPrivate:      c.Private,
						ViewerIsMember: c.Viewer == "member",
					}}, nil
				},
			}
			h := newTestHandler(q, nil)
			h.gh = fake.client(t)

			r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+transcriptID.String()+"/pulls", nil)
			r = withChiURLParam(r, "id", transcriptID.String())
			if user != nil {
				r = r.WithContext(context.WithValue(r.Context(), UserContextKey, user))
			}
			w := httptest.NewRecorder()
			h.ListTranscriptPullRequests(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d (%s), want 200", w.Code, w.Body.String())
			}
			var got schema.VillageTranscriptPullRequestsResponse
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode the response: %v", err)
			}
			if listed := len(got.PullRequests) == 1; listed != c.Listed {
				t.Fatalf("listed = %v (%s), want %v", listed, w.Body.String(), c.Listed)
			}
			if c.Listed {
				row := got.PullRequests[0]
				if row.Title == nil || *row.Title != "Tighten the ingest retry" || row.HeadRef == nil || *row.HeadRef != "fix/ingest-retry" {
					t.Fatalf("title/head_ref = %v/%v, want GitHub's values", row.Title, row.HeadRef)
				}
				if row.IsPrivateRepository != c.Private || string(row.State) != c.State {
					t.Fatalf("row = %+v, want private=%v state=%s", row, c.Private, c.State)
				}
			}
			if _, asks := fake.counts(); asks != 0 {
				t.Fatalf("GitHub was asked %d permission question(s); the pull request reads never ask what a viewer may read", asks)
			}
		})
	}
}
