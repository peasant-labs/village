//go:build integration

package handler

// One project, one name, every viewer, every surface — against a real PostgreSQL.
//
// The village stores exactly ONE form of a project's evidence: a local path
// arrives already redacted from the publishing client, there is no raw column
// beside it, and no surface masks anything at render time. The user-visible
// consequence is that the answer to "what is this project called" cannot depend
// on who is asking. This file is what holds that consequence: every case in
// testdata/project-name-viewers.yaml names ONE expected display name, and the
// test renders it through the owner, a stranger and a signed-out reader on the
// project page, the profile list, a transcript's detail, the public explore
// list, and the answer the correction routes send back.
//
// It must be a real database. The evidence it varies is STORED evidence — the
// deterministic project_path pick inside ListOwnerProjectIdentities, the owner
// override join, and the per-surface visibility predicate — and a mocked
// querier would return whatever the test handed it, proving nothing about any
// of the three.

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/projectname"
	"github.com/peasant-labs/village/backend/internal/storage"
)

//go:embed testdata/project-name-viewers.yaml
var projectNameViewersYAML []byte

// projectNameViewer is one row of the fixture's viewer axis.
type projectNameViewer struct {
	Name string `yaml:"name"`
	Why  string `yaml:"why"`
}

// projectNameSurface is one row of the fixture's surface axis. OwnerOnly marks a
// surface only the owner can reach at all (the correction routes require
// authentication and ownership), so the matrix skips the other two viewers there
// rather than asserting a refusal is a rendered name.
type projectNameSurface struct {
	Name      string `yaml:"name"`
	Why       string `yaml:"why"`
	OwnerOnly bool   `yaml:"owner_only"`
}

// projectNameViewerCase is one project's stored evidence and the single name
// every viewer must read on every surface.
type projectNameViewerCase struct {
	Name string `yaml:"name"`
	Why  string `yaml:"why"`
	// ProjectPath is the ALREADY-REDACTED local path the publishing client
	// recorded, stored verbatim.
	ProjectPath string `yaml:"project_path"`
	// GitRemote is the raw remote, or "" when the project has none.
	GitRemote string `yaml:"git_remote"`
	// PublishedProjectName is the project name the publisher disclosed on the
	// transcript, or "" to disclose none.
	PublishedProjectName string `yaml:"published_project_name"`
	// OwnerOverrideName is a stored owner rename, or "" for none.
	OwnerOverrideName string `yaml:"owner_override_name"`
	WantDisplayName   string `yaml:"want_display_name"`
	WantNameSource    string `yaml:"want_name_source"`
	// SeedThroughPublish makes the case store its evidence by POSTing a real
	// publish to the mounted publish route instead of by writing the row
	// directly. A direct write can only prove that a STORED value is rendered
	// unchanged; it assumes the step before it, that a published path reaches
	// the column unchanged. A case that sets this proves both halves of "what
	// the client sent is what every viewer reads".
	SeedThroughPublish bool `yaml:"seed_through_publish"`
}

type projectNameViewersFixture struct {
	Viewers  []projectNameViewer     `yaml:"viewers"`
	Surfaces []projectNameSurface    `yaml:"surfaces"`
	Cases    []projectNameViewerCase `yaml:"cases"`
}

// The three deletion-protection manifests, asserted as EXACT membership.
// Deleting a viewer, a surface or a case silently shrinks the matrix without
// failing anything, so each axis names what must be there. These are name
// manifests rather than counts: a count cannot say WHICH viewer or surface
// stopped being covered.
var (
	requiredProjectNameViewers  = []string{"owner", "other_user", "signed_out"}
	requiredProjectNameSurfaces = []string{"project_page", "profile", "transcript_detail", "explore", "override_response"}
	requiredProjectNameCases    = []string{
		"path_only_renders_the_stored_path_to_everyone",
		"remote_beats_path_for_everyone",
		"owner_rename_beats_path_for_everyone",
		"wire_path_stored_verbatim",
		"no_path_falls_through_to_the_privacy_label",
	}
)

