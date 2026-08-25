//go:build integration

package handler

// The project page, driven through the REAL handler against a REAL PostgreSQL.
//
// It has to be a real database. Two of the answers this route gives - a hidden
// owner's 404 and a discoverable owner's empty 200 - depend on the interaction
// between the users table's is_discoverable flag and the transcript visibility
// predicate, and a mocked querier would return whatever the test told it to,
// proving nothing about either. The NOT NULL project identity the whole feature
// rests on is likewise a database constraint, asserted here for the same reason.
//
// See testdata/project-page-visibility.yaml for why each case is there.

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/projectname"
)

//go:embed testdata/project-page-visibility.yaml
var projectPageVisibilityYAML []byte

type projectPageCase struct {
	Name                 string `yaml:"name"`
	Why                  string `yaml:"why"`
	OwnerDiscoverable    bool   `yaml:"owner_discoverable"`
	Viewer               string `yaml:"viewer"`
	TranscriptVisibility string `yaml:"transcript_visibility"`
	OwnerOverrideName    string `yaml:"owner_override_name"`
	// PublishedProjectName overrides the name the publisher disclosed on the
	// transcript. It exists so a case can store Peasant's privacy-safe label
	// instead of a real name, which is what lets the remote tier be reached.
	PublishedProjectName string `yaml:"published_project_name"`
	WantStatus           int    `yaml:"want_status"`
	WantTranscriptCount  int    `yaml:"want_transcript_count"`
	WantDisplayName      string `yaml:"want_display_name"`
	WantNameSource       string `yaml:"want_name_source"`
	WantRemoteLabel      string `yaml:"want_remote_label"`
	// ShareIntoPublicCollective records the project's transcript as an APPROVED
	// contribution to a public collective, which is what makes the roll-up
	// non-empty. The share is written as an ATTEMPT; the current-state row is
	// derived by the database trigger, never by a fixture.
	ShareIntoPublicCollective     bool `yaml:"share_into_public_collective"`
	WantCollectiveCount           int  `yaml:"want_collective_count"`
	WantCollectiveTranscriptCount int  `yaml:"want_collective_transcript_count"`
}

// requiredProjectPageCases names the cases that must exist. The two
// all_transcripts_private cases are the ones that keep 404 (the owner is hidden)
// apart from 200-with-nothing-to-show (the owner is not hidden, the work is), and
// hidden_owner_viewed_by_owner is what stops the boundary from being implemented
// as a blanket refusal.
var requiredProjectPageCases = []string{
	"discoverable_owner_public_project_viewed_by_anonymous",
	"discoverable_owner_public_project_viewed_by_other_signed_in",
	"discoverable_owner_public_project_viewed_by_owner",
	"hidden_owner_viewed_by_anonymous",
	"hidden_owner_viewed_by_other_signed_in",
	"hidden_owner_viewed_by_owner",
	"discoverable_owner_all_transcripts_private_viewed_by_anonymous",
	"discoverable_owner_all_transcripts_private_viewed_by_other_signed_in",
	"owner_renamed_project_resolves_to_the_chosen_name",
	"project_with_no_owner_name_resolves_to_the_published_name",
	"project_with_only_a_privacy_label_resolves_to_the_repository",
	"approved_contribution_appears_in_the_collectives_rollup",
}

func loadProjectPageCases(t *testing.T) []projectPageCase {
	t.Helper()
	cases, err := decodeFixtureRows[projectPageCase](projectPageVisibilityYAML)
	if err != nil {
		t.Fatalf("load the project-page fixture: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if present[c.Name] {
			t.Fatalf("the project-page fixture repeats case %q", c.Name)
		}
		present[c.Name] = true
		if c.Why == "" {
			t.Fatalf("case %q states no reason it exists; a case nobody can justify cannot be maintained", c.Name)
		}
		switch c.Viewer {
		case "owner", "other", "anonymous":
		default:
			t.Fatalf("case %q is viewed by %q; the viewers are owner, other and anonymous", c.Name, c.Viewer)
		}
		switch c.TranscriptVisibility {
		case "public", "private":
		default:
			t.Fatalf("case %q stores a %q transcript; this fixture drives public and private", c.Name, c.TranscriptVisibility)
		}
	}
	for _, required := range requiredProjectPageCases {
		if !present[required] {
			t.Fatalf("the project-page fixture no longer contains %q. That case exists because losing it hides a real "+
				"failure; restore it rather than removing it from this manifest.", required)
		}
	}
	return cases
}

