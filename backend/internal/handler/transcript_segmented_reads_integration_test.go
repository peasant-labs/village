//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/segmented_reads_encrypted.yaml
var segmentedReadsEncryptedYAML []byte

type segmentedReadsEncryptedCase struct {
	Name          string `yaml:"name"`
	Path          string `yaml:"path"`
	Status        int    `yaml:"status"`
	Conditional   bool   `yaml:"conditional"`
	Mismatch      bool   `yaml:"mismatch"`
	Private       bool   `yaml:"private"`
	Missing       bool   `yaml:"missing"`
	FailIntegrity bool   `yaml:"fail_integrity"`
	CompareTo     string `yaml:"compare_to"`
}

type failingIntegrityQueries struct{ Querier }

func (q failingIntegrityQueries) ServedIntegrityForContent(context.Context, sqlc.ServedIntegrityForContentParams) (sqlc.ServedIntegrityForContentRow, error) {
	return sqlc.ServedIntegrityForContentRow{}, errors.New("synthetic integrity lookup failure")
}

func TestSegmentedReadsEncrypted(t *testing.T) {
	cases, err := decodeFixtureRows[segmentedReadsEncryptedCase](segmentedReadsEncryptedYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{"header-cold-no-partition": true, "positioned-directory": true, "positioned-segment": true, "positioned-window-default": true, "positioned-window-anchored": true, "legacy-directory": true, "zero-directory": true, "legacy-segment": true, "zero-segment": true, "legacy-window": true, "zero-window": true, "protocol-absent": true, "protocol-refused": true, "protocol-malformed": true, "protocol-duplicate": true, "protocol-empty": true, "header": true, "header-bad-revision": true, "header-bad-representation": true, "header-revalidation": true, "etag-integrity-lookup-fails": true, "representation-mismatch": true, "access-denied": true, "etag-conditional-denied": true, "deleted-indistinguishable": true}
	fixture, err := decodeFixtureDocument[struct {
		Content        string                       `yaml:"content"`
		HistoryContent string                       `yaml:"history_content"`
		Cases          []contentSourceEncryptedCase `yaml:"cases"`
	}](contentSourceEncryptedYAML)
	if err != nil {
		t.Fatal(err)
	}
	content := strings.ReplaceAll(fixture.Content, "content-source-session", "550e8400-e29b-41d4-a716-446655440000")
	pool := publishLockPool(t, 8)
	t.Cleanup(pool.Close)
	world := newOrderingWorld(t, pool, publishEvidenceObjects(t), 49001, []byte(content))
	world.handler.contentCache = newTranscriptContentCache(transcriptContentCacheConfig{})
	counting := &decryptionCountingStore{TranscriptBlobStore: world.handler.blobs}
	world.handler.blobs = counting
	mux := chi.NewRouter()
	mux.Get("/transcripts/{id}/segments", world.handler.GetTranscriptSegments)
	mux.Get("/transcripts/{id}/segments/{index}", world.handler.GetTranscriptSegment)
	mux.Get("/transcripts/{id}/window", world.handler.GetTranscriptWindow)
	mux.Get("/transcripts/{id}/header", world.handler.GetTranscriptHeader)
	base := "/transcripts/" + uuidFromPg(world.id).String()
	etag := ""
	denied := ""
	bodies := make(map[string][]byte)
	for _, tc := range cases {
		if !required[tc.Name] {
			t.Fatalf("unexpected or repeated case %q", tc.Name)
		}
		delete(required, tc.Name)
		t.Run(tc.Name, func(t *testing.T) {
			if tc.FailIntegrity {
				original := world.handler.queries
				world.handler.queries = failingIntegrityQueries{Querier: original}
				t.Cleanup(func() { world.handler.queries = original })
			}
			if tc.Name == "access-denied" {
				if response := transcriptVisibilityPatch(t, world.handler, world.owner, world.id, dbVisibilityPrivate); response.Code != http.StatusOK {
					t.Fatalf("private status %d: %s", response.Code, response.Body.String())
				}
			}
			path := base + tc.Path
			if tc.Missing {
				path = "/transcripts/00000000-0000-4000-8000-000000000001" + tc.Path
			}
			if tc.Mismatch {
				path += "&representation=" + schema.RepresentationVersion + "." + strings.Repeat("0", 64)
			}
			r := httptest.NewRequest(http.MethodGet, path, nil)
			if !tc.Private {
				r = r.WithContext(context.WithValue(r.Context(), UserContextKey, world.owner))
			}
			if tc.Conditional {
				r.Header.Set("If-None-Match", etag)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != tc.Status {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.Status, w.Body.String())
			}
			if tc.CompareTo != "" {
				want, ok := bodies[tc.CompareTo]
				if !ok || !bytes.Equal(w.Body.Bytes(), want) {
					t.Fatalf("body differs from %s: got %s, want %s", tc.CompareTo, w.Body.Bytes(), want)
				}
			}
			bodies[tc.Name] = bytes.Clone(w.Body.Bytes())
			if tc.Name == "access-denied" {
				denied = w.Body.String()
			}
			if tc.Missing && w.Body.String() != denied {
				t.Fatalf("missing and denied responses differ: %q / %q", w.Body.String(), denied)
			}
			if w.Header().Get("Vary") != "Authorization, Cookie" {
				t.Fatal("missing vary")
			}
			if tc.Name == "header-cold-no-partition" {
				opened, err := world.handler.openCachedTranscript(world.ctx, world.row(), func(sqlc.Transcript) bool { return true })
				if err != nil || len(opened.Index.Partitions) != 0 {
					t.Fatalf("header opened a partition: %v", err)
				}
				if w.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("unversioned header cache fields: %v", w.Header())
				}
			}
			if tc.Name == "header" {
				etag = w.Header().Get("ETag")
				if etag == "" {
					t.Fatal("header lacks etag")
				}
				var header schema.TranscriptContentHeader
				if err := json.Unmarshal(w.Body.Bytes(), &header); err != nil {
					t.Fatal(err)
				}
				if header.ContentHash != world.row().ContentHash.String {
					t.Fatalf("header hash does not match the published generation")
				}
			}
			if tc.Name == "header" || tc.Name == "header-revalidation" {
				if w.Header().Get("ETag") != etag || w.Header().Get("Cache-Control") != "private, no-cache" {
					t.Fatalf("header cache fields: %v", w.Header())
				}
			}
			if tc.Status >= 400 && w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("refusal cache fields: %v", w.Header())
			}
			if strings.HasPrefix(tc.Name, "positioned-") {
				var value struct {
					Context schema.TranscriptReadContext `json:"context"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil || value.Context.ProtocolVersion != schema.SegmentedReadAPIVersion {
					t.Fatalf("context %+v: %v", value.Context, err)
				}
			}
			if tc.Name == "positioned-window-default" || tc.Name == "positioned-window-anchored" {
				var value schema.PositionedWindow
				if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
					t.Fatal(err)
				}
				if (value.RequestedAnchor == nil) != (tc.Name == "positioned-window-default") || value.AnchorPosition != 0 {
					t.Fatalf("unexpected window anchor: %+v", value)
				}
			}
		})
	}
	if len(required) != 0 {
		t.Fatalf("missing cases %v", required)
	}
	if got := counting.count(); got != 1 {
		t.Fatalf("decryptions %d, want one", got)
	}
}
