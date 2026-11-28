//go:build integration

package router

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
	schemaRoute "github.com/peasant-labs/schema/route"
	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/storage"
)

//go:embed testdata/segmented_read_journey.yaml
var segmentedReadJourneyYAML []byte

// journeyContentFixture is the published transcript the journey stores. It is
// the handler package's canonical valid content document.
const journeyContentFixture = "../handler/testdata/contract/current/valid/content.json"

// journeyRoute names one positioned read the journey makes. The names are the
// fixture's own vocabulary, not a wire value.
type journeyRoute string

const (
	journeyHeader    journeyRoute = "header"
	journeyDirectory journeyRoute = "directory"
	journeySegment   journeyRoute = "segment"
	journeyWindow    journeyRoute = "window"
	journeyLocate    journeyRoute = "locate"
)

// allJourneyRoutes is the closed set the fixture must cover exactly.
var allJourneyRoutes = []journeyRoute{journeyHeader, journeyDirectory, journeySegment, journeyWindow, journeyLocate}

// journeyRoutePaths builds each journey route's path from the contract.
var journeyRoutePaths = map[journeyRoute]func(id string) (string, error){
	journeyHeader:    schemaRoute.VillageGetTranscriptHeaderPath,
	journeyDirectory: schemaRoute.VillageGetTranscriptSegmentsPath,
	journeySegment:   func(id string) (string, error) { return schemaRoute.VillageGetTranscriptSegmentPath(id, 0) },
	journeyWindow:    schemaRoute.VillageGetTranscriptWindowPath,
	journeyLocate:    schemaRoute.VillageLocateTranscriptEntryPath,
}

type segmentedJourneyFixture struct {
	Reference string `yaml:"reference"`
	Routes    []struct {
		Name   journeyRoute `yaml:"name"`
		Method string       `yaml:"method"`
		Query  string       `yaml:"query"`
	} `yaml:"routes"`
	Viewers []struct {
		Name   string `yaml:"name"`
		Status int    `yaml:"status"`
	} `yaml:"viewers"`
}

