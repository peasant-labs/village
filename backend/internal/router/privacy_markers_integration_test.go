//go:build integration

package router

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"log"
	"log/slog"
	"math/rand"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
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

//go:embed testdata/privacy_markers.yaml
var privacyMarkersYAML []byte

// privacyMarkersFixture names the embedded fixture in failure messages.
const privacyMarkersFixture = "testdata/privacy_markers.yaml"

// privacyContentFixture is the contract's own valid content document, which the
// handler tests share; the synthetic transcript is built from it.
const privacyContentFixture = "../handler/testdata/contract/current/valid/content.json"

// requiredPrivacyRequestNames is the fixture's name manifest.
var requiredPrivacyRequestNames = []string{
	"header-owner", "header-denied",
	"directory-owner", "directory-denied",
	"segment-owner", "segment-denied", "segment-representation-malformed", "segment-revision-malformed",
	"segment-representation-stale", "segment-revision-stale",
	"window-owner", "window-unserved-anchor", "window-denied",
	"locate-owner-match", "locate-owner-unmatched", "locate-denied", "locate-invalid-body",
	"deep-link-turn", "content-owner", "missing-transcript", "unmatched-route", "invalid-segment-index",
}

type privacyViewer string

const (
	privacyViewerOwner     privacyViewer = "owner"
	privacyViewerAnonymous privacyViewer = "anonymous"
)

type privacyRequest struct {
	Name   string        `yaml:"name"`
	Method string        `yaml:"method"`
	Path   string        `yaml:"path"`
	Viewer privacyViewer `yaml:"viewer"`
	Status int           `yaml:"status"`
	Body   string        `yaml:"body"`
}

type privacyPlants struct {
	SessionID            string `yaml:"session_id"`
	Entry                string `yaml:"entry"`
	UnmatchedEntry       string `yaml:"unmatched_entry"`
	RepresentationMarker string `yaml:"representation_marker"`
	RevisionMarker       string `yaml:"revision_marker"`
	StaleRepresentation  string `yaml:"stale_representation"`
	StaleRevision        string `yaml:"stale_revision"`
	Turn                 int    `yaml:"turn"`
	Query                string `yaml:"query"`
	Project              string `yaml:"project"`
	Content              string `yaml:"content"`
	SourcePath           string `yaml:"source_path"`
}

type privacyFixture struct {
	Plants   privacyPlants    `yaml:"plants"`
	Requests []privacyRequest `yaml:"requests"`
}

func loadPrivacyFixture(t *testing.T) privacyFixture {
	t.Helper()
	var fixture privacyFixture
	d := yaml.NewDecoder(bytes.NewReader(privacyMarkersYAML))
	d.KnownFields(true)
	if err := d.Decode(&fixture); err != nil {
		t.Fatalf("decode %s: %v", privacyMarkersFixture, err)
	}
	names := map[string]bool{}
	for _, r := range fixture.Requests {
		if names[r.Name] {
			t.Fatalf("%s names %q twice; give each request one name", privacyMarkersFixture, r.Name)
		}
		names[r.Name] = true
	}
	for _, name := range requiredPrivacyRequestNames {
		if !names[name] {
			t.Fatalf("%s lost required request %q; restore it", privacyMarkersFixture, name)
		}
	}
	if len(names) != len(requiredPrivacyRequestNames) {
		t.Fatalf("%s has %d requests, the manifest names %d; add a new request's name to requiredPrivacyRequestNames", privacyMarkersFixture, len(names), len(requiredPrivacyRequestNames))
	}
	return fixture
}

// capturedOutput collects everything the server writes in-process while a
// test runs: the standard logger (the request log and handler log lines),
// slog's default logger, and standard output and standard error (chi's panic
// recoverer, and anything printed directly). The server registers no metrics
// and no tracing, so there are no labels or attributes to capture.
type capturedOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *capturedOutput) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *capturedOutput) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// captureServerOutput redirects every in-process output to one buffer until
// the test ends. The returned flush restores the outputs and waits until the
// pipe copiers have drained, so a read after it sees everything written; the
// test's cleanup calls it too, and only the first call has an effect.
func captureServerOutput(t *testing.T) (*capturedOutput, func()) {
	t.Helper()
	out := &capturedOutput{}
	previousWriter, previousFlags := log.Writer(), log.Flags()
	log.SetOutput(out)
	previousSlog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug})))
	stdout, stderr := os.Stdout, os.Stderr
	var copies sync.WaitGroup
	redirect := func(target **os.File) *os.File {
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatalf("open a pipe to capture output: %v", err)
		}
		*target = writer
		copies.Add(1)
		go func() {
			defer copies.Done()
			_, _ = io.Copy(out, reader)
		}()
		return writer
	}
	outWriter, errWriter := redirect(&os.Stdout), redirect(&os.Stderr)
	var once sync.Once
	flush := func() {
		once.Do(func() {
			os.Stdout, os.Stderr = stdout, stderr
			_ = outWriter.Close()
			_ = errWriter.Close()
			copies.Wait()
			log.SetOutput(previousWriter)
			log.SetFlags(previousFlags)
			slog.SetDefault(previousSlog)
		})
	}
	t.Cleanup(flush)
	return out, flush
}

