//go:build integration

package handler

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/projectname"
	"github.com/peasant-labs/village/backend/internal/promptattach"
)

// prWorld is the world the pull request read fixtures declare: people,
// collectives with their members and linked repositories, transcripts with
// their shares and tags, and attachments binding transcripts to pull requests.
// It is written the way production writes it - shares through the attempt
// ledger, attachment states through promptattach.Transition - so the reads
// under test see rows the write paths could have produced.
//
// Attachments are recorded and moved to their states in declaration order,
// except that every detach happens last, after every attachment has been
// attached, again in declaration order. So a fixture can make a pull request
// attached early and detached late, which is what tells "newest first by the
// later of attached and detached" from "by attached" alone.
type prWorld struct {
	People      []string            `yaml:"people"`
	Collectives []prWorldCollective `yaml:"collectives"`
	Transcripts []prWorldTranscript `yaml:"transcripts"`
	Attachments []prWorldAttachment `yaml:"attachments"`
	GitHub      prWorldGitHub       `yaml:"github"`
}

type prWorldCollective struct {
	Name         string            `yaml:"name"`
	CreatedBy    string            `yaml:"created_by"`
	DataAccess   string            `yaml:"data_access"`
	LinkedOrg    string            `yaml:"linked_org"`
	Members      map[string]string `yaml:"members"`
	Repositories []prWorldRepo     `yaml:"repositories"`
}

type prWorldRepo struct {
	Repo string `yaml:"repo"`
	// Private is required: a repository whose privacy a fixture forgot to state
	// must not silently become public.
	Private *bool `yaml:"private"`
}

type prWorldTranscript struct {
	Name       string            `yaml:"name"`
	Owner      string            `yaml:"owner"`
	Visibility string            `yaml:"visibility"`
	Remote     string            `yaml:"remote"`
	Turns      *int32            `yaml:"turns"`
	DurationMs *int64            `yaml:"duration_ms"`
	TokensIn   *int64            `yaml:"tokens_in"`
	TokensOut  *int64            `yaml:"tokens_out"`
	Shares     map[string]string `yaml:"shares"`
	Tags       []string          `yaml:"tags"`
}

type prWorldAttachment struct {
	Collective  string   `yaml:"collective"`
	Repo        string   `yaml:"repo"`
	Number      int      `yaml:"number"`
	Author      string   `yaml:"author"`
	State       string   `yaml:"state"`
	Transcripts []string `yaml:"transcripts"`
	// Unlinked removes the collective's repository link after the attachment is
	// recorded, the way an owner unlinking a repository leaves it.
	Unlinked bool `yaml:"unlinked"`
}

type prWorldGitHub struct {
	PullRequests []prWorldPull                `yaml:"pull_requests"`
	Readers      map[string]map[string]string `yaml:"readers"`
}

type prWorldPull struct {
	Ref     string `yaml:"ref"`
	Title   string `yaml:"title"`
	HeadRef string `yaml:"head_ref"`
}

// builtPRWorld is a world written to the database, with the ids its names
// resolve to.
type builtPRWorld struct {
	pool        *pgxpool.Pool
	people      map[string]pgtype.UUID
	logins      map[string]string
	githubIDs   map[string]int64
	collectives map[string]pgtype.UUID
	transcripts map[string]pgtype.UUID
	// tags maps a fixture tag to the name it was written under, which carries
	// the world's suffix because tag names are global.
	tags   map[string]string
	github *pullReadsGitHub
}

