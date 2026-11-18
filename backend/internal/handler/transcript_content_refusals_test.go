package handler

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/content_source_refusals.yaml
var refusalCorpus []byte

//go:embed testdata/content_source_refusal_routes.yaml
var refusalRoutes []byte

type refusalDocument struct {
	Name  string `yaml:"name"`
	Turns string `yaml:"turns"`
}

type refusalRoute struct {
	Name      string `yaml:"name"`
	Method    string `yaml:"method"`
	Path      string `yaml:"path"`
	Status    int    `yaml:"status"`
	BodyClass string `yaml:"body_class"`
}

func TestContentSourceRefusals(t *testing.T) {
	cases, err := decodeFixtureRows[refusalDocument](refusalCorpus)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := decodeFixtureRows[refusalRoute](refusalRoutes)
	if err != nil {
		t.Fatal(err)
	}
	requiredRoutes := map[string]bool{"content": false, "header": false, "directory": false, "segment": false, "window": false, "locate": false}
	for _, route := range routes {
		if _, ok := requiredRoutes[route.Name]; !ok || requiredRoutes[route.Name] || route.Status != http.StatusInternalServerError || route.BodyClass == "" {
			t.Fatalf("invalid refusal route: %+v", route)
		}
		requiredRoutes[route.Name] = true
	}
	for name, seen := range requiredRoutes {
		if !seen {
			t.Fatalf("missing refusal route %q", name)
		}
	}
	required := map[string]bool{
		"refusal-invalid-utf8": false, "refusal-duplicate-member": false,
		"refusal-turns-not-array": false, "refusal-role-type": false,
		"refusal-depth-type": false, "refusal-nesting-too-deep": false,
		"refusal-envelope-in-turn-array": false,
		"legacy-lone-surrogate-refused":  false,
	}
	for _, c := range cases {
		if _, ok := required[c.Name]; !ok || required[c.Name] {
			t.Fatalf("unexpected or duplicate refusal case %q", c.Name)
		}
		required[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			turns := strings.ReplaceAll(c.Turns, "__INVALID_UTF8__", string([]byte{0xff}))
			turns = strings.ReplaceAll(turns, "__DEEP__", strings.Repeat("[", 70)+"0"+strings.Repeat("]", 70))
			doc := []byte(fmt.Sprintf(`{"contractVersion":"0.1.0","kind":"session_detail","sessionDetail":{"id":"refusal","harness":"claude-code","startTime":"2026-01-01T00:00:00Z","endTime":"2026-01-01T00:01:00Z","durationMins":1,"totalTokens":0,"tokensIn":0,"tokensOut":0,"turnCount":1,"toolCallCount":0,"turns":%s}}`, turns))
			id := pgtype.UUID{Bytes: uuid.MustParse("30000000-0000-4000-8000-000000000001"), Valid: true}
			owner := pgtype.UUID{Bytes: uuid.MustParse("30000000-0000-4000-8000-ffffffffffff"), Valid: true}
			blobs := newFakeBlobStore()
			blobs.put("transcripts/30000000-0000-4000-8000-000000000001.bin", doc)
			row := sqlc.Transcript{ID: id, OwnerID: owner, Visibility: "public", ContentRevision: 3, ModelProvider: "claude-code", BlobKey: "transcripts/30000000-0000-4000-8000-000000000001.bin", WrappedDataKey: []byte("key"), EncryptionAlgorithm: "aes-256-gcm-random-nonce-v1", KeyVersion: 1, ContentHash: pgtype.Text{String: schema.ComputeTranscriptHash(doc), Valid: true}, BlobSizeBytes: pgtype.Int8{Int64: int64(len(doc)), Valid: true}}
			q := &mockQuerier{
				getTranscriptByID: func(context.Context, pgtype.UUID) (sqlc.Transcript, error) { return row, nil },
				listPublicOwnersForViewer: func(context.Context, sqlc.ListPublicOwnersForViewerParams) ([]sqlc.ListPublicOwnersForViewerRow, error) {
					return []sqlc.ListPublicOwnersForViewerRow{{ID: owner, GithubUsername: "owner", ProfileVisible: true}}, nil
				},
			}
			h := newTestHandler(q, blobs)
			h.contentCache = newTranscriptContentCache(transcriptContentCacheConfig{})
			mux := chi.NewRouter()
			mux.Get("/transcripts/{id}/content", h.GetTranscriptContent)
			mux.Get("/transcripts/{id}/header", h.GetTranscriptHeader)
			mux.Get("/transcripts/{id}/segments", h.GetTranscriptSegments)
			mux.Get("/transcripts/{id}/segments/{index}", h.GetTranscriptSegment)
			mux.Get("/transcripts/{id}/window", h.GetTranscriptWindow)
			mux.Post("/transcripts/{id}/segments/locate", h.LocateTranscriptEntry)
			base := "/transcripts/" + uuid.UUID(id.Bytes).String()
			for _, route := range routes {
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequest(route.Method, base+route.Path, strings.NewReader("{}")))
				if w.Code != route.Status || !strings.Contains(w.Body.String(), `"error"`) || !strings.Contains(w.Body.String(), route.BodyClass) {
					t.Errorf("%s: got %d %s, want %d %s", route.Name, w.Code, w.Body.String(), route.Status, route.BodyClass)
				}
			}
		})
	}
	for name, seen := range required {
		if !seen {
			t.Errorf("required refusal case %q missing", name)
		}
	}
}
