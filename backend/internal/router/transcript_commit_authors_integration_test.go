//go:build integration

package router

import (
	"bytes"
	"context"
	_ "embed"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
)

//go:embed testdata/transcript_commit_authors.yaml
var transcriptCommitAuthorsYAML []byte

// transcriptCommitAuthorsFixture names the embedded fixture in failure messages.
const transcriptCommitAuthorsFixture = "testdata/transcript_commit_authors.yaml"

// transcriptCommitsPath is the commits list of one transcript. The served
// contract does not declare the route yet; it is listed in
// testdata/undocumented_routes.yaml.
func transcriptCommitsPath(id uuid.UUID) string {
	// unstring-allow: go-endpoint-literal — the commits list is not in the served contract yet
	return "/api/v1/transcripts/" + id.String() + "/commits"
}

// requiredTranscriptCommitAuthorCases is the fixture's name manifest: both
// owner settings against each caller the rule distinguishes.
var requiredTranscriptCommitAuthorCases = []string{
	"hidden_owner_to_owner",
	"hidden_owner_to_collective_owner",
	"hidden_owner_to_signed_in_stranger",
	"hidden_owner_to_anonymous_caller",
	"discoverable_owner_to_owner",
	"discoverable_owner_to_collective_owner",
	"discoverable_owner_to_signed_in_stranger",
	"discoverable_owner_to_anonymous_caller",
}

// commitAuthorViewer is who sends the request.
type commitAuthorViewer string

const (
	commitAuthorViewerOwner           commitAuthorViewer = "owner"
	commitAuthorViewerCollectiveOwner commitAuthorViewer = "collective_owner"
	commitAuthorViewerStranger        commitAuthorViewer = "stranger"
	commitAuthorViewerAnonymous       commitAuthorViewer = "anonymous"
)

func (v commitAuthorViewer) known() bool {
	switch v {
	case commitAuthorViewerOwner, commitAuthorViewerCollectiveOwner, commitAuthorViewerStranger, commitAuthorViewerAnonymous:
		return true
	}
	return false
}

// commitAuthorAnswer is what the caller receives for each commit's author.
type commitAuthorAnswer string

const (
	commitAuthorServed   commitAuthorAnswer = "served"
	commitAuthorWithheld commitAuthorAnswer = "withheld"
)

type commitAuthorCommit struct {
	Sha           string  `yaml:"sha"`
	Message       *string `yaml:"message"`
	AuthorName    *string `yaml:"author_name"`
	AuthorEmail   *string `yaml:"author_email"`
	AuthoredAtMs  *int64  `yaml:"authored_at_ms"`
	CommittedAtMs *int64  `yaml:"committed_at_ms"`
}

type commitAuthorCase struct {
	Name              string             `yaml:"name"`
	OwnerDiscoverable bool               `yaml:"owner_discoverable"`
	Viewer            commitAuthorViewer `yaml:"viewer"`
	Author            commitAuthorAnswer `yaml:"author"`
	Why               string             `yaml:"why"`
}

type commitAuthorFixture struct {
	Commits      []commitAuthorCommit `yaml:"commits"`
	ServedBody   string               `yaml:"served_body"`
	WithheldBody string               `yaml:"withheld_body"`
	Cases        []commitAuthorCase   `yaml:"cases"`
}

func loadCommitAuthorFixture(t *testing.T) commitAuthorFixture {
	t.Helper()
	var fixture commitAuthorFixture
	d := yaml.NewDecoder(bytes.NewReader(transcriptCommitAuthorsYAML))
	d.KnownFields(true)
	if err := d.Decode(&fixture); err != nil {
		t.Fatalf("decode %s: %v", transcriptCommitAuthorsFixture, err)
	}
	if len(fixture.Commits) == 0 || fixture.ServedBody == "" || fixture.WithheldBody == "" {
		t.Fatalf("%s must carry commits, served_body and withheld_body", transcriptCommitAuthorsFixture)
	}
	names := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" || c.Why == "" || names[c.Name] {
			t.Fatalf("%s case %+v must carry a unique name and a reason", transcriptCommitAuthorsFixture, c)
		}
		if !c.Viewer.known() {
			t.Fatalf("%s case %q names viewer %q; the viewers are owner, collective_owner, stranger and anonymous", transcriptCommitAuthorsFixture, c.Name, c.Viewer)
		}
		if c.Author != commitAuthorServed && c.Author != commitAuthorWithheld {
			t.Fatalf("%s case %q names author %q; the answers are served and withheld", transcriptCommitAuthorsFixture, c.Name, c.Author)
		}
		names[c.Name] = true
	}
	for _, name := range requiredTranscriptCommitAuthorCases {
		if !names[name] {
			t.Fatalf("%s lost required case %q; restore it", transcriptCommitAuthorsFixture, name)
		}
	}
	return fixture
}