// validate refuses a world whose names do not resolve, so a typo cannot make a
// case pass by referring to nothing.
func (w prWorld) validate(t *testing.T, fixture string) {
	t.Helper()
	people := map[string]bool{}
	for _, p := range w.People {
		people[p] = true
	}
	collectives := map[string]map[string]bool{}
	for _, c := range w.Collectives {
		if !people[c.CreatedBy] {
			t.Fatalf("%s: collective %q is created by unknown person %q", fixture, c.Name, c.CreatedBy)
		}
		for person, role := range c.Members {
			if !people[person] || (role != "member" && role != "contributor" && role != "pending") {
				t.Fatalf("%s: collective %q has member %q with role %q", fixture, c.Name, person, role)
			}
		}
		repos := map[string]bool{}
		for _, r := range c.Repositories {
			if r.Private == nil {
				t.Fatalf("%s: collective %q links %s without saying whether it is private", fixture, c.Name, r.Repo)
			}
			repos[r.Repo] = true
		}
		collectives[c.Name] = repos
	}
	transcripts := map[string]bool{}
	for _, tr := range w.Transcripts {
		if !people[tr.Owner] {
			t.Fatalf("%s: transcript %q is owned by unknown person %q", fixture, tr.Name, tr.Owner)
		}
		switch tr.Visibility {
		case dbVisibilityPublic, dbVisibilityShared, dbVisibilityPrivate:
		default:
			t.Fatalf("%s: transcript %q has visibility %q", fixture, tr.Name, tr.Visibility)
		}
		for collective, status := range tr.Shares {
			if collectives[collective] == nil || (status != "approved" && status != "pending") {
				t.Fatalf("%s: transcript %q shares with %q as %q", fixture, tr.Name, collective, status)
			}
		}
		transcripts[tr.Name] = true
	}
	for _, a := range w.Attachments {
		if collectives[a.Collective] == nil || !collectives[a.Collective][a.Repo] || !people[a.Author] {
			t.Fatalf("%s: attachment %s#%d names a collective, repository, or author the world does not have", fixture, a.Repo, a.Number)
		}
		if _, err := promptattach.Parse(a.State); err != nil {
			t.Fatalf("%s: attachment %s#%d has state %q", fixture, a.Repo, a.Number, a.State)
		}
		for _, name := range a.Transcripts {
			if !transcripts[name] {
				t.Fatalf("%s: attachment %s#%d binds unknown transcript %q", fixture, a.Repo, a.Number, name)
			}
		}
	}
	for person := range w.GitHub.Readers {
		if !people[person] {
			t.Fatalf("%s: GitHub reader %q is not a person in the world", fixture, person)
		}
	}
}