// projectPageWorld is one case's owner, viewer and project.
type projectPageWorld struct {
	pool         *pgxpool.Pool
	owner        pgtype.UUID
	ownerName    string
	other        pgtype.UUID
	projectHash  string
	transcript   pgtype.UUID
	collective   pgtype.UUID
	collectiveOn bool
}

// publishedProjectName is the name the publisher disclosed on the transcript. It
// is deliberately NOT a privacy label, so the resolver classifies it as consented.
const publishedProjectName = "ledger-service"

func newProjectPageWorld(t *testing.T, ctx context.Context, pool *pgxpool.Pool, testCase projectPageCase) *projectPageWorld {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	w := &projectPageWorld{
		pool:        pool,
		ownerName:   "project-owner-" + suffix,
		projectHash: strings.Repeat("a", 52) + suffix,
	}
	w.owner = shareInsertUser(t, ctx, pool, w.ownerName)
	w.other = shareInsertUser(t, ctx, pool, "project-other-"+suffix)

	if _, err := pool.Exec(ctx, "UPDATE users SET is_discoverable = $2 WHERE id = $1",
		w.owner, testCase.OwnerDiscoverable); err != nil {
		t.Fatalf("set the owner's discoverability: %v", err)
	}

	w.transcript = projectPageInsertTranscript(t, ctx, pool, w.owner, "page-"+suffix, w.projectHash,
		testCase.TranscriptVisibility, testCase.publishedName())

	if testCase.ShareIntoPublicCollective {
		w.collectiveOn = true
		if err := pool.QueryRow(ctx, `
			INSERT INTO groups (name, created_by, acceptance_mode, data_access)
			VALUES ($1, $2, 'open', 'public') RETURNING id
		`, "project-collective-"+suffix, w.owner).Scan(&w.collective); err != nil {
			t.Fatalf("create the collective: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, 'owner')
		`, w.collective, w.owner); err != nil {
			t.Fatalf("add the collective owner: %v", err)
		}
		// The current-state share row is written by the trigger on this insert.
		// A fixture that wrote transcript_shares directly would prove nothing
		// about the shipped mechanism, and the fail-closed guard refuses it.
		if _, err := pool.Exec(ctx, `
			INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status, decided_at, decided_by)
			VALUES ($1, $2, 1, 'approved', now(), $3)
		`, w.transcript, w.collective, w.owner); err != nil {
			t.Fatalf("record the approved contribution: %v", err)
		}
	}

	if testCase.OwnerOverrideName != "" {
		if _, err := pool.Exec(ctx, `
			INSERT INTO owner_overrides (owner_id, target_kind, target_key, field, value)
			VALUES ($1, 'project', $2, 'display_name', $3)
		`, w.owner, w.projectHash, testCase.OwnerOverrideName); err != nil {
			t.Fatalf("store the owner's chosen project name: %v", err)
		}
	}
	return w
}

func (w *projectPageWorld) cleanup(t *testing.T, ctx context.Context) {
	t.Helper()
	if _, err := w.pool.Exec(ctx, "DELETE FROM owner_overrides WHERE owner_id = $1", w.owner); err != nil {
		t.Errorf("cleanup owner corrections: %v", err)
	}
	if w.collectiveOn {
		if _, err := w.pool.Exec(ctx, "DELETE FROM groups WHERE id = $1", w.collective); err != nil {
			t.Errorf("cleanup collective: %v", err)
		}
	}
	cleanupOwners(t, ctx, w.pool, w.owner, w.other)
}

// projectPageInsertTranscript stores one transcript carrying a project identity, a
// disclosed project name and a git remote.
// publishedName is the project name the case's transcript carries. Cases that do
// not name one carry an ordinary disclosed name.
func (c projectPageCase) publishedName() string {
	if c.PublishedProjectName != "" {
		return c.PublishedProjectName
	}
	return publishedProjectName
}

func projectPageInsertTranscript(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner pgtype.UUID, localID, projectHash, visibility, projectName string) pgtype.UUID {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)", database.SystemActorID); err != nil {
		t.Fatal(err)
	}
	id := toPgUUID(uuid.New())
	if _, err := tx.Exec(ctx, `
		INSERT INTO transcripts (id, owner_id, local_id, title, visibility, model_provider, model_name, blob_key,
		                         blob_size_bytes, schema_version, content_hash, wrapped_data_key, encryption_algorithm,
		                         key_version, project_hash, project_name, git_remote)
		VALUES ($1,$2,$3,$4,$5,'claude-code',$6,$7,$8,'0.1.0',$9,$10,'aes-256-gcm-random-nonce-v1',1,$11,$12,$13)
	`, id, owner, localID, "t-"+localID, visibility, "m-"+localID, "blob/"+localID,
		int64(len(localID)), "hash-"+localID, []byte("fixture-wrapped-data-key"),
		projectHash, projectName, "git@github.com:peasant-labs/ledger.git"); err != nil {
		t.Fatalf("insert transcript %s: %v", localID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}

// withProjectPageURLParams attaches BOTH route parameters in one context.
// withChiURLParam builds a fresh route context each call, so calling it twice
// would silently drop the first parameter.
func withProjectPageURLParams(r *http.Request, username, projectHash string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("username", username)
	rctx.URLParams.Add("projectHash", projectHash)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestGetUserProject_VisibilityBoundary(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	h := New(&config.Config{}, pool, nil)

	for _, testCase := range loadProjectPageCases(t) {
		t.Run(testCase.Name, func(t *testing.T) {
			world := newProjectPageWorld(t, ctx, pool, testCase)
			defer world.cleanup(t, ctx)

			r := httptest.NewRequest(http.MethodGet,
				"/api/v1/users/"+world.ownerName+"/projects/"+world.projectHash, nil)
			r = withProjectPageURLParams(r, world.ownerName, world.projectHash)
			switch testCase.Viewer {
			case "owner":
				r = r.WithContext(withUserID(r.Context(), uuid.UUID(world.owner.Bytes)))
			case "other":
				r = r.WithContext(withUserID(r.Context(), uuid.UUID(world.other.Bytes)))
			}
			w := httptest.NewRecorder()

			h.GetUserProject(w, r)

			if w.Code != testCase.WantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, testCase.WantStatus, w.Body.String())
			}
			if testCase.WantStatus != http.StatusOK {
				// A refusal must not leak the page it refused to show.
				if strings.Contains(w.Body.String(), testCase.publishedName()) {
					t.Fatalf("the refusal disclosed the project's published name: %s", w.Body.String())
				}
				return
			}

			var page struct {
				Project     resolvedProject `json:"project"`
				Transcripts []struct {
					ProjectHash        string `json:"project_hash"`
					ProjectDisplayName string `json:"project_display_name"`
				} `json:"transcripts"`
				Collectives []ProjectCollectiveRollupEntry `json:"collectives"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
				t.Fatalf("decode the project page %q: %v", w.Body.String(), err)
			}
			if len(page.Transcripts) != testCase.WantTranscriptCount {
				t.Fatalf("the page listed %d transcripts, want %d", len(page.Transcripts), testCase.WantTranscriptCount)
			}
			if page.Project.ProjectHash != world.projectHash {
				t.Fatalf("the page is keyed on %q, want the requested project hash %q", page.Project.ProjectHash, world.projectHash)
			}
			if page.Project.DisplayName == "" || page.Project.DisplayName == "Other" {
				t.Fatalf("resolved display name = %q; it may never be empty or the literal \"Other\"", page.Project.DisplayName)
			}
			if testCase.WantDisplayName != "" && page.Project.DisplayName != testCase.WantDisplayName {
				t.Errorf("resolved display name = %q, want %q", page.Project.DisplayName, testCase.WantDisplayName)
			}
			if testCase.WantNameSource != "" && page.Project.NameSource != projectname.NameSource(testCase.WantNameSource) {
				t.Errorf("resolved name source = %q, want %q", page.Project.NameSource, testCase.WantNameSource)
			}
			// The repository label is rendered as a subtitle independently of which
			// tier supplied the display name, so it is asserted on its own. An empty
			// label here means the contract module's remote-label rule is not reaching
			// the resolver, which looks on screen exactly like the missing-name defect
			// this work exists to fix.
			if testCase.WantRemoteLabel != "" && page.Project.RemoteLabel != testCase.WantRemoteLabel {
				t.Errorf("resolved remote label = %q, want %q", page.Project.RemoteLabel, testCase.WantRemoteLabel)
			}
			if len(page.Collectives) != testCase.WantCollectiveCount {
				t.Fatalf("the roll-up listed %d collectives, want %d. An empty roll-up on a project that HAS an approved "+
					"contribution means the page is not calling the shared collectives query at all.",
					len(page.Collectives), testCase.WantCollectiveCount)
			}
			for _, collective := range page.Collectives {
				if collective.TranscriptCount != int32(testCase.WantCollectiveTranscriptCount) {
					t.Errorf("collective %q counted %d transcripts, want %d",
						collective.Name, collective.TranscriptCount, testCase.WantCollectiveTranscriptCount)
				}
				if collective.ID != world.collective {
					t.Errorf("the roll-up named a collective this project did not contribute to")
				}
			}
			for _, listed := range page.Transcripts {
				if listed.ProjectHash != world.projectHash {
					t.Errorf("the page listed a transcript of project %q, want only %q", listed.ProjectHash, world.projectHash)
				}
				if listed.ProjectDisplayName != page.Project.DisplayName {
					t.Errorf("a listed transcript renders %q while the page renders %q; every surface must show one name",
						listed.ProjectDisplayName, page.Project.DisplayName)
				}
			}
		})
	}
}