// TestServerOutputCarriesNoTranscriptMarkers publishes a transcript through the
// production router over PostgreSQL and the encrypted object store, sends every
// positioned read granted and refused, a deep link, a matched and an unmatched
// route and an invalid request, and requires that nothing the server wrote
// names the transcript, its entries, the echoed values, a query string, a body
// or a credential. Route templates and statuses may appear.
func TestServerOutputCarriesNoTranscriptMarkers(t *testing.T) {
	fixture := loadPrivacyFixture(t)
	plants := fixture.Plants
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the mounted privacy check; the integration gate rejects this skip")
	}
	ctx := context.Background()
	poolConfig, err := database.PoolConfig(databaseURL)
	if err != nil {
		t.Fatalf("build the pool config: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("reach the test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.RunMigrations(pool); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	objects, err := storage.NewS3ObjectStore(&config.Config{S3Endpoint: os.Getenv("TEST_S3_ENDPOINT"), S3Bucket: os.Getenv("TEST_S3_BUCKET"), S3AccessKey: os.Getenv("TEST_S3_ACCESS_KEY"), S3SecretKey: os.Getenv("TEST_S3_SECRET_KEY"), S3UsePathStyle: true})
	if err != nil {
		t.Fatalf("construct the test object store: %v", err)
	}
	ring, err := config.ParseTranscriptKeyring("1", `{"1":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}`)
	if err != nil {
		t.Fatalf("construct the test keyring: %v", err)
	}
	blobs, err := storage.NewEncryptedTranscriptStore(objects, ring)
	if err != nil {
		t.Fatalf("construct the encrypted store: %v", err)
	}

	githubID := 9_000_000_000 + rand.Int63n(1_000_000_000)
	login := "privacy-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	var owner uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id) VALUES ($1, $2, $1::bigint::text) RETURNING id
	`, githubID, login).Scan(&owner); err != nil {
		t.Fatalf("insert the owner: %v", err)
	}
	var published uuid.UUID
	var routes http.Handler
	var token string
	t.Cleanup(func() {
		// The owner deletes it through the production route, which removes the
		// stored object too; the system delete below then finds nothing left.
		if published != uuid.Nil && routes != nil {
			req := httptest.NewRequest(http.MethodDelete, transcriptPath(schemaRoute.VillageGetTranscriptMetadata, published), nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			routes.ServeHTTP(rec, req)
			if rec.Code >= 300 {
				t.Errorf("the owner's delete of the published transcript = %d %s", rec.Code, rec.Body.String())
			}
		}
		deletePrivacyWorld(t, pool, published, owner)
	})

	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct the title pipeline: %v", err)
	}
	cfg := &config.Config{JWTSecret: "privacy-markers-" + uuid.NewString(), FrontendURL: "https://app.example.com"}
	routes = New(cfg, pool, blobs, titles)
	token, err = auth.CreateToken(cfg.JWTSecret, owner, login)
	if err != nil {
		t.Fatalf("mint the owner's session: %v", err)
	}

	// Everything from the publish onward is captured.
	captured, flushCapture := captureServerOutput(t)
	var title string
	published, title = publishPrivacyTranscript(t, routes, token, plants)

	send := func(method, path, body string, viewer privacyViewer) *httptest.ResponseRecorder {
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, reader)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if viewer == privacyViewerOwner {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, req)
		return rec
	}

	// The served revision, representation and content hash come from the
	// owner's header read; the reads that must succeed echo them.
	headerRead := send(http.MethodGet, transcriptPath(schemaRoute.VillageGetTranscriptHeader, published)+"?v=2", "", privacyViewerOwner)
	if headerRead.Code != http.StatusOK {
		t.Fatalf("the owner's header read = %d %s, want 200; the published transcript is not served by positioned reads", headerRead.Code, headerRead.Body.String())
	}
	var header schema.TranscriptContentHeader
	if err := json.Unmarshal(headerRead.Body.Bytes(), &header); err != nil {
		t.Fatalf("decode the header read: %v", err)
	}
	contentHash := header.ContentHash
	missing := uuid.NewString()
	fill := strings.NewReplacer(
		"{transcript}", published.String(),
		"{missing}", missing,
		"{entry}", plants.Entry,
		"{unmatched_entry}", plants.UnmatchedEntry,
		"{revision}", header.Revision,
		"{representation}", header.Representation,
		"{revision_marker}", plants.RevisionMarker,
		"{representation_marker}", plants.RepresentationMarker,
		"{stale_revision}", plants.StaleRevision,
		"{stale_representation}", plants.StaleRepresentation,
		"{turn}", strconv.Itoa(plants.Turn),
		"{query}", plants.Query,
		"{content_hash}", contentHash,
	)
	paths := []string{}
	for _, request := range fixture.Requests {
		t.Run(request.Name, func(t *testing.T) {
			path := fill.Replace(request.Path)
			paths = append(paths, path)
			rec := send(request.Method, path, fill.Replace(request.Body), request.Viewer)
			if rec.Code != request.Status {
				t.Fatalf("%s %s as %s = %d %s, want %d", request.Method, path, request.Viewer, rec.Code, rec.Body.String(), request.Status)
			}
		})
	}

	// Read only after every output has drained into the capture.
	flushCapture()
	output := captured.String()
	markers := map[string]string{
		"transcript id":                       published.String(),
		"transcript id (compact)":             strings.ReplaceAll(published.String(), "-", ""),
		"unpublished transcript id":           missing,
		"unpublished transcript id (compact)": strings.ReplaceAll(missing, "-", ""),
		"session id":                          plants.SessionID,
		"entry reference":                     plants.Entry,
		"unmatched entry":                     plants.UnmatchedEntry,
		"representation marker":               plants.RepresentationMarker,
		"revision marker":                     plants.RevisionMarker,
		"stale representation":                plants.StaleRepresentation,
		"stale revision":                      plants.StaleRevision,
		"turn":                                strconv.Itoa(plants.Turn),
		"query":                               plants.Query,
		"query string":                        "?v=2",
		"project":                             plants.Project,
		"content":                             plants.Content,
		"source path":                         plants.SourcePath,
		"bearer token":                        token,
		"authorization scheme":                "Bearer ",
		"owner login":                         login,
		"owner user id":                       owner.String(),
		"owner user id (compact)":             strings.ReplaceAll(owner.String(), "-", ""),
		"owner github id":                     strconv.FormatInt(githubID, 10),
		"locate body":                         `"reference"`,
	}
	// A title this short could match unrelated log text; the derived title is
	// built from the planted content, which is itself a marker.
	if len(title) >= 8 {
		markers["title"] = title
	}
	if len(header.Representation) >= 8 {
		markers["served representation"] = header.Representation
	}
	if contentHash != "" {
		markers["served content hash"] = contentHash
	}
	for _, path := range paths {
		markers["raw path "+path] = path
		// A log that prints the path but drops the query still leaks it.
		if bare, _, hasQuery := strings.Cut(path, "?"); hasQuery {
			markers["raw path without query "+bare] = bare
		}
	}
	leaked := false
	for name, value := range markers {
		if value != "" && strings.Contains(output, value) {
			leaked = true
			t.Errorf("the server's output names the %s (%q); it may name only route templates and statuses", name, value)
		}
	}
	if leaked {
		t.Errorf("the captured output that named a marker:\n%s", output)
	}
	// The capture must have seen the request log, or its silence proves
	// nothing. It runs after the markers, so a log that prints raw paths (and
	// so fewer template lines) fails on the named marker first.
	if lines := strings.Count(output, schemaRoute.VillageGetTranscriptMetadata.Pattern()); lines < len(fixture.Requests) {
		t.Errorf("the captured output holds %d request-log lines for transcript routes, want at least %d; either the capture is not seeing the server's log, or the log no longer prints route templates:\n%s", lines, len(fixture.Requests), output)
	}
}

// publishPrivacyTranscript publishes the synthetic transcript through the
// production publish route and returns its id and the title the server gave it.
func publishPrivacyTranscript(t *testing.T, routes http.Handler, token string, plants privacyPlants) (uuid.UUID, string) {
	t.Helper()
	contentBytes, err := os.ReadFile(privacyContentFixture)
	if err != nil {
		t.Fatalf("read the contract content fixture %s: %v", privacyContentFixture, err)
	}
	// Plant the session id, the first turn's text and its entry reference by
	// replacing exact source text, so every other byte stays the contract's.
	plantings := [][2]string{
		{`"550e8400-e29b-41d4-a716-446655440000"`, strconv.Quote(plants.SessionID)},
		{`"content": "fix the bug",`, `"content": ` + strconv.Quote(plants.Content) + `, "sourceEntryRef": ` + strconv.Quote(plants.Entry) + `,`},
	}
	text := string(contentBytes)
	for _, planting := range plantings {
		if strings.Count(text, planting[0]) != 1 {
			t.Fatalf("%s no longer holds %s exactly once; update the planting to the fixture's current text", privacyContentFixture, planting[0])
		}
		text = strings.Replace(text, planting[0], planting[1], 1)
	}
	transcript := []byte(text)
	metadata := schema.PublishRequest{
		Identity:    schema.SessionIdentity{SessionID: schema.SessionID(plants.SessionID), SchemaVersion: 2},
		Model:       schema.ModelInfo{Harness: schema.HarnessClaudeCode, Model: "claude-opus-4-5"},
		Timestamp:   schema.TimestampInfo{Start: 1700000000000, End: 1700000060000},
		Source:      schema.SourceInfo{FilePath: plants.SourcePath, Format: "jsonl"},
		Project:     schema.ProjectContext{Hash: schema.ProjectHash(strings.Repeat("ab", 32)), Name: plants.Project},
		Stats:       schema.SessionStats{TurnCount: 2, ToolCallCount: 1, DurationMs: 60000, TokensIn: 60, TokensOut: 40},
		Diagnostics: schema.DiagnosticsInfo{Warnings: []schema.DiagnosticEntry{}},
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("encode the publish metadata: %v", err)
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("metadata", string(metadataJSON)); err != nil {
		t.Fatalf("write the metadata field: %v", err)
	}
	part, err := form.CreateFormFile("transcript_file", "privacy.json")
	if err != nil {
		t.Fatalf("create the transcript part: %v", err)
	}
	if _, err := part.Write(transcript); err != nil {
		t.Fatalf("write the transcript part: %v", err)
	}
	if err := form.Close(); err != nil {
		t.Fatalf("close the publish form: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, schemaRoute.VillagePublishTranscript.Pattern(), &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("publish the synthetic transcript = %d %s, want 200 or 201", rec.Code, rec.Body.String())
	}
	var response struct {
		Transcript struct {
			ID    uuid.UUID `json:"id"`
			Title *string   `json:"title"`
		} `json:"transcript"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.Transcript.ID == uuid.Nil {
		t.Fatalf("decode the publish response %s: %v", rec.Body.String(), err)
	}
	title := ""
	if response.Transcript.Title != nil {
		title = *response.Transcript.Title
	}
	return response.Transcript.ID, title
}

// deletePrivacyWorld removes the published transcript as the system actor, the
// owner, and the transcript's audit rows last through the maintenance escape.
func deletePrivacyWorld(t *testing.T, pool *pgxpool.Pool, transcript, owner uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if transcript != uuid.Nil {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Errorf("begin transcript delete: %v", err)
			return
		}
		if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)", database.SystemActorID); err != nil {
			t.Errorf("declare the system actor: %v", err)
		} else if _, err := tx.Exec(ctx, `DELETE FROM transcripts WHERE id = $1`, transcript); err != nil {
			t.Errorf("delete the transcript: %v", err)
		} else if err := tx.Commit(ctx); err != nil {
			t.Errorf("commit transcript delete: %v", err)
		}
		_ = tx.Rollback(ctx)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, owner); err != nil {
		t.Errorf("delete the owner: %v", err)
	}
	if transcript == uuid.Nil {
		return
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Errorf("begin audit purge: %v", err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET LOCAL app.audit_maintenance = 'on'"); err != nil {
		t.Errorf("set the maintenance escape: %v", err)
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM transcript_governance_events_audit WHERE transcript_id = $1`, transcript); err != nil {
		t.Errorf("purge the audit rows: %v", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("commit the audit purge: %v", err)
	}
}

// transcriptPath fills a transcript route's {id} with one transcript.
func transcriptPath(route schemaRoute.Route, id uuid.UUID) string {
	return strings.Replace(route.Pattern(), "{id}", id.String(), 1)
}
