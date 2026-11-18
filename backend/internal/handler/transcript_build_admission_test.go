package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

//go:embed testdata/build_admission.yaml
var buildAdmissionYAML []byte

type buildAdmissionCase struct {
	Name   string   `yaml:"name"`
	Modes  []string `yaml:"modes"`
	Routes []struct {
		Name string `yaml:"name"`
		Path string `yaml:"path"`
	} `yaml:"routes"`
}

func admissionIndex(t *testing.T, cook cookRowsFunc) *transcriptIndex {
	t.Helper()
	doc := cacheTestDocument(t, schema.HarnessClaudeCode, 1024, 1)
	x, err := newTranscriptIndex(transcriptIndexInput{Bytes: doc, ModelProvider: "claude-code", Revision: 3, ContentHash: storage.ContentHash(schema.ComputeTranscriptHash(doc)), Durable: true, Cook: cook})
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func buildReserve(c *transcriptContentCache) transcriptBuildReserve {
	return func() (func(), error) { return c.AcquireBuild(context.Background()) }
}

func TestBuildAdmission(t *testing.T) {
	cases, err := decodeFixtureRows[buildAdmissionCase](buildAdmissionYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{"build-admission-refuses-503-retry-after": true, "build-admission-denied-viewer-404-before-admission": true, "build-admission-warm-read-and-waiter-take-no-slot": true, "build-admission-releases-on-failure-and-cancel": true, "build-admission-refusal-reserves-nothing": true}
	for _, tc := range cases {
		if !required[tc.Name] {
			t.Fatalf("unexpected case %s", tc.Name)
		}
		delete(required, tc.Name)
		t.Run(tc.Name, func(t *testing.T) {
			switch tc.Name {
			case "build-admission-refuses-503-retry-after":
				testAdmissionRoutes(t, tc)
			case "build-admission-denied-viewer-404-before-admission":
				h, mux, base, _ := admissionHandler(t, false)
				release1, _ := h.contentCache.AcquireBuild(context.Background())
				defer release1()
				release2, _ := h.contentCache.AcquireBuild(context.Background())
				defer release2()
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, base+"/segments?v=2", nil))
				if w.Code != 404 {
					t.Fatalf("denied: %d %s", w.Code, w.Body.String())
				}
				if h.contentCache.heavyWork != 2 {
					t.Fatal("denied viewer changed slots")
				}
			case "build-admission-warm-read-and-waiter-take-no-slot":
				entered, unblock := make(chan struct{}), make(chan struct{})
				var calls atomic.Int32
				x := admissionIndex(t, func(turns []schema.TranscriptCookTurn, h schema.Harness) ([]schema.CookedTurn, error) {
					calls.Add(1)
					close(entered)
					<-unblock
					return schema.CookTranscriptRows(turns, h)
				})
				c := newTranscriptContentCache(transcriptContentCacheConfig{})
				reserve := buildReserve(c)
				done := make(chan error, 2)
				go func() { _, e := x.Partition(mainPartitionKey(), reserve); done <- e }()
				<-entered
				go func() { _, e := x.Partition(mainPartitionKey(), reserve); done <- e }()
				close(unblock)
				if e := <-done; e != nil {
					t.Fatal(e)
				}
				if e := <-done; e != nil {
					t.Fatal(e)
				}
				a, _ := c.AcquireBuild(context.Background())
				b, _ := c.AcquireBuild(context.Background())
				defer a()
				defer b()
				if _, e := x.Partition(mainPartitionKey(), reserve); e != nil {
					t.Fatalf("built partition refused: %v", e)
				}
				if calls.Load() != 1 {
					t.Fatalf("cook calls %d", calls.Load())
				}
				cache := newTranscriptContentCache(transcriptContentCacheConfig{})
				key := cacheTestKey(1)
				loading, finish := make(chan struct{}), make(chan struct{})
				loaded := make(chan error, 2)
				loader := func(context.Context) (*openedTranscript, error) {
					close(loading)
					<-finish
					return &openedTranscript{Index: x, Bytes: x}, nil
				}
				go func() {
					_, err := cache.Acquire(context.Background(), key, transcriptCacheLoad{Weight: 1}, loader)
					loaded <- err
				}()
				<-loading
				other, err := cache.AcquireBuild(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				go func() {
					_, err := cache.Acquire(context.Background(), key, transcriptCacheLoad{Weight: 1}, loader)
					loaded <- err
				}()
				for {
					cache.mu.Lock()
					waiters := cache.flights[key].waiters
					cache.mu.Unlock()
					if waiters == 2 {
						break
					}
					runtime.Gosched()
				}
				close(finish)
				if err := <-loaded; err != nil {
					t.Fatal(err)
				}
				if err := <-loaded; err != nil {
					t.Fatal(err)
				}
				other()
			case "build-admission-releases-on-failure-and-cancel":
				testAdmissionRelease(t, tc.Modes)
			case "build-admission-refusal-reserves-nothing":
				c := newTranscriptContentCache(transcriptContentCacheConfig{Budget: 100})
				c.mu.Lock()
				c.reserved = 100
				e := c.admitLocked(1)
				if !errors.Is(e, errTranscriptReadBusy) || c.heavyWork != 0 || c.reserved != 100 {
					t.Fatal("weight refusal changed accounting")
				}
				c.reserved = 0
				c.mu.Unlock()
				a, _ := c.AcquireBuild(context.Background())
				b, _ := c.AcquireBuild(context.Background())
				c.mu.Lock()
				e = c.admitLocked(50)
				if !errors.Is(e, errTranscriptReadBusy) || c.heavyWork != 2 || c.reserved != 0 {
					t.Fatal("count refusal changed accounting")
				}
				c.mu.Unlock()
				a()
				b()
				r, e := c.AcquireBuild(context.Background())
				if e != nil {
					t.Fatal(e)
				}
				r()
			}
		})
	}
	if len(required) != 0 {
		t.Fatalf("missing cases: %v", required)
	}
}

func testAdmissionRelease(t *testing.T, modes []string) {
	required := map[string]bool{"error": true, "panic": true, "cancel": true}
	for _, mode := range modes {
		if !required[mode] {
			t.Fatalf("unexpected mode %q", mode)
		}
		delete(required, mode)
		t.Run(mode, func(t *testing.T) {
			entered, unblock := make(chan struct{}), make(chan struct{})
			c := newTranscriptContentCache(transcriptContentCacheConfig{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			x := admissionIndex(t, func(turns []schema.TranscriptCookTurn, h schema.Harness) ([]schema.CookedTurn, error) {
				close(entered)
				<-unblock
				if mode == "panic" {
					panic("test panic")
				}
				if mode == "error" {
					return nil, errors.New("test error")
				}
				return schema.CookTranscriptRows(turns, h)
			})
			done := make(chan bool, 1)
			go func() {
				panicked := false
				defer func() {
					if recover() != nil {
						panicked = true
					}
					done <- panicked
				}()
				_, _ = x.Partition(mainPartitionKey(), func() (func(), error) { return c.AcquireBuild(context.WithoutCancel(ctx)) })
			}()
			<-entered
			cancel()
			a, e := c.AcquireBuild(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			if _, e = c.AcquireBuild(context.Background()); !errors.Is(e, errTranscriptReadBusy) {
				t.Fatalf("build did not hold slot: %v", e)
			}
			a()
			close(unblock)
			panicked := <-done
			if panicked != (mode == "panic") {
				t.Fatalf("panic=%v", panicked)
			}
			c.mu.Lock()
			n := c.heavyWork
			c.mu.Unlock()
			if n != 0 {
				t.Fatalf("leaked %d slots", n)
			}
		})
	}
	if len(required) != 0 {
		t.Fatalf("missing modes: %v", required)
	}
}

func admissionHandler(t *testing.T, public bool) (*Handler, *chi.Mux, string, *transcriptIndex) {
	t.Helper()
	x := admissionIndex(t, nil)
	id := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	owner := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	visibility := "public"
	if !public {
		visibility = "private"
	}
	row := sqlc.Transcript{ID: id, OwnerID: owner, Visibility: visibility, ContentRevision: 3, ModelProvider: "claude-code", BlobKey: "transcripts/30000000-0000-4000-8000-000000000001.bin", WrappedDataKey: []byte("key"), EncryptionAlgorithm: "aes-256-gcm-random-nonce-v1", KeyVersion: 1, ContentHash: pgtype.Text{String: string(x.ContentHash), Valid: true}, BlobSizeBytes: pgtype.Int8{Int64: int64(len(x.Bytes)), Valid: true}}
	transcriptID := schema.VillageUUID(uuid.UUID(id.Bytes).String())
	x.Transport = schema.TranscriptReadTransport{Kind: schema.TranscriptReadTransportVillageTranscript, TranscriptID: &transcriptID}
	x.Validation = schema.TranscriptReadServedLegacy
	q := &mockQuerier{getTranscriptByID: func(context.Context, pgtype.UUID) (sqlc.Transcript, error) { return row, nil }, listPublicOwnersForViewer: func(context.Context, sqlc.ListPublicOwnersForViewerParams) ([]sqlc.ListPublicOwnersForViewerRow, error) {
		return []sqlc.ListPublicOwnersForViewerRow{{ID: owner, GithubUsername: "owner", ProfileVisible: true}}, nil
	}, servedIntegrityForContent: func(context.Context, sqlc.ServedIntegrityForContentParams) (sqlc.ServedIntegrityForContentRow, error) {
		return sqlc.ServedIntegrityForContentRow{Open: true, FlaggedTurns: []int32{0}}, nil
	}}
	h := newTestHandler(q, nil)
	h.contentCache = newTranscriptContentCache(transcriptContentCacheConfig{})
	opened := &openedTranscript{Index: x, Bytes: x, SourceRow: row}
	_, err := h.contentCache.Acquire(context.Background(), transcriptCacheKeyForRow(row), transcriptCacheLoad{Weight: int64(len(x.Bytes))}, func(context.Context) (*openedTranscript, error) { return opened, nil })
	if err != nil {
		t.Fatal(err)
	}
	mux := chi.NewRouter()
	mux.Get("/transcripts/{id}/header", h.GetTranscriptHeader)
	mux.Get("/transcripts/{id}/segments", h.GetTranscriptSegments)
	mux.Get("/transcripts/{id}/segments/{index}", h.GetTranscriptSegment)
	mux.Get("/transcripts/{id}/window", h.GetTranscriptWindow)
	mux.Post("/transcripts/{id}/segments/locate", h.LocateTranscriptEntry)
	return h, mux, "/transcripts/" + uuid.UUID(id.Bytes).String(), x
}

func testAdmissionRoutes(t *testing.T, tc buildAdmissionCase) {
	required := map[string]bool{"header": true, "directory": true, "segment": true, "window": true, "locate": true, "history": true}
	h, mux, base, _ := admissionHandler(t, true)
	h.contentCache.mu.Lock()
	h.contentCache.entries = make(map[transcriptCacheKey]*transcriptCacheEntry)
	h.contentCache.retained = 0
	h.contentCache.reserved = h.contentCache.budget
	h.contentCache.mu.Unlock()
	baseline := httptest.NewRecorder()
	mux.ServeHTTP(baseline, httptest.NewRequest(http.MethodGet, base+"/segments?v=2", nil))
	if baseline.Code != 503 || baseline.Header().Get("Retry-After") != "1" {
		t.Fatalf("cache refusal: %d %s", baseline.Code, baseline.Body.String())
	}
	expected := baseline.Body.Bytes()
	for _, route := range tc.Routes {
		if !required[route.Name] {
			t.Fatalf("unexpected route %s", route.Name)
		}
		delete(required, route.Name)
		t.Run(route.Name, func(t *testing.T) {
			h, mux, base, x := admissionHandler(t, true)
			var body []byte
			method := http.MethodGet
			if route.Name == "locate" {
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, base+"/segments?v=2", nil))
				if w.Code != 200 {
					t.Fatalf("warm directory: %d %s", w.Code, w.Body.String())
				}
				var d schema.PositionedDirectory
				if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
					t.Fatal(err)
				}
				body, _ = json.Marshal(schema.TranscriptEntryLocateRequest{Reference: "main-ref", Revision: d.Context.Revision, Representation: d.Context.Representation, ContentHash: d.Context.ContentHash})
				method = http.MethodPost
			}
			if route.Name == "header" && (len(x.Partitions) != 0 || x.ContentHash == "") {
				t.Fatal("header fixture must be cold with visible hash")
			}
			a, e := h.contentCache.AcquireBuild(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			defer a()
			b, e := h.contentCache.AcquireBuild(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			defer b()
			req := httptest.NewRequest(method, base+route.Path, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != 503 || w.Header().Get("Retry-After") != "1" {
				t.Fatalf("got %d retry %q body %s", w.Code, w.Header().Get("Retry-After"), w.Body.String())
			}
			if expected == nil {
				expected = append([]byte(nil), w.Body.Bytes()...)
			} else if !bytes.Equal(expected, w.Body.Bytes()) {
				t.Fatalf("body differs: %s", w.Body.String())
			}
		})
	}
	if len(required) != 0 {
		t.Fatal(fmt.Sprintf("missing routes: %v", required))
	}
}