// TestTranscriptProjectHashIsRequired proves the NOT NULL identity constraint is
// live in the database, not merely intended. It is the backstop behind the publish
// guard: if a future write path forgets the guard, the row is still refused.
func TestTranscriptProjectHashIsRequired(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	owner := shareInsertUser(t, ctx, pool, "project-nullhash-"+suffix)
	defer cleanupOwners(t, ctx, pool, owner)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)", database.SystemActorID); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO transcripts (id, owner_id, local_id, title, visibility, model_provider, model_name, blob_key,
		                         blob_size_bytes, schema_version, content_hash, wrapped_data_key, encryption_algorithm,
		                         key_version, project_hash)
		VALUES ($1,$2,$3,$4,'private','claude-code',$5,$6,$7,'0.1.0',$8,$9,'aes-256-gcm-random-nonce-v1',1,NULL)
	`, toPgUUID(uuid.New()), owner, "nullhash-"+suffix, "t", "m", "blob/x", int64(1), "hash", []byte("k"))
	if err == nil {
		t.Fatal("a transcript with no project identity was stored. project_hash is what groups a publisher's " +
			"transcripts, so a row without one belongs to no project and appears in no project's history; the column " +
			"must be NOT NULL.")
	}
	if !strings.Contains(err.Error(), "project_hash") {
		t.Fatalf("the refusal does not name the column: %v", err)
	}
}