// buildPRWorld writes the world and registers its teardown.
func buildPRWorld(t *testing.T, pool *pgxpool.Pool, w prWorld, fixture string) *builtPRWorld {
	t.Helper()
	w.validate(t, fixture)
	ctx := context.Background()
	built := &builtPRWorld{
		pool: pool, people: map[string]pgtype.UUID{}, logins: map[string]string{}, githubIDs: map[string]int64{},
		collectives: map[string]pgtype.UUID{}, transcripts: map[string]pgtype.UUID{}, tags: map[string]string{},
		github: newPullReadsGitHub(t),
	}
	// Random identities, so two runs or two suites never collide on the unique
	// GitHub id, login, tag, or (repository, number) columns.
	base := 7_000_000_000 + rand.Int63n(1_000_000_000)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	// Teardown is registered before the first write, so a world that fails
	// halfway is still removed: the owners and everything cascading from them,
	// the audit rows, then the world's own tags.
	var ids []pgtype.UUID
	t.Cleanup(func() {
		ctx := context.Background()
		if len(ids) > 0 {
			cleanupOwners(t, ctx, pool, ids...)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM tags WHERE name LIKE $1`, "prr-%-"+suffix); err != nil {
			t.Errorf("remove the world's tags: %v", err)
		}
	})
	for i, person := range w.People {
		githubID := base + int64(i)
		login := fmt.Sprintf("prr-%s-%s", person, suffix)
		var id pgtype.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (github_id, github_username, provider_user_id, is_discoverable)
			VALUES ($1, $2, $1::bigint::text, true) RETURNING id
		`, githubID, login).Scan(&id); err != nil {
			t.Fatalf("insert person %s: %v", person, err)
		}
		built.people[person], built.logins[person], built.githubIDs[person] = id, login, githubID
		ids = append(ids, id)
	}

	for _, c := range w.Collectives {
		dataAccess := c.DataAccess
		if dataAccess == "" {
			dataAccess = "members_only"
		}
		var id pgtype.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO groups (name, created_by, data_access, linked_github_org) VALUES ($1, $2, $3, $4) RETURNING id
		`, "prr-"+c.Name+"-"+suffix, built.people[c.CreatedBy], dataAccess, nullableText(c.LinkedOrg)).Scan(&id); err != nil {
			t.Fatalf("insert collective %s: %v", c.Name, err)
		}
		built.collectives[c.Name] = id
		if _, err := pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, 'owner')`, id, built.people[c.CreatedBy]); err != nil {
			t.Fatalf("add the owner of %s: %v", c.Name, err)
		}
		for person, role := range c.Members {
			if _, err := pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, $3)`, id, built.people[person], role); err != nil {
				t.Fatalf("add %s to %s: %v", person, c.Name, err)
			}
		}
		for _, r := range c.Repositories {
			owner, name, _ := strings.Cut(r.Repo, "/")
			if _, err := pool.Exec(ctx, `
				INSERT INTO collective_repositories (group_id, owner, name, installation_id, is_private, linked_by)
				VALUES ($1, $2, $3, 4242, $4, $5)
			`, id, owner, name, *r.Private, built.people[c.CreatedBy]); err != nil {
				t.Fatalf("link %s to %s: %v", r.Repo, c.Name, err)
			}
		}
	}

	queries := sqlc.New(pool)
	for _, tr := range w.Transcripts {
		built.transcripts[tr.Name] = insertPRWorldTranscript(t, ctx, pool, built.people[tr.Owner], tr, suffix)
		for _, tag := range tr.Tags {
			name := "prr-" + tag + "-" + suffix
			created, err := queries.GetOrCreateTag(ctx, name)
			if err != nil {
				t.Fatalf("create tag %s: %v", tag, err)
			}
			if err := queries.LinkTranscriptTag(ctx, sqlc.LinkTranscriptTagParams{TranscriptID: built.transcripts[tr.Name], TagID: created.ID}); err != nil {
				t.Fatalf("tag %s with %s: %v", tr.Name, tag, err)
			}
			built.tags[tag] = name
		}
		for collective, status := range tr.Shares {
			if _, err := pool.Exec(ctx, `
				INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status) VALUES ($1, $2, 1, $3)
			`, built.transcripts[tr.Name], built.collectives[collective], status); err != nil {
				t.Fatalf("share %s with %s: %v", tr.Name, collective, err)
			}
		}
	}

	repoIDs := map[string]int64{}
	type pendingDetach struct {
		id  pgtype.UUID
		ref string
	}
	var detaches []pendingDetach
	for _, a := range w.Attachments {
		owner, name, _ := strings.Cut(a.Repo, "/")
		if repoIDs[a.Repo] == 0 {
			repoIDs[a.Repo] = base + 500 + int64(len(repoIDs))
		}
		created, err := queries.CreatePullRequestAttachment(ctx, sqlc.CreatePullRequestAttachmentParams{
			GroupID: built.collectives[a.Collective], RepoOwner: owner, RepoName: name, GithubRepoID: repoIDs[a.Repo],
			Number: int32(a.Number), HeadSha: "headsha", BaseRemote: a.Repo, HeadRemote: a.Repo, AuthorID: built.people[a.Author],
		})
		if err != nil {
			t.Fatalf("record attachment %s#%d: %v", a.Repo, a.Number, err)
		}
		for position, transcript := range a.Transcripts {
			if err := queries.AttachPullRequestTranscript(ctx, sqlc.AttachPullRequestTranscriptParams{
				AttachmentID: created.ID, TranscriptID: built.transcripts[transcript], Position: int32(position), PreviousVisibility: dbVisibilityPrivate,
			}); err != nil {
				t.Fatalf("bind %s to %s#%d: %v", transcript, a.Repo, a.Number, err)
			}
		}
		for _, step := range prWorldStatePath(a.State) {
			if step == promptattach.Detached {
				detaches = append(detaches, pendingDetach{id: created.ID, ref: fmt.Sprintf("%s#%d", a.Repo, a.Number)})
				continue
			}
			if _, err := promptattach.Transition(ctx, queries, created.ID, step); err != nil {
				t.Fatalf("move %s#%d to %s: %v", a.Repo, a.Number, step, err)
			}
		}
		if a.Unlinked {
			if _, err := pool.Exec(ctx, `DELETE FROM collective_repositories WHERE group_id = $1 AND owner = $2 AND name = $3`,
				built.collectives[a.Collective], owner, name); err != nil {
				t.Fatalf("unlink %s: %v", a.Repo, err)
			}
		}
	}

	for _, d := range detaches {
		if _, err := promptattach.Transition(ctx, queries, d.id, promptattach.Detached); err != nil {
			t.Fatalf("detach %s: %v", d.ref, err)
		}
	}

	for _, pull := range w.GitHub.PullRequests {
		repo, number, _ := strings.Cut(pull.Ref, "#")
		n, err := strconv.Atoi(number)
		if err != nil {
			t.Fatalf("%s: pull request %q is not owner/name#number", fixture, pull.Ref)
		}
		built.github.setPull(repo, n, pull.Title, pull.HeadRef)
	}
	for person, repos := range w.GitHub.Readers {
		for repo, permission := range repos {
			built.github.setReader(strconv.FormatInt(built.githubIDs[person], 10), built.logins[person], repo, permission)
		}
	}
	return built
}

// prWorldStatePath is the lifecycle a new attachment takes to reach a state.
func prWorldStatePath(state string) []promptattach.State {
	switch promptattach.State(state) {
	case promptattach.Waiting:
		return []promptattach.State{promptattach.Waiting}
	case promptattach.Preview:
		return []promptattach.State{promptattach.Preview}
	case promptattach.Attached:
		return []promptattach.State{promptattach.Attached}
	case promptattach.Detached:
		return []promptattach.State{promptattach.Attached, promptattach.Detached}
	default:
		return nil
	}
}

func insertPRWorldTranscript(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner pgtype.UUID, tr prWorldTranscript, suffix string) pgtype.UUID {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transcript %s: %v", tr.Name, err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)", database.SystemActorID); err != nil {
		t.Fatalf("declare the system actor: %v", err)
	}
	localID := tr.Name + "-" + suffix
	id := toPgUUID(uuid.New())
	if _, err := tx.Exec(ctx, `
		INSERT INTO transcripts (id, owner_id, local_id, title, visibility, model_provider, model_name, blob_key, blob_size_bytes,
		                         schema_version, content_hash, wrapped_data_key, encryption_algorithm, key_version, project_hash,
		                         git_remote, turn_count, duration_ms, tokens_in, tokens_out, session_origin)
		VALUES ($1, $2, $3, $4, $5, 'claude-code', 'model', $6, 1, '0.1.0', $7, $8, 'aes-256-gcm-random-nonce-v1', 1, $9,
		        $10, $11, $12, $13, $14, 'user')
	`, id, owner, localID, "t-"+tr.Name, tr.Visibility, "blob/"+localID, schema.ComputeTranscriptHash([]byte(localID)),
		[]byte("fixture-wrapped-data-key"), fixtureProjectHash(localID), nullableText(tr.Remote), tr.Turns, tr.DurationMs, tr.TokensIn, tr.TokensOut); err != nil {
		t.Fatalf("insert transcript %s: %v", tr.Name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit transcript %s: %v", tr.Name, err)
	}
	return id
}

// handler composes the production handlers over the world's pool and its
// GitHub fake. Each call is a fresh handler, so no cached GitHub answer or spent
// question budget carries from one case to the next.
func (w *builtPRWorld) handler(t *testing.T, pool *pgxpool.Pool) *Handler {
	t.Helper()
	return &Handler{cfg: minimalConfig(), pool: pool, queries: sqlc.New(pool), gh: w.github.client(t),
		projectNames: projectname.Resolver{Label: schema.RemoteLabel}}
}

// router mounts the reads under test the way the production router does.
func prWorldRouter(h *Handler) http.Handler {
	routes := chi.NewRouter()
	routes.Route("/api/v1", func(r chi.Router) {
		h.RegisterTranscriptBrowseRoutes(r)
		h.RegisterCollectiveBrowseRoutes(r)
		r.With(h.AuthOptional).Get("/transcripts/{id}/pulls", h.ListTranscriptPullRequests)
		r.With(h.AuthRequired).Get("/users/me/stats", h.GetMyStats)
		r.With(h.AuthRequired).Get("/groups/{id}/repositories/available", h.ListAvailableRepositories)
	})
	return routes
}

// get serves one request as a named person, or anonymously for "".
func (w *builtPRWorld) get(t *testing.T, h *Handler, routes http.Handler, viewer, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	if viewer != "" {
		id, ok := w.people[viewer]
		if !ok {
			t.Fatalf("viewer %q is not a person in the world", viewer)
		}
		token, err := auth.CreateToken(h.cfg.JWTSecret, uuid.UUID(id.Bytes), w.logins[viewer])
		if err != nil {
			t.Fatalf("mint a session for %s: %v", viewer, err)
		}
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, r)
	return rec
}

// transcriptName maps a transcript id on the wire back to its fixture name.
func (w *builtPRWorld) transcriptName(t *testing.T, id string) string {
	t.Helper()
	for name, tid := range w.transcripts {
		if uuid.UUID(tid.Bytes).String() == id {
			return name
		}
	}
	t.Fatalf("the response names transcript %s, which the world did not write", id)
	return ""
}