func loadProjectNameViewersFixture(t *testing.T) projectNameViewersFixture {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(projectNameViewersYAML))
	decoder.KnownFields(true)
	var fixture projectNameViewersFixture
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("decode the strict project-name-viewers fixture: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("the project-name-viewers fixture must contain exactly one YAML document: %v", err)
	}

	viewers := map[string]struct{}{}
	for _, v := range fixture.Viewers {
		requireFixtureReason(t, "viewer", v.Name, v.Why)
		viewers[v.Name] = struct{}{}
	}
	assertExactTitleFixtureNames(t, "project-name-viewers viewers", viewers, requiredProjectNameViewers)

	surfaces := map[string]struct{}{}
	for _, s := range fixture.Surfaces {
		requireFixtureReason(t, "surface", s.Name, s.Why)
		surfaces[s.Name] = struct{}{}
	}
	assertExactTitleFixtureNames(t, "project-name-viewers surfaces", surfaces, requiredProjectNameSurfaces)

	cases := map[string]struct{}{}
	for _, c := range fixture.Cases {
		requireFixtureReason(t, "case", c.Name, c.Why)
		cases[c.Name] = struct{}{}
		if c.WantDisplayName == "" || c.WantNameSource == "" {
			t.Fatalf("project-name-viewers case %q names no expected display name or source; the whole point of a "+
				"case here is the ONE string every viewer must read", c.Name)
		}
	}
	assertExactTitleFixtureNames(t, "project-name-viewers cases", cases, requiredProjectNameCases)
	return fixture
}

func requireFixtureReason(t *testing.T, kind, name, why string) {
	t.Helper()
	if name == "" {
		t.Fatalf("the project-name-viewers fixture carries a %s with an empty name", kind)
	}
	if strings.TrimSpace(why) == "" {
		t.Fatalf("project-name-viewers %s %q states no reason it exists; a row nobody can justify cannot be "+
			"maintained", kind, name)
	}
}

// projectNameWorld is one case's owner, stranger, project and transcript.
type projectNameWorld struct {
	pool         *pgxpool.Pool
	owner        pgtype.UUID
	ownerName    string
	other        pgtype.UUID
	projectHash  string
	transcript   pgtype.UUID
	transcriptID uuid.UUID
	// title is unique per case so the public explore list — which is not
	// scoped to an owner — can be narrowed to this world's own transcript
	// without narrowing it by owner and thereby testing the profile path twice.
	title string
	// publisher and blobs are set only for a case seeded through the real
	// publish route; the published object has to be removed again afterwards.
	publisher *Handler
	blobs     storage.TranscriptBlobStore
}