// TestSegmentedReadJourneyThroughProductionRouter reads a published private
// transcript through router.New, so the middleware, the route-to-handler
// mounting and the session composition are the ones production serves. A
// handler test on a focused mux injects its viewer and cannot see a route
// mounted without its session middleware or onto the wrong handler.
func TestSegmentedReadJourneyThroughProductionRouter(t *testing.T) {
	var fixture segmentedJourneyFixture
	decoder := yaml.NewDecoder(bytes.NewReader(segmentedReadJourneyYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("decode the journey fixture: %v", err)
	}
	requireNames := func(kind string, got []string, want ...string) {
		t.Helper()
		seen := map[string]bool{}
		for _, name := range got {
			if seen[name] {
				t.Fatalf("journey %s %q repeats", kind, name)
			}
			seen[name] = true
		}
		for _, name := range want {
			if !seen[name] {
				t.Fatalf("journey fixture lacks required %s %q", kind, name)
			}
			delete(seen, name)
		}
		if len(seen) != 0 {
			t.Fatalf("journey %ss outside the required set: %v", kind, seen)
		}
	}
	routeNames := make([]string, len(fixture.Routes))
	for i, route := range fixture.Routes {
		routeNames[i] = string(route.Name)
	}
	viewerNames := make([]string, len(fixture.Viewers))
	for i, viewer := range fixture.Viewers {
		viewerNames[i] = viewer.Name
	}
	wantRoutes := make([]string, len(allJourneyRoutes))
	for i, route := range allJourneyRoutes {
		wantRoutes[i] = string(route)
	}
	requireNames("route", routeNames, wantRoutes...)
	requireNames("viewer", viewerNames, "owner", "stranger", "anonymous")

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the production-router read journey")
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

	insertUser := func(prefix string) (uuid.UUID, string) {
		t.Helper()
		githubID := 8_000_000_000 + rand.Int63n(1_000_000_000)
		login := prefix + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (github_id, github_username, provider_user_id) VALUES ($1, $2, $1::bigint::text) RETURNING id
		`, githubID, login).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", prefix, err)
		}
		return id, login
	}
	owner, ownerLogin := insertUser("journey-owner-")
	stranger, strangerLogin := insertUser("journey-stranger-")
	transcript := uuid.New()
	asSystem := func(sql string, args ...any) {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)", database.SystemActorID); err != nil {
			t.Fatalf("declare the system actor: %v", err)
		}
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	t.Cleanup(func() {
		asSystem(`DELETE FROM transcripts WHERE id = $1`, transcript)
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{owner, stranger}); err != nil {
			t.Errorf("delete the users: %v", err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Errorf("begin audit purge: %v", err)
			return
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, "SET LOCAL app.audit_maintenance = 'on'"); err != nil {
			t.Errorf("set maintenance escape: %v", err)
			return
		}
		if _, err := tx.Exec(ctx, `DELETE FROM transcript_governance_events_audit WHERE transcript_id = $1`, transcript); err != nil {
			t.Errorf("purge audit rows: %v", err)
			return
		}
		if err := tx.Commit(ctx); err != nil {
			t.Errorf("commit audit purge: %v", err)
		}
	})

	stored, err := os.ReadFile(journeyContentFixture)
	if err != nil {
		t.Fatalf("read the transcript fixture: %v", err)
	}
	// The first turn carries the entry reference the locator must find.
	content := bytes.Replace(stored, []byte(`"content": "fix the bug"`), []byte(`"sourceEntryRef": "`+fixture.Reference+`", "content": "fix the bug"`), 1)
	if bytes.Equal(content, stored) {
		t.Fatal("the transcript fixture no longer holds the turn the journey marks with an entry reference")
	}
	objects, err := storage.NewS3ObjectStore(&config.Config{S3Endpoint: os.Getenv("TEST_S3_ENDPOINT"), S3Bucket: os.Getenv("TEST_S3_BUCKET"), S3AccessKey: os.Getenv("TEST_S3_ACCESS_KEY"), S3SecretKey: os.Getenv("TEST_S3_SECRET_KEY"), S3UsePathStyle: true})
	if err != nil {
		t.Fatalf("construct the object store: %v", err)
	}
	ring, err := config.ParseTranscriptKeyring("1", `{"1":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}`)
	if err != nil {
		t.Fatalf("construct the keyring: %v", err)
	}
	blobs, err := storage.NewEncryptedTranscriptStore(objects, ring)
	if err != nil {
		t.Fatalf("construct the encrypted store: %v", err)
	}
	descriptor, identity, err := blobs.Write(ctx, transcript, content)
	if err != nil {
		t.Fatalf("write the encrypted transcript: %v", err)
	}
	t.Cleanup(func() {
		if err := blobs.Delete(context.Background(), descriptor); err != nil {
			t.Errorf("delete the encrypted transcript: %v", err)
		}
	})
	asSystem(`
		INSERT INTO transcripts (id, owner_id, local_id, title, visibility, model_provider, model_name, blob_key, blob_size_bytes,
		                         schema_version, content_hash, wrapped_data_key, encryption_algorithm, key_version, project_hash)
		VALUES ($1, $2, $3, 'segmented journey', 'private', 'claude-code', 'model', $4, $5, '0.1.0', $6, $7, $8, $9, $10)
	`, transcript, owner, "segmented-journey-"+transcript.String(), string(descriptor.ObjectKey()), identity.PlaintextSize(), string(identity.Hash()),
		descriptor.WrappedDEK(), string(descriptor.Algorithm()), descriptor.KeyVersion(), strings.Repeat("8", 64))

	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct the title pipeline: %v", err)
	}
	cfg := &config.Config{JWTSecret: "segmented-read-journey-secret", FrontendURL: "https://app.example.com"}
	routes := New(cfg, pool, blobs, titles)
	tokens := map[string]string{"anonymous": ""}
	for name, user := range map[string]struct {
		id    uuid.UUID
		login string
	}{"owner": {owner, ownerLogin}, "stranger": {stranger, strangerLogin}} {
		token, err := auth.CreateToken(cfg.JWTSecret, user.id, user.login)
		if err != nil {
			t.Fatalf("mint the %s's session: %v", name, err)
		}
		tokens[name] = token
	}
	serve := func(method, target, bearer string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, bytes.NewReader(body))
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, req)
		return rec
	}
	path := func(name journeyRoute, id uuid.UUID) string {
		t.Helper()
		build, ok := journeyRoutePaths[name]
		if !ok {
			t.Fatalf("journey route %q has no contract path builder", name)
		}
		built, err := build(id.String())
		if err != nil {
			t.Fatalf("build the %s path: %v", name, err)
		}
		return built
	}

	// The owner's directory read supplies the identity the locator request
	// must echo.
	directory := serve(http.MethodGet, path(journeyDirectory, transcript)+"?v=2", tokens["owner"], nil)
	if directory.Code != http.StatusOK {
		t.Fatalf("the owner's directory read = %d %s", directory.Code, directory.Body.String())
	}
	var listed schema.PositionedDirectory
	if err := json.Unmarshal(directory.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if listed.Context.TotalTurns != 2 || len(listed.Segments) != 1 || listed.Context.ContentHash != string(identity.Hash()) {
		t.Fatalf("the directory does not describe the published generation: %+v", listed.Context)
	}
	locate, err := json.Marshal(schema.TranscriptEntryLocateRequest{Reference: fixture.Reference, Revision: listed.Context.Revision, Representation: listed.Context.Representation, ContentHash: listed.Context.ContentHash})
	if err != nil {
		t.Fatal(err)
	}

	for _, viewer := range fixture.Viewers {
		for _, route := range fixture.Routes {
			t.Run(viewer.Name+"/"+string(route.Name), func(t *testing.T) {
				var body []byte
				if route.Method == http.MethodPost {
					body = locate
				}
				got := serve(route.Method, path(route.Name, transcript)+route.Query, tokens[viewer.Name], body)
				if got.Code != viewer.Status {
					t.Fatalf("%s %s as %s = %d %s, want %d", route.Method, route.Name, viewer.Name, got.Code, got.Body.String(), viewer.Status)
				}
				if viewer.Status != http.StatusOK {
					missing := serve(route.Method, path(route.Name, uuid.New())+route.Query, tokens[viewer.Name], body)
					if missing.Code != got.Code || missing.Body.String() != got.Body.String() || missing.Header().Get("Cache-Control") != got.Header().Get("Cache-Control") {
						t.Fatalf("the private transcript is distinguishable from a missing one for %s: %d %q / %d %q", viewer.Name, got.Code, got.Body.String(), missing.Code, missing.Body.String())
					}
					return
				}
				switch route.Name {
				case journeyHeader:
					var header schema.TranscriptContentHeader
					if err := json.Unmarshal(got.Body.Bytes(), &header); err != nil || header.ContentHash != string(identity.Hash()) || header.Representation != listed.Context.Representation {
						t.Fatalf("the header does not describe the published generation: %s (%v)", got.Body.String(), err)
					}
				case journeySegment:
					var segment struct {
						Rows []json.RawMessage `json:"rows"`
					}
					if err := json.Unmarshal(got.Body.Bytes(), &segment); err != nil || len(segment.Rows) != 2 || !bytes.Contains(got.Body.Bytes(), []byte("fix the bug")) {
						t.Fatalf("the segment does not serve the published turns: %s (%v)", got.Body.String(), err)
					}
				case journeyLocate:
					var match schema.TranscriptEntryMatch
					if err := json.Unmarshal(got.Body.Bytes(), &match); err != nil || match.Position != 0 || match.SegmentOrdinal != 0 || match.Kind != schema.TranscriptEntryReferenceSource {
						t.Fatalf("the locator did not find the referenced entry: %s (%v)", got.Body.String(), err)
					}
				}
			})
		}
	}
}
