package handler

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

//go:embed testdata/segmented_read_payloads.yaml
var segmentedReadPayloadsYAML []byte

type segmentedDirectoryPayloadCase struct {
	Name    string `yaml:"name"`
	Total   int    `yaml:"total"`
	Query   string `yaml:"query"`
	Status  int    `yaml:"status"`
	Page    *int   `yaml:"page"`
	PerPage *int   `yaml:"per_page"`
	Count   int    `yaml:"count"`
	First   int    `yaml:"first"`
	Last    int    `yaml:"last"`
	Error   string `yaml:"error"`
}

type segmentedHeaderPayloadCase struct {
	Name          string                         `yaml:"name"`
	Document      string                         `yaml:"document"`
	ModelProvider string                         `yaml:"model_provider"`
	Flagged       []int32                        `yaml:"flagged"`
	Withheld      bool                           `yaml:"withheld"`
	Marks         []schema.TranscriptFlaggedTurn `yaml:"marks"`
	OmittedImages int                            `yaml:"omitted_images"`
	Present       []string                       `yaml:"present"`
	Absent        []string                       `yaml:"absent"`
}

var requiredSegmentedPayloadCases = []string{
	"directory-at-cap-unpaged",
	"directory-above-cap-first-page",
	"directory-above-cap-last-page",
	"directory-page-beyond-last",
	"directory-page-two-at-cap",
	"directory-page-zero",
	"header-mark-remapped-and-removed-row-dropped",
	"header-marks-sorted-and-deduplicated",
	"header-every-mark-dropped-omits-member",
	"header-omitted-images-main-and-history",
	"header-withheld-owner-omits-identity-and-marks",
}

// payloadHandler mounts the production read handlers over one generation that
// the cache already holds, so every case is served from the read path with no
// object store.
func payloadHandler(t *testing.T, document []byte, provider string, flagged []int32, withheld bool) (*chi.Mux, string, *transcriptIndex) {
	t.Helper()
	x, err := newTranscriptIndex(transcriptIndexInput{Bytes: document, ModelProvider: provider, Revision: 3, ContentHash: storage.ContentHash(schema.ComputeTranscriptHash(document)), Durable: true})
	if err != nil {
		t.Fatal(err)
	}
	id := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	owner := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	row := sqlc.Transcript{ID: id, OwnerID: owner, Visibility: "public", ContentRevision: 3, ModelProvider: provider, BlobKey: "transcripts/30000000-0000-4000-8000-000000000002.bin", WrappedDataKey: []byte("key"), EncryptionAlgorithm: "aes-256-gcm-random-nonce-v1", KeyVersion: 1, ContentHash: pgtype.Text{String: string(x.ContentHash), Valid: true}, BlobSizeBytes: pgtype.Int8{Int64: int64(len(x.Bytes)), Valid: true}}
	transcriptID := schema.VillageUUID(uuid.UUID(id.Bytes).String())
	x.Transport = schema.TranscriptReadTransport{Kind: schema.TranscriptReadTransportVillageTranscript, TranscriptID: &transcriptID}
	x.Validation = schema.TranscriptReadServedLegacy
	q := &mockQuerier{getTranscriptByID: func(context.Context, pgtype.UUID) (sqlc.Transcript, error) { return row, nil }, listPublicOwnersForViewer: func(context.Context, sqlc.ListPublicOwnersForViewerParams) ([]sqlc.ListPublicOwnersForViewerRow, error) {
		return []sqlc.ListPublicOwnersForViewerRow{{ID: owner, GithubUsername: "owner", ProfileVisible: !withheld}}, nil
	}, servedIntegrityForContent: func(context.Context, sqlc.ServedIntegrityForContentParams) (sqlc.ServedIntegrityForContentRow, error) {
		return sqlc.ServedIntegrityForContentRow{Open: len(flagged) != 0, FlaggedTurns: flagged}, nil
	}}
	h := newTestHandler(q, nil)
	h.contentCache = newTranscriptContentCache(transcriptContentCacheConfig{})
	opened := &openedTranscript{Index: x, Bytes: x, SourceRow: row}
	if _, err := h.contentCache.Acquire(context.Background(), transcriptCacheKeyForRow(row), transcriptCacheLoad{Weight: int64(len(x.Bytes))}, func(context.Context) (*openedTranscript, error) { return opened, nil }); err != nil {
		t.Fatal(err)
	}
	if !h.contentCache.has(transcriptCacheKeyForRow(row)) {
		t.Fatal("the payload fixture's generation was not retained; a read would reach the absent object store")
	}
	mux := chi.NewRouter()
	mux.Get("/transcripts/{id}/header", h.GetTranscriptHeader)
	mux.Get("/transcripts/{id}/segments", h.GetTranscriptSegments)
	return mux, "/transcripts/" + uuid.UUID(id.Bytes).String(), x
}