// TestTranscriptCommitAuthorsFollowTheOwnerRule drives
// GET /api/v1/transcripts/{id}/commits through the production router over
// PostgreSQL, for an anonymous and a discoverable owner, as the owner, a
// collective owner holding an approved submission, a signed-in stranger and a
// signed-out caller. A caller to whom the owner is anonymous receives no
// commit author name or email; every other caller receives the exact bytes the
// route has always sent.
func TestTranscriptCommitAuthorsFollowTheOwnerRule(t *testing.T) {
	fixture := loadCommitAuthorFixture(t)
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the mounted commit author test; the integration gate rejects this skip")
	}
	ctx := context.Background()
	poolConfig, err := database.PoolConfig(databaseURL)
	if err != nil {
		t.Fatalf("build the pool config: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Skipf("cannot reach the test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.RunMigrations(pool); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	asSystem := func(sql string, args ...any) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)", database.SystemActorID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	type person struct {
		id    uuid.UUID
		login string
	}
	var people []uuid.UUID
	var transcripts []uuid.UUID
	// Teardown order for the fail-closed, append-only audit: transcripts as
	// the system actor, then the users (their collective cascades), then the
	// audit rows the deletes appended, last.
	t.Cleanup(func() {
		if len(transcripts) > 0 {
			if err := asSystem(`DELETE FROM transcripts WHERE id = ANY($1)`, transcripts); err != nil {
				t.Errorf("delete the transcripts as the system actor: %v", err)
			}
		}
		if len(people) > 0 {
			if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, people); err != nil {
				t.Errorf("delete the users: %v", err)
			}
		}
		if len(transcripts) == 0 {
			return
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Errorf("begin audit purge: %v", err)
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, "SET LOCAL app.audit_maintenance = 'on'"); err != nil {
			t.Errorf("set maintenance escape: %v", err)
			return
		}
		if _, err := tx.Exec(ctx, `DELETE FROM transcript_governance_events_audit WHERE transcript_id = ANY($1)`, transcripts); err != nil {
			t.Errorf("purge audit rows: %v", err)
			return
		}
		if err := tx.Commit(ctx); err != nil {
			t.Errorf("commit audit purge: %v", err)
		}
	})

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	newPerson := func(role string, discoverable bool) person {
		t.Helper()
		githubID := 8_000_000_000 + rand.Int63n(1_000_000_000)
		login := "commit-authors-" + role + "-" + suffix
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (github_id, github_username, provider_user_id, is_discoverable)
			VALUES ($1, $2, $1::bigint::text, $3) RETURNING id
		`, githubID, login, discoverable).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", role, err)
		}
		people = append(people, id)
		return person{id: id, login: login}
	}
	newTranscript := func(owner person) uuid.UUID {
		t.Helper()
		id := uuid.New()
		localID := "commit-authors-" + id.String()
		if err := asSystem(`
			INSERT INTO transcripts (id, owner_id, local_id, title, visibility, model_provider, model_name, blob_key, blob_size_bytes,
			                         schema_version, content_hash, wrapped_data_key, encryption_algorithm, key_version, project_hash)
			VALUES ($1, $2, $3, 'commit authors', 'public', 'claude-code', 'model', $4, 1, '0.1.0', $5, $6, 'aes-256-gcm-random-nonce-v1', 1, $7)
		`, id, owner.id, localID, "blob/"+localID, strings.Repeat("a", 64), []byte("fixture-wrapped-data-key"), strings.Repeat("7", 64)); err != nil {
			t.Fatalf("insert the transcript of %s: %v", owner.login, err)
		}
		transcripts = append(transcripts, id)
		for order, c := range fixture.Commits {
			var authoredAt, committedAt *time.Time
			if c.AuthoredAtMs != nil {
				at := time.UnixMilli(*c.AuthoredAtMs)
				authoredAt = &at
			}
			if c.CommittedAtMs != nil {
				at := time.UnixMilli(*c.CommittedAtMs)
				committedAt = &at
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO transcript_commits (transcript_id, commit_order, sha, message, author_name, author_email, authored_at, committed_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			`, id, order, c.Sha, c.Message, c.AuthorName, c.AuthorEmail, authoredAt, committedAt); err != nil {
				t.Fatalf("insert commit %s: %v", c.Sha, err)
			}
		}
		return id
	}

	hiddenOwner := newPerson("hidden", false)
	discoverableOwner := newPerson("discoverable", true)
	collectiveOwner := newPerson("collective", false)
	stranger := newPerson("stranger", false)
	owners := map[bool]person{false: hiddenOwner, true: discoverableOwner}
	transcriptOf := map[bool]uuid.UUID{false: newTranscript(hiddenOwner), true: newTranscript(discoverableOwner)}

	var collective uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO groups (name, created_by) VALUES ($1, $2) RETURNING id`,
		"commit-authors-"+suffix, collectiveOwner.id).Scan(&collective); err != nil {
		t.Fatalf("insert the collective: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id, role, origin) VALUES ($1, $2, 'owner', 'creator')
		ON CONFLICT (group_id, user_id) DO UPDATE SET role = 'owner', origin = 'creator'`, collective, collectiveOwner.id); err != nil {
		t.Fatalf("make the collective owner: %v", err)
	}
	for _, id := range transcriptOf {
		if _, err := pool.Exec(ctx, `INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status) VALUES ($1, $2, 1, 'approved')`,
			id, collective); err != nil {
			t.Fatalf("submit transcript %s to the collective: %v", id, err)
		}
	}

	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct the title pipeline: %v", err)
	}
	cfg := &config.Config{JWTSecret: "transcript-commit-authors", FrontendURL: "https://app.example.com"}
	routes := New(cfg, pool, nil, titles)
	tokenFor := func(p person) string {
		t.Helper()
		token, err := auth.CreateToken(cfg.JWTSecret, p.id, p.login)
		if err != nil {
			t.Fatalf("mint the session of %s: %v", p.login, err)
		}
		return token
	}

	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			owner := owners[c.OwnerDiscoverable]
			var bearer string
			switch c.Viewer {
			case commitAuthorViewerOwner:
				bearer = tokenFor(owner)
			case commitAuthorViewerCollectiveOwner:
				bearer = tokenFor(collectiveOwner)
			case commitAuthorViewerStranger:
				bearer = tokenFor(stranger)
			case commitAuthorViewerAnonymous:
			}
			req := httptest.NewRequest(http.MethodGet, transcriptCommitsPath(transcriptOf[c.OwnerDiscoverable]), nil)
			if bearer != "" {
				req.Header.Set("Authorization", "Bearer "+bearer)
			}
			rec := httptest.NewRecorder()
			routes.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: status %d, want 200 (body: %s)", c.Why, rec.Code, rec.Body.String())
			}
			got := strings.TrimSpace(rec.Body.String())
			want := fixture.ServedBody
			if c.Author == commitAuthorWithheld {
				want = fixture.WithheldBody
			}
			if got != want {
				t.Fatalf("%s: body\n got %s\nwant %s", c.Why, got, want)
			}
			if c.Author != commitAuthorWithheld {
				return
			}
			for _, commit := range fixture.Commits {
				for _, value := range []*string{commit.AuthorName, commit.AuthorEmail} {
					if value != nil && strings.Contains(got, *value) {
						t.Fatalf("%s: the response names commit author %q", c.Why, *value)
					}
				}
			}
		})
	}
}