// seedProjectNameWorldDirectly writes the case's stored evidence as one row.
// It is the right seed for a case whose subject is what the DATABASE holds —
// the deterministic project_path pick, the NULL-versus-empty distinction, the
// owner-override join — none of which needs a publish to reach that state.
func seedProjectNameWorldDirectly(t *testing.T, ctx context.Context, w *projectNameWorld, c projectNameViewerCase, suffix string) {
	t.Helper()
	localID := "pn-" + suffix
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		"SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)",
		database.SystemActorID); err != nil {
		t.Fatal(err)
	}
	w.transcriptID = uuid.New()
	w.transcript = toPgUUID(w.transcriptID)
	// Every optional piece of evidence is written as a nullable value so a case
	// that discloses nothing stores NULL rather than an empty string: the
	// deterministic pick in ListOwnerProjectIdentities filters on both, and a
	// fixture that could only ever store "" would never exercise the NULL half.
	if _, err := tx.Exec(ctx, `
		INSERT INTO transcripts (id, owner_id, local_id, title, visibility, model_provider, model_name, blob_key,
		                         blob_size_bytes, schema_version, content_hash, wrapped_data_key, encryption_algorithm,
		                         key_version, project_hash, project_name, git_remote, project_path)
		VALUES ($1,$2,$3,$4,'public','claude-code',$5,$6,$7,'0.1.0',$8,$9,'aes-256-gcm-random-nonce-v1',1,$10,$11,$12,$13)
	`, w.transcript, w.owner, localID, w.title, "m-"+suffix, "blob/"+suffix, int64(len(suffix)),
		"hash-"+suffix, []byte("fixture-wrapped-data-key"), w.projectHash,
		nullableText(c.PublishedProjectName), nullableText(c.GitRemote), nullableText(c.ProjectPath)); err != nil {
		t.Fatalf("store the case's transcript: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// seedProjectNameWorldThroughPublish stores the case's evidence the way a real
// contributor does: it POSTs a publish to the mounted publish route, with the
// case's redacted project path in the request, and then makes the transcript
// public through the mounted update route.
//
// This closes the one step a direct row write has to assume. The village's
// promise is that the path a client sends is the path every viewer reads; a
// seeded row can only show the second half of that. Here the value is carried
// by a request, written by the publish handler, and read back from the column
// before any surface renders it, so a publish-side rewrite, truncation or mask
// fails the case instead of being invisible to it.
//
// It needs the same real object storage every publish needs, so it skips when
// none is configured and fails in CI, which is how every other mounted publish
// test in this package behaves. The encrypted aggregate configures that storage
// and rejects a skip, so the case cannot quietly stop running there.
func seedProjectNameWorldThroughPublish(t *testing.T, ctx context.Context, w *projectNameWorld, c projectNameViewerCase) {
	t.Helper()
	w.blobs = authoritativeTestBlobStore(t)
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct the real title pipeline the publish route uses: %v", err)
	}
	w.publisher = &Handler{
		pool:    w.pool,
		queries: sqlc.New(w.pool),
		blobs:   w.blobs,
		titles:  titles,
		cfg:     &config.Config{FrontendURL: "https://village.example"},
	}
	user := &AuthUser{ID: uuid.UUID(w.owner.Bytes), Username: w.ownerName}

	content := `{"contractVersion":"0.1.0","kind":"session_detail","sessionDetail":{"id":"project-name-viewers","harness":"claude-code","turns":[]}}`
	title := w.title
	quality := schema.AuthoritativeQualityMetrics(schema.QualityMetrics{TitleGenerated: &title})
	request := schema.AuthoritativePublishRequest{
		Identity:  schema.AuthoritativeSessionIdentity{SessionID: schema.SessionID(uuid.NewString()), SchemaVersion: 2},
		Model:     schema.AuthoritativeModelInfo{Harness: schema.HarnessClaudeCode, Model: "fixture-model"},
		Timestamp: schema.AuthoritativeTimestampInfo{Start: 1700000000000, End: 1700000001000},
		Source:    schema.AuthoritativeSourceInfo{FilePath: "/fixture/session.jsonl", Format: schema.SourceFormatJSONL},
		Git:       schema.AuthoritativeGitContext{Remote: optionalPublishedText(c.GitRemote)},
		// FilePath is the already-redacted local path. It is the value under
		// test: everything below reads it back rather than trusting the send.
		Project:          schema.AuthoritativeProjectContext{Hash: schema.ProjectHash(w.projectHash), FilePath: c.ProjectPath, Name: c.PublishedProjectName},
		Quality:          &quality,
		License:          schema.LicenseCCBY,
		ContentHash:      schema.ComputeTranscriptContentHash([]byte(content)),
		VisibilityIntent: schema.VisibilityIntentPrivate,
	}
	metadata, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("encode the publish request: %v", err)
	}
	body, boundary := multipartBody(t, map[string]string{"metadata": string(metadata)}, content)
	publish := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	publish.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	publish = publish.WithContext(context.WithValue(ctx, UserContextKey, user))
	published := httptest.NewRecorder()
	w.publisher.PublishTranscript(published, publish)
	if published.Code != http.StatusCreated {
		t.Fatalf("publishing this case's transcript answered %d, so nothing was stored to render: %s",
			published.Code, published.Body.String())
	}
	var receipt schema.AuthoritativePublishResponse
	if err := json.Unmarshal(published.Body.Bytes(), &receipt); err != nil {
		t.Fatalf("decode the publish receipt %q: %v", published.Body.String(), err)
	}
	w.transcriptID = uuid.MustParse(receipt.TranscriptID.String())
	w.transcript = toPgUUID(w.transcriptID)

	// The public surfaces in the matrix (explore, and every signed-out read)
	// only answer for a public transcript, and a publish always stores the
	// private default. The owner makes it public through the same route a
	// person uses, so no visibility is written behind the handlers' backs.
	makePublic := httptest.NewRequest(http.MethodPatch, "/api/v1/transcripts/"+w.transcriptID.String(),
		strings.NewReader(`{"visibility":"public"}`))
	makePublic = withChiURLParam(makePublic, "id", w.transcriptID.String())
	makePublic = makePublic.WithContext(context.WithValue(makePublic.Context(), UserContextKey, user))
	madePublic := httptest.NewRecorder()
	w.publisher.UpdateTranscript(madePublic, makePublic)
	if madePublic.Code != http.StatusOK {
		t.Fatalf("making this case's published transcript public answered %d, so the public surfaces in the matrix "+
			"would have had nothing to render: %s", madePublic.Code, madePublic.Body.String())
	}

	// Read the column back before any surface runs. This is the persist half of
	// the guarantee, and it is asserted against the value the request carried,
	// not against a value re-derived here.
	stored, err := w.publisher.queries.GetTranscriptByID(ctx, w.transcript)
	if err != nil {
		t.Fatalf("re-read the transcript this case published: %v", err)
	}
	if stored.ProjectPath.String != c.ProjectPath {
		t.Fatalf("the publish route carried the project path %q and stored %q. What this means: the village "+
			"rewrote an already-redacted path on the way in, so every viewer of this project reads something the "+
			"contributor never sent, and the redaction the client performed no longer describes what is published. "+
			"Detected while seeding the project-name matrix through the mounted publish route, before any surface "+
			"rendered. How to fix it: store schema PublishRequest.Project.FilePath verbatim in "+
			"handler.schemaToTranscriptParams and add no path transform to the publish path; this milestone "+
			"applies no village-side path guard on purpose.", c.ProjectPath, stored.ProjectPath.String)
	}
	if stored.GitRemote.String != c.GitRemote || stored.ProjectName.String != c.PublishedProjectName {
		t.Fatalf("the publish route carried the remote %q and the project name %q but stored %q and %q; the case's "+
			"other evidence must reach the row unchanged too, or the name this matrix compares is resolved from "+
			"evidence the case never described", c.GitRemote, c.PublishedProjectName,
			stored.GitRemote.String, stored.ProjectName.String)
	}
	// The stored title is what the explore surface is narrowed by, so it is
	// taken from the row rather than assumed to survive the title pipeline.
	w.title = stored.Title.String
}

