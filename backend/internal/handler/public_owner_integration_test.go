//go:build integration

package handler

// How each route shows a transcript owner, collective member or contributor,
// and annotation author to each kind of viewer, driven through the production
// handlers against a real PostgreSQL. The profile-or-anonymous rule lives in
// one SQL statement (ListPublicOwnersForViewer), so a mock could only prove a
// re-implementation agrees with itself.
//
// Two assertions run on every route:
//   - the exact owner shape: a profile carries exactly kind, id, username,
//     display_name and avatar_url with the owner's values; an anonymous owner
//     is exactly {"kind":"anonymous"};
//   - no leak: when the case expects anonymous, the owner's username, id,
//     display name, avatar URL and organization login appear nowhere in the
//     response bytes.

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	gh "github.com/peasant-labs/village/backend/internal/github"
)

//go:embed testdata/public-owner-visibility.yaml
var publicOwnerVisibilityYAML []byte

//go:embed testdata/public-owner-filters.yaml
var publicOwnerFiltersYAML []byte

type poFilter struct {
	Name  string   `yaml:"name"`
	Query string   `yaml:"query"`
	Views []string `yaml:"views"`
}

// requiredPublicOwnerFilters names every filter the matrix must keep running.
var requiredPublicOwnerFilters = []string{"owner", "organization", "project_name", "repository", "project_hash"}

func loadPublicOwnerFilters(t *testing.T) []poFilter {
	t.Helper()
	filters, err := decodeFixtureRows[poFilter](publicOwnerFiltersYAML)
	if err != nil {
		t.Fatalf("load the public-owner filters: %v", err)
	}
	present := map[string]bool{}
	for _, f := range filters {
		if f.Name == "" || f.Query == "" || len(f.Views) == 0 || present[f.Name] {
			t.Fatalf("public-owner filter %+v must carry a unique name, a query and its views", f)
		}
		for _, view := range f.Views {
			if view != "flat" && view != "grouped" {
				t.Fatalf("filter %q names view %q; the views are flat and grouped", f.Name, view)
			}
		}
		present[f.Name] = true
	}
	for _, name := range requiredPublicOwnerFilters {
		if !present[name] {
			t.Fatalf("the public-owner filters no longer contain %q; restore it", name)
		}
	}
	return filters
}

type poExpect struct {
	Owner       string `yaml:"owner"`
	Roster      string `yaml:"roster"`
	ProfilePage string `yaml:"profile_page"`
	Annotator   string `yaml:"annotator"`
	Filter      string `yaml:"filter"`
	Pull        bool   `yaml:"pull"`
	Review      bool   `yaml:"review"`
	MyShares    bool   `yaml:"my_shares"`
	// RelationRoster is how the relation_owner's own collective roster shows
	// the owner (profile | absent), or none when that collective is gone.
	RelationRoster string `yaml:"relation_roster"`
}

type poCase struct {
	Name              string `yaml:"name"`
	Why               string `yaml:"why"`
	OwnerDiscoverable bool   `yaml:"owner_discoverable"`
	Viewer            string `yaml:"viewer"`
	// Relation names how the relation_owner viewer's own collective relates
	// to the owner (see the fixture header); empty for every other viewer.
	Relation string   `yaml:"relation"`
	Expect   poExpect `yaml:"expect"`
}

// requiredPublicOwnerCases names every case that must exist: both owner
// settings against each of the five viewers the rule distinguishes.
var requiredPublicOwnerCases = []string{
	"discoverable_owner_to_anonymous_viewer",
	"discoverable_owner_to_other_viewer",
	"discoverable_owner_to_member_viewer",
	"discoverable_owner_to_themselves",
	"discoverable_owner_to_collective_owner",
	"hidden_owner_to_anonymous_viewer",
	"hidden_owner_to_other_viewer",
	"hidden_owner_to_member_viewer",
	"hidden_owner_to_themselves",
	"hidden_owner_to_collective_owner",
	"hidden_owner_force_added_by_unrelated_owner",
	"hidden_owner_with_pending_join_request",
	"hidden_owner_with_rejected_share",
	"hidden_owner_left_with_approved_share",
	"hidden_owner_submitted_to_deleted_collective",
}

func loadPublicOwnerCases(t *testing.T) []poCase {
	t.Helper()
	cases, err := decodeFixtureRows[poCase](publicOwnerVisibilityYAML)
	if err != nil {
		t.Fatalf("load the public-owner corpus: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || c.Why == "" || present[c.Name] {
			t.Fatalf("public-owner case %+v must carry a unique name and a reason", c)
		}
		present[c.Name] = true
		for field, value := range map[string]string{"owner": c.Expect.Owner, "roster": c.Expect.Roster, "profile_page": c.Expect.ProfilePage, "annotator": c.Expect.Annotator, "filter": c.Expect.Filter} {
			allowed := map[string][]string{
				"owner": {"profile", "anonymous"}, "roster": {"profile", "absent"}, "profile_page": {"profile", "not_found"},
				"annotator": {"name", "anon"}, "filter": {"match", "empty"},
			}[field]
			if !containsString(allowed, value) {
				t.Fatalf("case %q expects %s %q; the outcomes are %v", c.Name, field, value, allowed)
			}
		}
		if (c.Viewer == "relation_owner") != (c.Relation != "") {
			t.Fatalf("case %q: a relation is set exactly when the viewer is relation_owner", c.Name)
		}
		if c.Viewer == "relation_owner" && !containsString([]string{"profile", "absent", "none"}, c.Expect.RelationRoster) {
			t.Fatalf("case %q expects relation_roster %q; the outcomes are profile, absent and none", c.Name, c.Expect.RelationRoster)
		}
	}
	for _, required := range requiredPublicOwnerCases {
		if !present[required] {
			t.Fatalf("the public-owner corpus no longer contains %q; restore it rather than deleting it from this manifest", required)
		}
	}
	return cases
}

type poWorld struct {
	pool        *pgxpool.Pool
	owner       pgtype.UUID
	ownerName   string
	displayName string
	avatarURL   string
	orgLogin    string
	moderator   pgtype.UUID
	member      pgtype.UUID
	other       pgtype.UUID
	group       pgtype.UUID
	projectHash string
	accepted    pgtype.UUID // public, accepted into the collective
	shared      pgtype.UUID // collective-shared, accepted: pullable by members
	pending     pgtype.UUID // public, awaiting review
	private     pgtype.UUID // private, never shared
	contentKey  string      // blob key of the accepted transcript's stored content
	relOwner    pgtype.UUID // owner of the relation collective (relation_owner viewer)
	ownedGroup  pgtype.UUID // a collective the owner created and co-owns with collective_owner
	helper      pgtype.UUID // a helper session the accepted transcript started
	repoOwner   string      // the GitHub account of the pull request's repository
	installID   int64       // the App installation of the owner's linked repository
	suffix      string
	relGroup    pgtype.UUID // the relation collective, when the case has one
	// sharedContentKey is the blob key of the shared transcript's stored
	// content, which the pull content route serves.
	sharedContentKey string
}

func (w *poWorld) id(v pgtype.UUID) string { return uuid.UUID(v.Bytes).String() }