// syntheticMainPartition is a built main index of total one-row segments. The
// cut budget makes a real directory above the cap gigabytes long, so the
// directory cases install an index of that many refs on a real generation and
// read it through the mounted handler.
func syntheticMainPartition(total int) *partitionIndex {
	rows := make([]indexedTurn, total)
	refs := make([]schema.PositionedSegmentRef, total)
	positions := make([]positionRef, total)
	for i := range total {
		recorded := i
		rows[i] = indexedTurn{Stored: i, ServedPosition: i, DisplayNumber: "1", Role: schema.RoleUser, ConversationTurn: true, ByteLength: 1, RecordedIndexState: schema.TranscriptRecordedIndexPresent, RecordedIndex: &recorded}
		refs[i] = schema.PositionedSegmentRef{Index: i, SourceFirstPosition: i, SourceLastPosition: i, FirstTurn: &recorded, LastTurn: &recorded, ByteLength: 1, ConversationTurns: &schema.ConversationTurnSpan{Before: i, Count: 1}}
		positions[i] = positionRef{Ordinal: i, SourceFirstPosition: i, SourceLastPosition: i}
	}
	conversation := total
	return &partitionIndex{Rows: rows, Verdict: []schema.CookedTurn{}, Refs: refs, Positions: positions, TotalSegments: total, TotalTurns: total, TotalConversationTurns: &conversation, UsagePresent: schema.TranscriptUsageUnknown}
}

func decodeMembers(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var members map[string]json.RawMessage
	if err := json.Unmarshal(body, &members); err != nil {
		t.Fatalf("response is not a JSON object: %v: %s", err, body)
	}
	return members
}