// optionalPublishedText renders "the case discloses nothing" as an absent
// optional field rather than as an empty string, which is what a client that
// has no git remote actually sends.
func optionalPublishedText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func newProjectNameWorld(t *testing.T, ctx context.Context, pool *pgxpool.Pool, c projectNameViewerCase) *projectNameWorld {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	w := &projectNameWorld{
		pool:        pool,
		ownerName:   "pn-owner-" + suffix,
		projectHash: strings.Repeat("c", 52) + suffix,
		title:       "project name viewer matrix " + suffix,
	}
	w.owner = shareInsertUser(t, ctx, pool, w.ownerName)
	w.other = shareInsertUser(t, ctx, pool, "pn-other-"+suffix)

	if c.SeedThroughPublish {
		seedProjectNameWorldThroughPublish(t, ctx, w, c)
	} else {
		seedProjectNameWorldDirectly(t, ctx, w, c, suffix)
	}

	if c.OwnerOverrideName != "" {
		if _, err := pool.Exec(ctx, `
			INSERT INTO owner_overrides (owner_id, target_kind, target_key, field, value)
			VALUES ($1, 'project', $2, 'display_name', $3)
		`, w.owner, w.projectHash, c.OwnerOverrideName); err != nil {
			t.Fatalf("store the owner's chosen project name: %v", err)
		}
	}
	return w
}

// nullableText stores "" as SQL NULL, so "the publisher disclosed nothing" and
// "the publisher disclosed an empty string" are not silently the same row.
func nullableText(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}

func (w *projectNameWorld) cleanup(t *testing.T, ctx context.Context) {
	t.Helper()
	// A case seeded through the publish route left a real encrypted object
	// behind; it is removed before its transcript row goes, while the row can
	// still say which object it was.
	if w.publisher != nil && w.blobs != nil {
		deleteCurrentTitleTestBlob(t, ctx, w.publisher, w.blobs, w.transcript)
	}
	if _, err := w.pool.Exec(ctx, "DELETE FROM owner_overrides WHERE owner_id = $1", w.owner); err != nil {
		t.Errorf("cleanup owner corrections: %v", err)
	}
	cleanupOwners(t, ctx, w.pool, w.owner, w.other)
}

// asViewer attaches the named viewer's identity to a request. signed_out
// attaches none, which is what makes the anonymous branch of each surface run.
func (w *projectNameWorld) asViewer(r *http.Request, viewer string) *http.Request {
	switch viewer {
	case "owner":
		return r.WithContext(withUserID(r.Context(), uuid.UUID(w.owner.Bytes)))
	case "other_user":
		return r.WithContext(withUserID(r.Context(), uuid.UUID(w.other.Bytes)))
	default:
		return r
	}
}