func newPublicOwnerWorld(t *testing.T, ctx context.Context, pool *pgxpool.Pool, h *Handler, c poCase) *poWorld {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	w := &poWorld{
		pool:        pool,
		ownerName:   "po-owner-" + suffix,
		displayName: "Po Display " + suffix,
		avatarURL:   "https://avatars.example.test/po-" + suffix + ".png",
		orgLogin:    "po-org-" + suffix,
		projectHash: strings.Repeat(suffix, 6)[:64],
	}
	w.owner = cvInsertUser(t, ctx, pool, w.ownerName, c.OwnerDiscoverable)
	if _, err := pool.Exec(ctx, `UPDATE users SET display_name = $2, avatar_url = $3 WHERE id = $1`, w.owner, w.displayName, w.avatarURL); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_github_orgs (user_id, org_login, org_id, visible, fetched_at) VALUES ($1, $2, $3, true, now())`,
		w.owner, w.orgLogin, int64(uuid.New().ID())); err != nil {
		t.Fatal(err)
	}
	w.moderator = cvInsertUser(t, ctx, pool, "po-mod-"+suffix, true)
	w.member = cvInsertUser(t, ctx, pool, "po-member-"+suffix, true)
	w.other = cvInsertUser(t, ctx, pool, "po-other-"+suffix, true)
	if err := pool.QueryRow(ctx, `
		INSERT INTO groups (name, created_by, acceptance_mode, data_access) VALUES ($1, $2, 'curated', 'public') RETURNING id
	`, "po-"+suffix, w.moderator).Scan(&w.group); err != nil {
		t.Fatalf("create the collective: %v", err)
	}
	shareAddMember(t, ctx, pool, w.group, w.moderator, "owner")
	shareAddMember(t, ctx, pool, w.group, w.member, "member")
	shareAddMember(t, ctx, pool, w.group, w.owner, "member")

	seed := "posession-" + suffix
	w.accepted = cvInsertTranscript(t, ctx, pool, w.owner, seed, 0, "public", w.projectHash)
	w.shared = cvInsertTranscript(t, ctx, pool, w.owner, seed, 1, "shared", w.projectHash)
	w.pending = cvInsertTranscript(t, ctx, pool, w.owner, seed, 2, "public", w.projectHash)
	w.private = cvInsertTranscript(t, ctx, pool, w.owner, seed, 3, "private", w.projectHash)
	// A personal repository names its owner in the remote, the way an
	// unredacted git remote does.
	execAsSystem(t, ctx, pool, `UPDATE transcripts SET git_remote = $2, git_branch = 'main', project_name = 'po-project'
		WHERE owner_id = $1`, w.owner, "https://github.com/"+w.ownerName+"/po-project.git")

	owner := w.actor("owner")
	moderator := w.actor("collective_owner")
	for _, transcript := range []pgtype.UUID{w.accepted, w.shared, w.pending} {
		w.mustOK(t, h.ShareTranscript, owner, http.MethodPost, "/share", map[string][]string{"group_ids": {w.id(w.group)}},
			map[string]string{"id": w.id(transcript)})
	}
	for _, transcript := range []pgtype.UUID{w.accepted, w.shared} {
		w.mustOK(t, h.ReviewShare, moderator, http.MethodPatch, "/review", map[string]string{"status": "approved"},
			map[string]string{"id": w.id(w.group), "transcriptID": w.id(transcript)})
	}
	for _, transcript := range []pgtype.UUID{w.accepted, w.shared} {
		rec := httptest.NewRecorder()
		h.CreateTranscriptAnnotation(rec, w.direct(owner, http.MethodPost, "/annotate",
			map[string]any{"typeId": "quality.session_outcome", "value": "success", "entryIndex": 0},
			map[string]string{"id": w.id(transcript)}))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create the owner's annotation: %d %s", rec.Code, rec.Body.String())
		}
	}
	// Annotations that name the owner's project, which the annotation routes
	// withhold for an anonymous owner.
	if _, err := pool.Exec(ctx, `UPDATE annotations SET project_hash = $2 WHERE owner_id = $1`, w.owner, w.projectHash); err != nil {
		t.Fatalf("give the owner's annotations a target project: %v", err)
	}
	// Stored content whose session detail names the repository, served by the
	// content route through a test blob store.
	w.contentKey = "transcripts/" + uuid.NewString() + ".bin"
	execAsSystem(t, ctx, pool, `UPDATE transcripts SET blob_key = $2 WHERE id = $1`, w.accepted, w.contentKey)
	w.sharedContentKey = "transcripts/" + uuid.NewString() + ".bin"
	execAsSystem(t, ctx, pool, `UPDATE transcripts SET blob_key = $2 WHERE id = $1`, w.shared, w.sharedContentKey)

	w.relOwner = cvInsertUser(t, ctx, pool, "po-rel-"+suffix, true)
	if c.Relation != "" {
		w.buildRelation(t, ctx, h, c.Relation, suffix)
	}
	w.suffix = suffix
	w.buildPersonFields(t, ctx, h)
	return w
}

// buildPersonFields gives the owner every other way a person appears on a
// route: an attestation, a pull request attachment they authored and asked
// for, a collective they created, a repository they linked to it, and a
// helper session that groups under one of their transcripts.
func (w *poWorld) buildPersonFields(t *testing.T, ctx context.Context, h *Handler) {
	t.Helper()
	owner := w.actor("owner")
	// The owner signed in with GitHub, so a requester id resolves to them.
	if _, err := w.pool.Exec(ctx, `UPDATE users SET provider_user_id = github_id::text WHERE id = $1`, w.owner); err != nil {
		t.Fatal(err)
	}

	// An attestation the owner made for their own transcript, naming their
	// organization.
	rec := httptest.NewRecorder()
	h.CreateAttestation(rec, w.direct(owner, http.MethodPost, "/attest",
		map[string]any{"org_login": w.orgLogin, "attestation_type": "used_in_training"},
		map[string]string{"id": w.id(w.accepted)}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create the owner's attestation: %d %s", rec.Code, rec.Body.String())
	}

	// A pull request in a public repository linked to the collective, which
	// the owner authored and asked prompts for.
	w.repoOwner = "po-repoowner-" + w.suffix
	if _, err := w.pool.Exec(ctx, `INSERT INTO collective_repositories (group_id, owner, name, installation_id, is_private, linked_by)
		VALUES ($1, $2, 'po-repo', $3, false, $4)`, w.group, w.repoOwner, int64(uuid.New().ID()), w.moderator); err != nil {
		t.Fatal(err)
	}
	if _, err := w.pool.Exec(ctx, `INSERT INTO pull_request_attachments (repo_owner, repo_name, github_repo_id, number, head_sha,
			base_remote, head_remote, author_id, requester_github_id, state, group_id)
		SELECT $1, 'po-repo', $2, 7, 'deadbeef', 'github.com/x/y', 'github.com/x/y', u.id, u.github_id, 'attached', $3
		FROM users u WHERE u.id = $4`, w.repoOwner, int64(uuid.New().ID()), w.group, w.owner); err != nil {
		t.Fatal(err)
	}

	// A collective the owner created, co-owned by collective_owner, with
	// member as a plain member, and a repository the owner linked to it
	// through an App installation all three can reach.
	if err := w.pool.QueryRow(ctx, `
		INSERT INTO groups (name, created_by, acceptance_mode, data_access) VALUES ($1, $2, 'open', 'public') RETURNING id
	`, "po-owned-"+w.suffix, w.owner).Scan(&w.ownedGroup); err != nil {
		t.Fatal(err)
	}
	shareAddMember(t, ctx, w.pool, w.ownedGroup, w.owner, "owner")
	shareAddMember(t, ctx, w.pool, w.ownedGroup, w.moderator, "owner")
	shareAddMember(t, ctx, w.pool, w.ownedGroup, w.member, "member")
	w.installID = int64(uuid.New().ID())
	account := int64(uuid.New().ID())
	if _, err := w.pool.Exec(ctx, `INSERT INTO github_app_installations (installation_id, account_login, account_id, account_type)
		VALUES ($1, $2, $3, 'Organization')`, w.installID, "po-inst-"+w.suffix, account); err != nil {
		t.Fatal(err)
	}
	for _, person := range []pgtype.UUID{w.owner, w.moderator, w.member} {
		if _, err := w.pool.Exec(ctx, `INSERT INTO user_github_orgs (user_id, org_login, org_id, visible, fetched_at) VALUES ($1, $2, $3, false, now())`,
			person, "po-inst-"+w.suffix, account); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.pool.Exec(ctx, `INSERT INTO collective_repositories (group_id, owner, name, installation_id, is_private, linked_by)
		VALUES ($1, $2, 'po-linked', $3, false, $4)`, w.ownedGroup, "po-inst-"+w.suffix, w.installID, w.owner); err != nil {
		t.Fatal(err)
	}

	// A helper session the accepted transcript started, so the grouped list
	// offers a helper group whose members route is checked too.
	w.helper = cvInsertTranscript(t, ctx, w.pool, w.owner, "posession-"+w.suffix, 4, "public", w.projectHash)
	execAsSystem(t, ctx, w.pool, `UPDATE transcripts SET session_purpose = 'helper_review', parent_session_id = (SELECT local_id FROM transcripts WHERE id = $2) WHERE id = $1`,
		w.helper, w.accepted)
}

// buildRelation gives relation_owner a collective of their own and relates
// the owner to it in one way. Only acts the owner took (a submission, or a
// leftover join request) may reveal the owner's transcripts to relation_owner;
// a membership relation_owner created alone may not.
func (w *poWorld) buildRelation(t *testing.T, ctx context.Context, h *Handler, relation, suffix string) {
	t.Helper()
	if err := w.pool.QueryRow(ctx, `
		INSERT INTO groups (name, created_by, acceptance_mode, data_access) VALUES ($1, $2, 'curated', 'public') RETURNING id
	`, "po-rel-"+suffix, w.relOwner).Scan(&w.relGroup); err != nil {
		t.Fatalf("create the relation collective: %v", err)
	}
	shareAddMember(t, ctx, w.pool, w.relGroup, w.relOwner, "owner")
	owner, relOwner := w.actor("owner"), w.actor("relation_owner")
	submit := func() {
		w.mustOK(t, h.ShareTranscript, owner, http.MethodPost, "/share", map[string][]string{"group_ids": {w.id(w.relGroup)}},
			map[string]string{"id": w.id(w.accepted)})
	}
	review := func(status string) {
		w.mustOK(t, h.ReviewShare, relOwner, http.MethodPatch, "/review", map[string]string{"status": status},
			map[string]string{"id": w.id(w.relGroup), "transcriptID": w.id(w.accepted)})
	}
	switch relation {
	case "force_add":
		// The collective's owner added the person by username; the person did
		// nothing.
		shareAddMember(t, ctx, w.pool, w.relGroup, w.owner, "member")
	case "join_request":
		shareAddMember(t, ctx, w.pool, w.relGroup, w.owner, "pending")
	case "rejected_share":
		shareAddMember(t, ctx, w.pool, w.relGroup, w.owner, "member")
		submit()
		review("rejected")
	case "left_with_approved_share":
		shareAddMember(t, ctx, w.pool, w.relGroup, w.owner, "member")
		submit()
		review("approved")
		if _, err := w.pool.Exec(ctx, `DELETE FROM group_members WHERE group_id = $1 AND user_id = $2`, w.relGroup, w.owner); err != nil {
			t.Fatal(err)
		}
	case "deleted_collective":
		shareAddMember(t, ctx, w.pool, w.relGroup, w.owner, "member")
		submit()
		if _, err := w.pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, w.relGroup); err != nil {
			t.Fatal(err)
		}
		w.relGroup = pgtype.UUID{}
	default:
		t.Fatalf("the fixture names relation %q; the relations are force_add, join_request, rejected_share, left_with_approved_share and deleted_collective", relation)
	}
}

func (w *poWorld) cleanup(t *testing.T, ctx context.Context) {
	t.Helper()
	if _, err := w.pool.Exec(ctx, "DELETE FROM annotations WHERE owner_id = $1", w.owner); err != nil {
		t.Errorf("cleanup annotations: %v", err)
	}
	if _, err := w.pool.Exec(ctx, "DELETE FROM pull_request_attachments WHERE author_id = $1", w.owner); err != nil {
		t.Errorf("cleanup the attachment: %v", err)
	}
	if _, err := w.pool.Exec(ctx, "DELETE FROM github_app_installations WHERE installation_id = $1", w.installID); err != nil {
		t.Errorf("cleanup the installation: %v", err)
	}
	if _, err := w.pool.Exec(ctx, "DELETE FROM attestations WHERE attester_id = $1", w.owner); err != nil {
		t.Errorf("cleanup the attestation: %v", err)
	}
	if _, err := w.pool.Exec(ctx, "DELETE FROM groups WHERE id = ANY($1)", []pgtype.UUID{w.group, w.relGroup, w.ownedGroup}); err != nil {
		t.Errorf("cleanup the collective: %v", err)
	}
	cleanupOwners(t, ctx, w.pool, w.owner, w.moderator, w.member, w.other, w.relOwner)
}

func (w *poWorld) actor(name string) *AuthUser {
	switch name {
	case "owner":
		return &AuthUser{ID: uuid.UUID(w.owner.Bytes), Username: w.ownerName}
	case "collective_owner":
		return &AuthUser{ID: uuid.UUID(w.moderator.Bytes), Username: "po-mod"}
	case "member":
		return &AuthUser{ID: uuid.UUID(w.member.Bytes), Username: "po-member"}
	case "other":
		return &AuthUser{ID: uuid.UUID(w.other.Bytes), Username: "po-other"}
	case "relation_owner":
		return &AuthUser{ID: uuid.UUID(w.relOwner.Bytes), Username: "po-rel"}
	}
	return nil
}

// direct builds a request for calling one handler with explicit URL params.
func (w *poWorld) direct(actor *AuthUser, method, target string, body any, params map[string]string) *http.Request {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	route := chi.NewRouteContext()
	for key, value := range params {
		route.URLParams.Add(key, value)
	}
	ctx := context.Background()
	if actor != nil {
		ctx = context.WithValue(ctx, UserContextKey, actor)
	}
	ctx = context.WithValue(ctx, chi.RouteCtxKey, route)
	return httptest.NewRequest(method, target, bytes.NewReader(raw)).WithContext(ctx)
}

func (w *poWorld) mustOK(t *testing.T, handler http.HandlerFunc, actor *AuthUser, method, target string, body any, params map[string]string) {
	t.Helper()
	rec := httptest.NewRecorder()
	handler(rec, w.direct(actor, method, target, body, params))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s %s answered %d: %s", method, target, rec.Code, rec.Body.String())
	}
}

// publicOwnerRouter mounts the production read handlers at their API paths.
func publicOwnerRouter(h *Handler) http.Handler {
	if err := h.registerCollectiveGroupedScopes(); err != nil {
		panic(err)
	}
	h.RegisterTranscriptBrowseRoutes(chi.NewRouter())
	r := chi.NewRouter()
	r.Get("/transcripts", h.ListTranscripts)
	r.Get("/transcripts/{id}", h.GetTranscript)
	r.Get("/transcripts/{id}/annotations", h.ListTranscriptAnnotations)
	r.Get("/transcripts/{id}/content", h.GetTranscriptContent)
	r.Get("/transcripts/{id}/collectives", h.ListTranscriptCollectives)
	r.Get("/transcripts/{id}/attestations", h.ListTranscriptAttestations)
	r.Get("/transcript-groups/{groupId}/members", h.ListHelperMembers)
	r.Get("/pulls/{owner}/{name}/{number}", h.GetPullRequestAttachment)
	r.Get("/groups", h.ListGroups)
	r.Post("/groups", h.CreateGroup)
	r.Get("/groups/visible", h.ListVisibleGroups)
	r.Patch("/groups/{id}", h.UpdateGroup)
	r.Get("/groups/{id}/repositories", h.ListRepositories)
	r.Post("/groups/{id}/repositories", h.LinkRepository)
	r.Get("/orgs/{login}", h.GetOrg)
	r.Get("/collectives/{id}", h.GetGroup)
	r.Get("/collectives/{id}/pending", h.ListPendingShares)
	r.Get("/collectives/{id}/my-shares", h.ListMyGroupShares)
	r.Get("/pull/transcripts", h.ListPullableTranscripts)
	r.Get("/pull/transcripts/{id}", h.GetPullTranscript)
	r.Get("/pull/transcripts/{id}/annotations", h.GetPullTranscriptAnnotations)
	r.Get("/pull/transcripts/{id}/content", h.GetPullTranscriptContent)
	r.Get("/users/{username}", h.GetUserPublicProfile)
	r.Get("/users/{username}/projects/{projectHash}", h.GetUserProject)
	return r
}

func (w *poWorld) get(t *testing.T, router http.Handler, actor *AuthUser, target string) (int, []byte) {
	t.Helper()
	return w.send(t, router, actor, http.MethodGet, target, nil)
}

func (w *poWorld) send(t *testing.T, router http.Handler, actor *AuthUser, method, target string, body any) (int, []byte) {
	t.Helper()
	ctx := context.Background()
	if actor != nil {
		ctx = context.WithValue(ctx, UserContextKey, actor)
	}
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, target, bytes.NewReader(raw)).WithContext(ctx)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, request)
	return rec.Code, rec.Body.Bytes()
}

// assertPersonRoutes checks every route that shows the owner in a role other
// than transcript owner: attester, pull request author and requester,
// collective creator, repository linker, helper-group member, and
// organization member.
func (w *poWorld) assertPersonRoutes(t *testing.T, h *Handler, router http.Handler, viewer *AuthUser, c poCase, groupedList []byte) {
	t.Helper()
	check := func(where string, body []byte) { w.assertNoLeak(t, where, body, c) }
	want := c.Expect.Owner

	// Attestations, on their route and on the detail.
	status, body := w.get(t, router, viewer, "/transcripts/"+w.id(w.accepted)+"/attestations")
	if status != http.StatusOK {
		t.Fatalf("attestations answered %d: %s", status, body)
	}
	check("attestations", body)
	var attestations []struct {
		Attester json.RawMessage `json:"attester"`
		OrgLogin string          `json:"org_login"`
	}
	mustDecode(t, body, &attestations)
	status, body = w.get(t, router, viewer, "/transcripts/"+w.id(w.accepted))
	if status != http.StatusOK {
		t.Fatalf("detail answered %d: %s", status, body)
	}
	var detail struct {
		Attestations []struct {
			Attester json.RawMessage `json:"attester"`
			OrgLogin string          `json:"org_login"`
		} `json:"attestations"`
	}
	mustDecode(t, body, &detail)
	for where, rows := range map[string][]struct {
		Attester json.RawMessage `json:"attester"`
		OrgLogin string          `json:"org_login"`
	}{"attestations route": attestations, "detail attestations": detail.Attestations} {
		if len(rows) != 1 {
			t.Fatalf("%s holds %d attestations, want 1", where, len(rows))
		}
		w.assertOwner(t, where+" attester", rows[0].Attester, want)
		wantOrg := w.orgLogin
		if want == "anonymous" {
			wantOrg = ""
		}
		if rows[0].OrgLogin != wantOrg {
			t.Errorf("%s org_login = %q, want %q", where, rows[0].OrgLogin, wantOrg)
		}
	}

	// The pull request attachment the owner authored and asked prompts for.
	status, body = w.get(t, router, viewer, "/pulls/"+w.repoOwner+"/po-repo/7")
	if status != http.StatusOK {
		t.Fatalf("pull request attachment answered %d: %s", status, body)
	}
	check("pull request attachment", body)
	var attachment struct {
		Attachment struct {
			Author      json.RawMessage `json:"author"`
			RequestedBy json.RawMessage `json:"requested_by"`
		} `json:"attachment"`
	}
	mustDecode(t, body, &attachment)
	w.assertOwner(t, "attachment author", attachment.Attachment.Author, want)
	w.assertOwner(t, "attachment requested_by", attachment.Attachment.RequestedBy, want)

	// The helper group under the accepted transcript, through its members route.
	var groupedPayload struct {
		Items []struct {
			HelperGroups []struct {
				GroupID     string `json:"groupId"`
				MemberScope string `json:"memberScope"`
			} `json:"helperGroups"`
		} `json:"items"`
	}
	mustDecode(t, groupedList, &groupedPayload)
	helperGroups := 0
	for _, item := range groupedPayload.Items {
		for _, group := range item.HelperGroups {
			helperGroups++
			status, body = w.get(t, router, viewer, "/transcript-groups/"+group.GroupID+"/members?scope="+url.QueryEscape(group.MemberScope))
			if status != http.StatusOK {
				t.Fatalf("helper members answered %d: %s", status, body)
			}
			check("helper members", body)
			owners := groupedOwners(t, body, "members")
			if len(owners) == 0 {
				t.Errorf("the helper group lists no member")
			}
			for _, owner := range owners {
				w.assertOwner(t, "helper member", owner, want)
			}
		}
	}
	if helperGroups == 0 {
		t.Errorf("the grouped list offers no helper group for the owner's helper session")
	}

	// The organization page lists the owner only as a profile.
	status, body = w.get(t, router, viewer, "/orgs/"+url.PathEscape(w.orgLogin))
	if status != http.StatusOK {
		t.Fatalf("organization answered %d: %s", status, body)
	}
	var org struct {
		Members []struct {
			ID string `json:"id"`
		} `json:"members"`
	}
	mustDecode(t, body, &org)
	listed := false
	for _, member := range org.Members {
		listed = listed || member.ID == w.id(w.owner)
	}
	if listed != (want == "profile") {
		t.Errorf("the organization page lists the owner = %v, want %v", listed, want == "profile")
	}
	if want == "anonymous" {
		// The organization's own login is the page's subject, so only the
		// owner's own identifying values are checked here.
		for label, value := range map[string]string{"username": w.ownerName, "id": w.id(w.owner), "display name": w.displayName, "avatar": w.avatarURL} {
			if bytes.Contains(body, []byte(value)) {
				t.Errorf("the organization page leaks the hidden owner's %s %q", label, value)
			}
		}
	}

	if viewer == nil {
		return
	}

	// Collective records carry their creator: the owner created ownedGroup.
	creatorOf := func(where string, body []byte, required bool) {
		var rows []struct {
			ID      string          `json:"id"`
			Creator json.RawMessage `json:"creator"`
		}
		mustDecode(t, body, &rows)
		found := false
		for _, row := range rows {
			if row.ID == w.id(w.ownedGroup) {
				found = true
				w.assertOwner(t, where+" creator", row.Creator, want)
			}
		}
		if found != required {
			t.Errorf("%s lists the owner's collective = %v, want %v", where, found, required)
		}
	}
	status, body = w.get(t, router, viewer, "/groups/visible")
	if status != http.StatusOK {
		t.Fatalf("visible collectives answered %d: %s", status, body)
	}
	check("visible collectives", body)
	creatorOf("visible collectives", body, true)
	status, body = w.get(t, router, viewer, "/groups")
	if status != http.StatusOK {
		t.Fatalf("your collectives answered %d: %s", status, body)
	}
	check("your collectives", body)
	member := c.Viewer == "owner" || c.Viewer == "collective_owner" || c.Viewer == "member"
	creatorOf("your collectives", body, member)

	// Creating a collective answers with the creator: the viewer themselves.
	status, body = w.send(t, router, viewer, http.MethodPost, "/groups", map[string]any{"name": "po-created-" + w.suffix + "-" + c.Viewer})
	if status != http.StatusCreated {
		t.Fatalf("create collective answered %d: %s", status, body)
	}
	check("create collective", body)
	var created struct {
		ID      string `json:"id"`
		Creator struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		} `json:"creator"`
	}
	mustDecode(t, body, &created)
	if created.Creator.Kind != "profile" || created.Creator.ID != viewer.ID.String() {
		t.Errorf("create collective creator = %+v, want the viewer's own profile", created.Creator)
	}
	if _, err := w.pool.Exec(context.Background(), "DELETE FROM groups WHERE id = $1", created.ID); err != nil {
		t.Errorf("cleanup the created collective: %v", err)
	}

	if c.Viewer == "collective_owner" {
		// A co-owner updating the owner's collective is answered with its creator.
		status, body = w.send(t, router, viewer, http.MethodPatch, "/groups/"+w.id(w.ownedGroup), map[string]any{"description": "updated"})
		if status != http.StatusOK {
			t.Fatalf("update collective answered %d: %s", status, body)
		}
		check("update collective", body)
		var updated struct {
			Creator json.RawMessage `json:"creator"`
		}
		mustDecode(t, body, &updated)
		w.assertOwner(t, "update collective creator", updated.Creator, want)
	}

	if !member {
		return
	}
	// The repository the owner linked, as the collective's members list it.
	f := &fakeGitHub{repoBody: `{"name":"po-linked-2","private":false,"owner":{"login":"po-inst-` + w.suffix + `"}}`, installationID: w.installID}
	newFakeGitHub(t, f)
	client, err := gh.NewClient(gh.Config{AppID: "123", PrivateKeyPEM: testAppPEM(t)}, gh.WithBaseURL(f.srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	h.gh = client
	defer func() { h.gh = nil }()
	status, body = w.get(t, router, viewer, "/groups/"+w.id(w.ownedGroup)+"/repositories")
	if status != http.StatusOK {
		t.Fatalf("linked repositories answered %d: %s", status, body)
	}
	check("linked repositories", body)
	var repositories struct {
		Repositories []struct {
			Name     string          `json:"name"`
			LinkedBy json.RawMessage `json:"linked_by"`
		} `json:"repositories"`
	}
	mustDecode(t, body, &repositories)
	if len(repositories.Repositories) != 1 {
		t.Fatalf("linked repositories lists %d, want the owner's one: %s", len(repositories.Repositories), body)
	}
	w.assertOwner(t, "linked repository linked_by", repositories.Repositories[0].LinkedBy, want)
	if c.Viewer == "owner" {
		// Linking answers with the linker: the owner themselves.
		installation := w.installID
		status, body = w.send(t, router, viewer, http.MethodPost, "/groups/"+w.id(w.ownedGroup)+"/repositories",
			map[string]any{"owner": "po-inst-" + w.suffix, "name": "po-linked-2", "installation_id": installation})
		if status != http.StatusCreated {
			t.Fatalf("link repository answered %d: %s", status, body)
		}
		var linked struct {
			LinkedBy json.RawMessage `json:"linked_by"`
		}
		mustDecode(t, body, &linked)
		w.assertOwner(t, "link repository linked_by", linked.LinkedBy, "profile")
	}
}

// wantProfile is the exact profile object of the world's owner.
func (w *poWorld) wantProfile() map[string]any {
	return map[string]any{"kind": "profile", "id": w.id(w.owner), "username": w.ownerName, "display_name": w.displayName, "avatar_url": w.avatarURL}
}

// assertOwner checks one owner object against the case: the exact profile of
// the world's owner, or exactly {"kind":"anonymous"}.
func (w *poWorld) assertOwner(t *testing.T, where string, raw json.RawMessage, want string) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("%s: owner is not an object: %s", where, raw)
	}
	expected := map[string]any{"kind": "anonymous"}
	if want == "profile" {
		expected = w.wantProfile()
	}
	if !reflect.DeepEqual(got, expected) {
		t.Errorf("%s: owner = %v, want %v", where, got, expected)
	}
}

// assertNoLeak refuses a response that carries any identifying value of the
// owner when the case expects them to be anonymous.
func (w *poWorld) assertNoLeak(t *testing.T, where string, body []byte, c poCase) {
	t.Helper()
	if c.Expect.Owner != "anonymous" {
		return
	}
	values := map[string]string{"username": w.ownerName, "id": w.id(w.owner), "display name": w.displayName, "avatar": w.avatarURL, "organization": w.orgLogin, "project hash": w.projectHash, "project name": "po-project"}
	for i, value := range withheldContentValues {
		values["content repository "+string(rune('a'+i))] = value
	}
	for label, value := range values {
		if bytes.Contains(body, []byte(value)) {
			t.Errorf("%s leaks the hidden owner's %s %q to the %s viewer: %s", where, label, value, c.Viewer, body)
		}
	}
}

func TestPublicOwnerVisibilityMatrix(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	for _, c := range loadPublicOwnerCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			h := New(&config.Config{}, pool, nil)
			blobs := newFakeBlobStore()
			h.blobs = blobs
			router := publicOwnerRouter(h)
			w := newPublicOwnerWorld(t, ctx, pool, h, c)
			blobs.put(w.contentKey, contentWithRepositoryJSON())
			blobs.put(w.sharedContentKey, contentWithRepositoryJSON())
			defer w.cleanup(t, ctx)
			viewer := w.actor(c.Viewer)
			check := func(where string, body []byte) { w.assertNoLeak(t, where, body, c) }

			// The batched read policy the parent resolution uses must agree with
			// the per-transcript policy for this viewer on every transcript.
			ids := []pgtype.UUID{w.accepted, w.shared, w.pending, w.private}
			readable, err := h.queries.ListReadableTranscriptIDs(ctx, sqlc.ListReadableTranscriptIDsParams{TranscriptIds: ids, ViewerID: viewerID(viewer)})
			if err != nil {
				t.Fatal(err)
			}
			batched := map[pgtype.UUID]bool{}
			for _, id := range readable {
				batched[id] = true
			}
			for _, id := range ids {
				row, err := h.queries.GetTranscriptByID(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				single, err := h.transcriptViewAccess(ctx, viewer, row)
				if err != nil {
					t.Fatal(err)
				}
				if single != batched[id] {
					t.Errorf("transcript %s: the per-transcript read policy says %v and the batched one %v for the %s viewer", w.id(id), single, batched[id], c.Viewer)
				}
			}

			// Transcript detail: owner and owner_orgs.
			status, body := w.get(t, router, viewer, "/transcripts/"+w.id(w.accepted))
			if status != http.StatusOK {
				t.Fatalf("detail answered %d: %s", status, body)
			}
			check("detail", body)
			var detail struct {
				Owner      json.RawMessage   `json:"owner"`
				OwnerOrgs  []json.RawMessage `json:"owner_orgs"`
				Transcript map[string]any    `json:"transcript"`
			}
			mustDecode(t, body, &detail)
			w.assertOwner(t, "detail", detail.Owner, c.Expect.Owner)
			if wantOrgs := map[string]int{"profile": 1, "anonymous": 0}[c.Expect.Owner]; len(detail.OwnerOrgs) != wantOrgs || detail.OwnerOrgs == nil {
				t.Errorf("detail owner_orgs = %v, want %d entries and never null", detail.OwnerOrgs, wantOrgs)
			}
			if _, present := detail.Transcript["owner_id"]; present {
				t.Errorf("detail transcript still carries owner_id")
			}

			// Flat list, filtered to this world's sessions.
			status, body = w.get(t, router, viewer, "/transcripts?limit=100&q="+url.QueryEscape("posession"))
			if status != http.StatusOK {
				t.Fatalf("list answered %d: %s", status, body)
			}
			check("list", body)
			var list struct {
				Transcripts []struct {
					Owner     json.RawMessage   `json:"owner"`
					OwnerOrgs []json.RawMessage `json:"owner_orgs"`
					Shares    []struct {
						GroupID string `json:"group_id"`
					} `json:"shares"`
					Transcript struct {
						ID string `json:"id"`
					} `json:"transcript"`
				} `json:"transcripts"`
			}
			mustDecode(t, body, &list)
			listed := 0
			for _, row := range list.Transcripts {
				if row.Transcript.ID != w.id(w.accepted) && row.Transcript.ID != w.id(w.shared) && row.Transcript.ID != w.id(w.pending) {
					continue
				}
				listed++
				w.assertOwner(t, "list row", row.Owner, c.Expect.Owner)
				// A row discloses exactly the collectives the dedicated route
				// discloses to this viewer (the owner keeps all of them).
				if c.Viewer != "owner" {
					_, collectivesBody := w.get(t, router, viewer, "/transcripts/"+row.Transcript.ID+"/collectives")
					var dedicated struct {
						Collectives []struct {
							ID string `json:"id"`
						} `json:"collectives"`
					}
					mustDecode(t, collectivesBody, &dedicated)
					want := map[string]bool{}
					for _, collective := range dedicated.Collectives {
						want[collective.ID] = true
					}
					if len(row.Shares) != len(want) {
						t.Errorf("list row %s discloses %d collectives, the dedicated route %d", row.Transcript.ID, len(row.Shares), len(want))
					}
					for _, share := range row.Shares {
						if !want[share.GroupID] {
							t.Errorf("list row %s discloses collective %s the dedicated route withholds", row.Transcript.ID, share.GroupID)
						}
					}
				}
				if c.Expect.Owner == "anonymous" && len(row.OwnerOrgs) != 0 {
					t.Errorf("list row lists organizations beside an anonymous owner: %s", body)
				}
			}
			if listed == 0 {
				t.Fatalf("the list shows none of the world's transcripts: %s", body)
			}

			// Grouped list.
			status, body = w.get(t, router, viewer, "/transcripts?view=grouped&limit=100&q="+url.QueryEscape("posession"))
			groupedListBody := body
			if status != http.StatusOK {
				t.Fatalf("grouped list answered %d: %s", status, body)
			}
			check("grouped list", body)
			for i, owner := range groupedOwners(t, body, "items") {
				w.assertOwner(t, "grouped row "+string(rune('0'+i)), owner, c.Expect.Owner)
			}

			// The person and repository filters, from the filter fixture.
			for _, filterCase := range loadPublicOwnerFilters(t) {
				filter := strings.NewReplacer(
					"{owner_name}", url.QueryEscape(w.ownerName),
					"{org_login}", url.QueryEscape(w.orgLogin),
					"{project_hash}", url.QueryEscape(w.projectHash),
				).Replace(filterCase.Query)
				for _, view := range filterCase.Views {
					if view == "grouped" {
						view = "&view=grouped"
					} else {
						view = ""
					}
					status, body = w.get(t, router, viewer, "/transcripts?limit=100&"+filter+view)
					if status != http.StatusOK {
						t.Fatalf("filtered list %s%s answered %d: %s", filter, view, status, body)
					}
					var count struct {
						Transcripts []json.RawMessage `json:"transcripts"`
						Items       []json.RawMessage `json:"items"`
					}
					mustDecode(t, body, &count)
					got := len(count.Transcripts) + len(count.Items)
					if (c.Expect.Filter == "match") != (got > 0) {
						t.Errorf("filter %s%s returned %d rows to the %s viewer, want %s", filter, view, got, c.Viewer, c.Expect.Filter)
					}
				}
			}

			// Flat and grouped collective detail: library rows, roster, contributors.
			status, body = w.get(t, router, viewer, "/collectives/"+w.id(w.group))
			if status != http.StatusOK {
				t.Fatalf("collective answered %d: %s", status, body)
			}
			check("collective", body)
			var flat struct {
				Members []struct {
					Member     json.RawMessage `json:"member"`
					GithubOrgs []string        `json:"github_orgs"`
				} `json:"members"`
				Contributors []struct {
					Contributor json.RawMessage `json:"contributor"`
				} `json:"contributors"`
				Transcripts []struct {
					ID    string          `json:"id"`
					Owner json.RawMessage `json:"owner"`
				} `json:"transcripts"`
			}
			mustDecode(t, body, &flat)
			for _, row := range flat.Transcripts {
				w.assertOwner(t, "collective library row", row.Owner, c.Expect.Owner)
			}
			if len(flat.Transcripts) == 0 {
				t.Errorf("the public collective library is empty to the %s viewer", c.Viewer)
			}
			w.assertRoster(t, "collective roster", flat.Members, flat.Contributors, c.Expect.Roster)

			status, body = w.get(t, router, viewer, "/collectives/"+w.id(w.group)+"?view=grouped")
			if status != http.StatusOK {
				t.Fatalf("grouped collective answered %d: %s", status, body)
			}
			check("grouped collective", body)
			var grouped struct {
				Members []struct {
					Member     json.RawMessage `json:"member"`
					GithubOrgs []string        `json:"github_orgs"`
				} `json:"members"`
				Contributors []struct {
					Contributor json.RawMessage `json:"contributor"`
				} `json:"contributors"`
				TranscriptList json.RawMessage `json:"transcriptList"`
			}
			mustDecode(t, body, &grouped)
			owners := groupedOwners(t, grouped.TranscriptList, "items")
			if len(owners) == 0 {
				t.Errorf("the grouped collective library is empty to the %s viewer", c.Viewer)
			}
			for _, owner := range owners {
				w.assertOwner(t, "grouped collective row", owner, c.Expect.Owner)
			}
			w.assertRoster(t, "grouped collective roster", grouped.Members, grouped.Contributors, c.Expect.Roster)

			// relation_owner's own collective: its owner sees every member and
			// join request as a profile, whatever the transcript rule says.
			if c.Viewer == "relation_owner" && c.Expect.RelationRoster != "none" {
				status, body = w.get(t, router, viewer, "/collectives/"+w.id(w.relGroup))
				if status != http.StatusOK {
					t.Fatalf("relation collective answered %d: %s", status, body)
				}
				var roster struct {
					Members        []struct{ Member json.RawMessage } `json:"members"`
					PendingMembers []struct{ Member json.RawMessage } `json:"pending_members"`
				}
				mustDecode(t, body, &roster)
				found := false
				for _, row := range append(roster.Members, roster.PendingMembers...) {
					var person map[string]any
					mustDecode(t, row.Member, &person)
					if person["kind"] != "profile" {
						t.Errorf("relation collective roster shows its own owner an anonymous member: %s", row.Member)
					}
					if person["id"] == w.id(w.owner) {
						found = true
						w.assertOwner(t, "relation collective roster", row.Member, "profile")
					}
				}
				if found != (c.Expect.RelationRoster == "profile") {
					t.Errorf("relation collective roster lists the owner = %v, want %s", found, c.Expect.RelationRoster)
				}
			}

			// Review queue, flat and grouped. Mounted behind sign-in, so a
			// signed-out viewer never reaches the handler.
			for _, view := range []string{"", "?view=grouped"} {
				if viewer == nil {
					break
				}
				status, body = w.get(t, router, viewer, "/collectives/"+w.id(w.group)+"/pending"+view)
				if !c.Expect.Review {
					if status == http.StatusOK {
						t.Errorf("the %s viewer read the review queue", c.Viewer)
					}
					continue
				}
				if status != http.StatusOK {
					t.Fatalf("review queue%s answered %d: %s", view, status, body)
				}
				check("review queue", body)
				var rows []json.RawMessage
				if view == "" {
					var flatRows []struct {
						Owner json.RawMessage `json:"owner"`
					}
					mustDecode(t, body, &flatRows)
					for _, row := range flatRows {
						rows = append(rows, row.Owner)
					}
				} else {
					rows = groupedOwners(t, body, "items")
				}
				if len(rows) != 1 {
					t.Fatalf("review queue%s holds %d rows, want the one pending submission: %s", view, len(rows), body)
				}
				w.assertOwner(t, "review queue"+view, rows[0], c.Expect.Owner)
			}

			// The owner's own submissions.
			if c.Expect.MyShares {
				for _, view := range []string{"", "?view=grouped"} {
					status, body = w.get(t, router, viewer, "/collectives/"+w.id(w.group)+"/my-shares"+view)
					if status != http.StatusOK {
						t.Fatalf("my-shares%s answered %d: %s", view, status, body)
					}
					var rows []json.RawMessage
					if view == "" {
						var flatRows []struct {
							Owner json.RawMessage `json:"owner"`
						}
						mustDecode(t, body, &flatRows)
						for _, row := range flatRows {
							rows = append(rows, row.Owner)
						}
					} else {
						rows = groupedOwners(t, body, "items")
					}
					if len(rows) != 3 {
						t.Fatalf("my-shares%s holds %d rows, want 3: %s", view, len(rows), body)
					}
					for _, owner := range rows {
						w.assertOwner(t, "my-shares"+view, owner, "profile")
					}
				}
			}

			// Stored content: the session detail's repository identity.
			status, body = w.get(t, router, viewer, "/transcripts/"+w.id(w.accepted)+"/content")
			if status != http.StatusOK {
				t.Fatalf("content answered %d: %s", status, body)
			}
			check("content", body)
			for _, value := range withheldContentValues {
				if contains := bytes.Contains(body, []byte(value)); contains != (c.Expect.Owner == "profile") {
					t.Errorf("content carries %q = %v, want %v for the %s viewer", value, contains, c.Expect.Owner == "profile", c.Viewer)
				}
			}

			// Annotations on the web route.
			status, body = w.get(t, router, viewer, "/transcripts/"+w.id(w.accepted)+"/annotations")
			if status != http.StatusOK {
				t.Fatalf("annotations answered %d: %s", status, body)
			}
			check("annotations", body)
			var web struct {
				Annotations []struct {
					AnnotatorName     string  `json:"annotatorName"`
					TargetProjectHash *string `json:"targetProjectHash"`
				} `json:"annotations"`
			}
			mustDecode(t, body, &web)
			for _, row := range web.Annotations {
				w.assertTargetProject(t, "annotations", row.TargetProjectHash, c.Expect.Owner)
			}
			w.assertAnnotators(t, "annotations", len(web.Annotations), func(i int) string { return web.Annotations[i].AnnotatorName }, c.Expect.Annotator)

			// The pull surface.
			if viewer != nil {
				status, body = w.get(t, router, viewer, "/pull/transcripts?limit=200")
				if status != http.StatusOK {
					t.Fatalf("pull list answered %d: %s", status, body)
				}
				check("pull list", body)
				var pullList struct {
					Transcripts []struct {
						TranscriptID string          `json:"transcriptId"`
						Owner        json.RawMessage `json:"owner"`
					} `json:"transcripts"`
				}
				mustDecode(t, body, &pullList)
				pulled := false
				for _, row := range pullList.Transcripts {
					if row.TranscriptID == w.id(w.shared) {
						pulled = true
						w.assertOwner(t, "pull list row", row.Owner, c.Expect.Owner)
					}
				}
				if pulled != c.Expect.Pull {
					t.Errorf("pull list includes the shared transcript = %v, want %v", pulled, c.Expect.Pull)
				}
			}
			if c.Expect.Pull {
				status, body = w.get(t, router, viewer, "/pull/transcripts/"+w.id(w.shared))
				if status != http.StatusOK {
					t.Fatalf("pull metadata answered %d: %s", status, body)
				}
				check("pull metadata", body)
				var info struct {
					Owner json.RawMessage `json:"owner"`
				}
				mustDecode(t, body, &info)
				w.assertOwner(t, "pull metadata", info.Owner, c.Expect.Owner)

				status, body = w.get(t, router, viewer, "/pull/transcripts/"+w.id(w.shared)+"/content")
				if status != http.StatusOK {
					t.Fatalf("pull content answered %d: %s", status, body)
				}
				check("pull content", body)
				for _, value := range withheldContentValues {
					if contains := bytes.Contains(body, []byte(value)); contains != (c.Expect.Owner == "profile") {
						t.Errorf("pull content carries %q = %v, want %v for the %s viewer", value, contains, c.Expect.Owner == "profile", c.Viewer)
					}
				}

				status, body = w.get(t, router, viewer, "/pull/transcripts/"+w.id(w.shared)+"/annotations")
				if status != http.StatusOK {
					t.Fatalf("pull annotations answered %d: %s", status, body)
				}
				check("pull annotations", body)
				var pulledAnnotations []struct {
					Author            json.RawMessage `json:"author"`
					AnnotatorName     string          `json:"annotatorName"`
					TargetProjectHash *string         `json:"targetProjectHash"`
				}
				mustDecode(t, body, &pulledAnnotations)
				for _, row := range pulledAnnotations {
					w.assertOwner(t, "pull annotation author", row.Author, c.Expect.Owner)
					w.assertTargetProject(t, "pull annotations", row.TargetProjectHash, c.Expect.Owner)
				}
				w.assertAnnotators(t, "pull annotations", len(pulledAnnotations), func(i int) string { return pulledAnnotations[i].AnnotatorName }, c.Expect.Annotator)
			}

			// The profile and project pages keep their own rule and narrow the row.
			for _, target := range []string{"/users/" + w.ownerName, "/users/" + w.ownerName + "/projects/" + w.projectHash} {
				status, body = w.get(t, router, viewer, target)
				check(target, body)
				if c.Expect.ProfilePage == "not_found" {
					if status != http.StatusNotFound {
						t.Errorf("%s answered %d to the %s viewer, want 404", target, status, c.Viewer)
					}
					continue
				}
				if status != http.StatusOK {
					t.Fatalf("%s answered %d: %s", target, status, body)
				}
				raw := json.RawMessage(body)
				if strings.Contains(target, "/projects/") {
					var page struct {
						Owner json.RawMessage `json:"owner"`
					}
					mustDecode(t, body, &page)
					raw = page.Owner
				}
				w.assertOwner(t, target, raw, "profile")
			}

			w.assertPersonRoutes(t, h, router, viewer, c, groupedListBody)
		})
	}
}

// assertRoster checks the roster and contributor list: the owner is listed as
// their exact profile (with organizations), or not listed at all.
func (w *poWorld) assertRoster(t *testing.T, where string, members []struct {
	Member     json.RawMessage `json:"member"`
	GithubOrgs []string        `json:"github_orgs"`
}, contributors []struct {
	Contributor json.RawMessage `json:"contributor"`
}, want string) {
	t.Helper()
	ownerID := w.id(w.owner)
	memberFound, contributorFound := false, false
	for _, row := range members {
		var person map[string]any
		mustDecode(t, row.Member, &person)
		if person["kind"] != "profile" {
			if len(row.GithubOrgs) != 0 {
				t.Errorf("%s lists organizations beside an anonymous member", where)
			}
			continue
		}
		if person["id"] == ownerID {
			memberFound = true
			w.assertOwner(t, where+" member", row.Member, "profile")
			if !containsString(row.GithubOrgs, w.orgLogin) {
				t.Errorf("%s dropped the owner's organization: %v", where, row.GithubOrgs)
			}
		}
	}
	for _, row := range contributors {
		var person map[string]any
		mustDecode(t, row.Contributor, &person)
		if person["id"] == ownerID {
			contributorFound = true
			w.assertOwner(t, where+" contributor", row.Contributor, "profile")
		}
	}
	if (want == "profile") != memberFound || (want == "profile") != contributorFound {
		t.Errorf("%s lists the owner as member=%v contributor=%v, want %s", where, memberFound, contributorFound, want)
	}
}

// assertTargetProject checks an annotation's target project: the owner's
// project hash beside a profile owner, absent beside an anonymous one.
func (w *poWorld) assertTargetProject(t *testing.T, where string, got *string, owner string) {
	t.Helper()
	if owner == "anonymous" {
		if got != nil {
			t.Errorf("%s names the anonymous owner's target project %q", where, *got)
		}
		return
	}
	if got == nil || *got != w.projectHash {
		t.Errorf("%s target project = %v, want %q", where, got, w.projectHash)
	}
}

func (w *poWorld) assertAnnotators(t *testing.T, where string, n int, name func(int) string, want string) {
	t.Helper()
	if n == 0 {
		t.Fatalf("%s returned no annotation", where)
	}
	expected := w.ownerName
	if want == "anon" {
		expected = anonymousAnnotatorName
	}
	for i := 0; i < n; i++ {
		if name(i) != expected {
			t.Errorf("%s annotator %d = %q, want %q", where, i, name(i), expected)
		}
	}
}

// groupedOwners returns every transcript row owner in a grouped list body,
// and checks each route arm carries the same owner as its row.
func groupedOwners(t *testing.T, body []byte, key string) []json.RawMessage {
	t.Helper()
	var payload map[string]json.RawMessage
	mustDecode(t, body, &payload)
	var items []struct {
		Transcript *struct {
			Owner      json.RawMessage            `json:"owner"`
			Collective map[string]json.RawMessage `json:"collective"`
			Pending    map[string]json.RawMessage `json:"pending"`
			MyShare    map[string]json.RawMessage `json:"myShare"`
		} `json:"transcript"`
	}
	if raw, ok := payload[key]; ok {
		mustDecode(t, raw, &items)
	}
	owners := []json.RawMessage{}
	for _, item := range items {
		if item.Transcript == nil {
			continue
		}
		owners = append(owners, item.Transcript.Owner)
		for arm, fields := range map[string]map[string]json.RawMessage{"collective": item.Transcript.Collective, "pending": item.Transcript.Pending, "myShare": item.Transcript.MyShare} {
			if fields == nil {
				continue
			}
			var a, b any
			mustDecode(t, fields["owner"], &a)
			mustDecode(t, item.Transcript.Owner, &b)
			if !reflect.DeepEqual(a, b) {
				t.Errorf("grouped %s arm owner %s differs from the row owner %s", arm, fields["owner"], item.Transcript.Owner)
			}
		}
	}
	return owners
}

func mustDecode(t *testing.T, body []byte, into any) {
	t.Helper()
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("decode %T: %v (body: %s)", into, err, body)
	}
}