func TestSegmentedReadPayloads(t *testing.T) {
	fixture, err := decodeFixtureDocument[struct {
		Directory []segmentedDirectoryPayloadCase `yaml:"directory"`
		Header    []segmentedHeaderPayloadCase    `yaml:"header"`
	}](segmentedReadPayloadsYAML)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, name := range append(namesOf(fixture.Directory, func(c segmentedDirectoryPayloadCase) string { return c.Name }), namesOf(fixture.Header, func(c segmentedHeaderPayloadCase) string { return c.Name })...) {
		if seen[name] {
			t.Fatalf("payload case %q repeats", name)
		}
		seen[name] = true
	}
	for _, name := range requiredSegmentedPayloadCases {
		if !seen[name] {
			t.Fatalf("required payload case %q is missing from the fixture", name)
		}
		delete(seen, name)
	}
	if len(seen) != 0 {
		t.Fatalf("payload cases outside the required-name manifest: %v", seen)
	}

	document := []byte(`{"sessionDetail":{"id":"payload-directory","harness":"pi","turns":[{"index":0,"role":"user","content":"first"}]}}`)
	for _, tc := range fixture.Directory {
		t.Run(tc.Name, func(t *testing.T) {
			mux, base, x := payloadHandler(t, document, "pi", nil, false)
			x.mu.Lock()
			x.Partitions[mainPartitionKey()] = syntheticMainPartition(tc.Total)
			x.mu.Unlock()
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, base+"/segments?v=2"+tc.Query, nil))
			if w.Code != tc.Status {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.Status, w.Body.String())
			}
			members := decodeMembers(t, w.Body.Bytes())
			if tc.Status != http.StatusOK {
				var refusal struct {
					Error string `json:"error"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &refusal); err != nil || refusal.Error != tc.Error {
					t.Fatalf("refusal body %s, want error %q", w.Body.String(), tc.Error)
				}
				if _, present := members["segments"]; present {
					t.Fatal("a refused page carried segments")
				}
				return
			}
			for member, want := range map[string]*int{"directoryPage": tc.Page, "directoryPerPage": tc.PerPage} {
				raw, present := members[member]
				if want == nil {
					if present {
						t.Fatalf("an unpaged directory carries %s = %s", member, raw)
					}
					continue
				}
				var got int
				if !present || json.Unmarshal(raw, &got) != nil || got != *want {
					t.Fatalf("%s = %s, want %d", member, raw, *want)
				}
			}
			var directory schema.PositionedDirectory
			if err := json.Unmarshal(w.Body.Bytes(), &directory); err != nil {
				t.Fatal(err)
			}
			if len(directory.Segments) != tc.Count || directory.Segments[0].Index != tc.First || directory.Segments[len(directory.Segments)-1].Index != tc.Last {
				t.Fatalf("served refs %d from %d to %d, want %d from %d to %d", len(directory.Segments), directory.Segments[0].Index, directory.Segments[len(directory.Segments)-1].Index, tc.Count, tc.First, tc.Last)
			}
			if directory.Context.TotalSegments != tc.Total {
				t.Fatalf("totalSegments %d, want the whole partition's %d", directory.Context.TotalSegments, tc.Total)
			}
			first := directory.Segments[0]
			if first.SourceFirstPosition != tc.First || first.ConversationTurns == nil || first.ConversationTurns.Before != tc.First {
				t.Fatalf("the page's first ref is not placed in the whole partition: %+v", first)
			}
		})
	}

	for _, tc := range fixture.Header {
		t.Run(tc.Name, func(t *testing.T) {
			mux, base, _ := payloadHandler(t, []byte(tc.Document), tc.ModelProvider, tc.Flagged, tc.Withheld)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, base+"/header", nil))
			if w.Code != http.StatusOK {
				t.Fatalf("header status %d: %s", w.Code, w.Body.String())
			}
			members := decodeMembers(t, w.Body.Bytes())
			for _, member := range tc.Present {
				if _, ok := members[member]; !ok {
					t.Fatalf("header lacks %s: %s", member, w.Body.String())
				}
			}
			for _, member := range tc.Absent {
				if raw, ok := members[member]; ok {
					t.Fatalf("header carries %s = %s, which this case requires absent", member, raw)
				}
			}
			var omitted int
			if err := json.Unmarshal(members["omittedImages"], &omitted); err != nil || omitted != tc.OmittedImages {
				t.Fatalf("omittedImages = %s, want %d", members["omittedImages"], tc.OmittedImages)
			}
			if raw, ok := members["flaggedTurns"]; ok {
				var marks []schema.TranscriptFlaggedTurn
				if err := json.Unmarshal(raw, &marks); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(marks, tc.Marks) {
					t.Fatalf("flaggedTurns = %s, want %+v", raw, tc.Marks)
				}
			} else if len(tc.Marks) != 0 {
				t.Fatalf("header has no flaggedTurns, want %+v", tc.Marks)
			}
		})
	}
}

func namesOf[T any](cases []T, name func(T) string) []string {
	names := make([]string, len(cases))
	for i, c := range cases {
		names[i] = name(c)
	}
	return names
}