// renderedProject is the project identity one surface rendered.
type renderedProject struct {
	DisplayName string
	NameSource  projectname.NameSource
}

// renderSurface drives ONE production route and returns the project identity it
// rendered. Every branch calls the real exported handler the router mounts;
// none of them re-implements resolution.
func (w *projectNameWorld) renderSurface(t *testing.T, h *Handler, surface, viewer string) renderedProject {
	t.Helper()
	switch surface {
	case "project_page":
		r := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+w.ownerName+"/projects/"+w.projectHash, nil)
		r = withProjectPageURLParams(r, w.ownerName, w.projectHash)
		var page struct {
			Project resolvedProject `json:"project"`
		}
		decodeSurface(t, h.GetUserProject, w.asViewer(r, viewer), surface, &page)
		return renderedProject{DisplayName: page.Project.DisplayName, NameSource: page.Project.NameSource}

	case "profile":
		return w.renderListSurface(t, h, surface, viewer, "?owner="+w.ownerName)

	case "explore":
		// Narrowed by the case's own unique title rather than by owner, so this
		// really is the unscoped public list and not a second profile request.
		return w.renderListSurface(t, h, surface, viewer, "?q="+url.QueryEscape(w.title))

	case "transcript_detail":
		r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+w.transcriptID.String(), nil)
		r = withChiURLParam(r, "id", w.transcriptID.String())
		var detail struct {
			Transcript transcriptResponse `json:"transcript"`
		}
		decodeSurface(t, h.GetTranscript, w.asViewer(r, viewer), surface, &detail)
		return renderedProject{
			DisplayName: detail.Transcript.ProjectDisplayName,
			NameSource:  detail.Transcript.ProjectNameSource,
		}

	case "override_response":
		return w.renderOverrideSurface(t, h)

	default:
		t.Fatalf("the fixture names the surface %q, which this test does not know how to render. Every surface in "+
			"the fixture must be driven through a real route here, or the matrix silently stops covering it.", surface)
		return renderedProject{}
	}
}

// renderListSurface drives the one batched list route both the profile and the
// public explore list are served by, and returns the identity it rendered for
// this world's transcript.
func (w *projectNameWorld) renderListSurface(t *testing.T, h *Handler, surface, viewer, query string) renderedProject {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts"+query, nil)
	var list struct {
		Transcripts []struct {
			Transcript transcriptResponse `json:"transcript"`
		} `json:"transcripts"`
	}
	decodeSurface(t, h.ListTranscripts, w.asViewer(r, viewer), surface, &list)
	for _, item := range list.Transcripts {
		if item.Transcript.ID == w.transcript {
			return renderedProject{
				DisplayName: item.Transcript.ProjectDisplayName,
				NameSource:  item.Transcript.ProjectNameSource,
			}
		}
	}
	t.Fatalf("the %s list did not contain this project's transcript, so it could render no name to compare. The "+
		"transcript is public and the list was narrowed by %q, so an empty result means the list route stopped "+
		"returning it rather than that the name is wrong.", surface, query)
	return renderedProject{}
}

// renderOverrideSurface drives the owner-correction routes and returns the
// identity they answer with for the case's OWN evidence, leaving the stored
// evidence exactly as it found it so surface order cannot matter.
//
// A case that stores an owner rename is answered by re-sending that same rename
// through PATCH: clearing it would resolve a different project. Every other case
// stores a scratch rename and then clears it, so the answer under test is the
// DELETE response — the resolved default, which is the value the case expects.
func (w *projectNameWorld) renderOverrideSurface(t *testing.T, h *Handler) renderedProject {
	t.Helper()
	existing := ""
	if err := w.pool.QueryRow(context.Background(),
		`SELECT COALESCE(value, '') FROM owner_overrides
		  WHERE owner_id = $1 AND target_kind = 'project' AND target_key = $2 AND field = 'display_name'`,
		w.owner, w.projectHash).Scan(&existing); err != nil && !strings.Contains(err.Error(), "no rows") {
		t.Fatalf("read the stored owner correction: %v", err)
	}

	name := existing
	if name == "" {
		name = "scratch correction under test"
	}
	patch := httptest.NewRequest(http.MethodPatch, "/api/v1/users/me/projects/"+w.projectHash,
		strings.NewReader(fmt.Sprintf(`{"display_name":%q}`, name)))
	patch = withChiURLParam(patch, "projectHash", w.projectHash)
	var patched resolvedProject
	decodeSurface(t, h.SetProjectDisplayName, w.asViewer(patch, "owner"), "override_response (rename)", &patched)
	if existing != "" {
		return renderedProject{DisplayName: patched.DisplayName, NameSource: patched.NameSource}
	}

	clear := httptest.NewRequest(http.MethodDelete, "/api/v1/users/me/projects/"+w.projectHash+"/display-name", nil)
	clear = withChiURLParam(clear, "projectHash", w.projectHash)
	var cleared resolvedProject
	decodeSurface(t, h.ClearProjectDisplayName, w.asViewer(clear, "owner"), "override_response (reset)", &cleared)
	return renderedProject{DisplayName: cleared.DisplayName, NameSource: cleared.NameSource}
}

// decodeSurface runs one handler and decodes a 200 body, failing with the
// surface's name and the body so a refusal is never mistaken for a rendered
// name.
func decodeSurface(t *testing.T, handler http.HandlerFunc, r *http.Request, surface string, into any) {
	t.Helper()
	rec := httptest.NewRecorder()
	handler(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("the %s surface answered %d, so it rendered no project name to compare: %s",
			surface, rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
		t.Fatalf("decode the %s response %q: %v", surface, rec.Body.String(), err)
	}
}

// TestProjectNameIsTheSameForEveryViewerOnEverySurface is the matrix itself.
func TestProjectNameIsTheSameForEveryViewerOnEverySurface(t *testing.T) {
	ctx := context.Background()
	fixture := loadProjectNameViewersFixture(t)
	pool := govTestPool(t)
	defer pool.Close()
	h := New(&config.Config{}, pool, nil)

	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			world := newProjectNameWorld(t, ctx, pool, c)
			defer world.cleanup(t, ctx)

			// Every (surface, viewer) answer is collected before anything is
			// judged, so a divergence is reported as the whole disagreement
			// rather than as whichever pair happened to run first.
			rendered := map[string]renderedProject{}
			for _, surface := range fixture.Surfaces {
				for _, viewer := range fixture.Viewers {
					if surface.OwnerOnly && viewer.Name != "owner" {
						continue
					}
					rendered[surface.Name+" as "+viewer.Name] = world.renderSurface(t, h, surface.Name, viewer.Name)
				}
			}

			var disagreements []string
			for _, where := range sortedKeys(rendered) {
				got := rendered[where]
				if got.DisplayName != c.WantDisplayName || string(got.NameSource) != c.WantNameSource {
					disagreements = append(disagreements, fmt.Sprintf("%s rendered %q (%s)",
						where, got.DisplayName, got.NameSource))
				}
			}
			if len(disagreements) > 0 {
				t.Fatalf("this project must read %q (%s) everywhere, but:\n  %s\n\n"+
					"What this means: the name a person sees for a project depends on who they are or on which page "+
					"they opened, which is exactly what storing a single redacted form exists to prevent. Detected "+
					"in the project-name matrix against a real database, so nothing was disclosed differently to "+
					"anyone by this test.\n"+
					"How to fix it: resolve every surface through Handler.resolveProjectIdentities and add no "+
					"viewer-dependent branch to internal/projectname.Resolve; it takes no viewer argument on purpose.",
					c.WantDisplayName, c.WantNameSource, strings.Join(disagreements, "\n  "))
			}

			// The correction routes must have left the stored evidence as they
			// found it; otherwise the matrix above would depend on the order
			// its surfaces happen to run in.
			var overrides int
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM owner_overrides WHERE owner_id = $1 AND target_key = $2`,
				world.owner, world.projectHash).Scan(&overrides); err != nil {
				t.Fatalf("re-read the stored owner corrections: %v", err)
			}
			wantOverrides := 0
			if c.OwnerOverrideName != "" {
				wantOverrides = 1
			}
			if overrides != wantOverrides {
				t.Fatalf("after driving the correction routes the project holds %d owner correction(s), want %d; "+
					"the matrix must leave the case's stored evidence unchanged", overrides, wantOverrides)
			}
		})
	}
}

func sortedKeys(m map[string]renderedProject) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
