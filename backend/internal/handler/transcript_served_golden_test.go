package handler

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

//go:embed testdata/content_source_golden.yaml
var contentSourceGoldenYAML []byte

//go:embed testdata/content_source_golden.json
var contentSourceGoldenJSON []byte

// servedGoldenPath is the recorded answers' file; the embed above names the
// same file.
const servedGoldenPath = "testdata/content_source_golden.json"

// servedGoldenUpdateEnv rewrites servedGoldenPath from the
// current build instead of asserting it. Use it only for a deliberate change
// to served bytes, and say why in the commit.
const servedGoldenUpdateEnv = "VILLAGE_UPDATE_SERVED_GOLDEN"

type servedGoldenDocument struct {
	Name     string `yaml:"name"`
	Why      string `yaml:"why"`
	Flagged  []int  `yaml:"flagged"`
	Generate int    `yaml:"generate"`
	Document string `yaml:"document"`
	// StoredLegacy installs the content source's own index of the document in
	// the content cache, bypassing the strict read validator only.
	StoredLegacy bool `yaml:"stored_legacy"`
}

// requiredServedGoldenDocuments is the exact corpus: a missing or unlisted
// document fails.
var requiredServedGoldenDocuments = []string{
	"golden-claude-main-history-images",
	"golden-legacy-index-shapes",
	"golden-legacy-consistent-indexes",
	"golden-pi-keeps-every-row",
	"golden-indented-empty-main",
	"golden-multi-segment",
}

// servedGoldenAnswer is one recorded response. The body is held by digest and
// length so the multi-segment bodies stay out of the repository; any changed
// byte changes the digest.
type servedGoldenAnswer struct {
	Status       int    `json:"status"`
	ETag         string `json:"etag,omitempty"`
	CacheControl string `json:"cacheControl"`
	Length       int    `json:"length"`
	SHA256       string `json:"sha256"`
}

func (d servedGoldenDocument) bytes() []byte {
	if d.Generate == 0 {
		return []byte(d.Document)
	}
	var turns strings.Builder
	for i := 0; i < d.Generate; i++ {
		if i > 0 {
			turns.WriteByte(',')
		}
		role := "assistant"
		if i%5 == 0 {
			role = "user"
		}
		// About 4 KiB of escaped and multi-byte text per turn, so 600 turns
		// cross the 1 MiB segment budget more than once.
		content := strings.Repeat(fmt.Sprintf("turn %d says \"hi\"\n\tété ", i), 160)
		encoded, err := json.Marshal(content)
		if err != nil {
			panic(err)
		}
		fmt.Fprintf(&turns, `{"index":%d,"role":%q,"content":%s,"timestamp":"2026-01-01T00:00:01Z","depth":0`, i, role, encoded)
		if i%7 == 3 {
			turns.WriteString(`,"contentParts":[{"type":"image","image":{"omittedReason":"not_published"}}]`)
		}
		if i%11 == 4 {
			fmt.Fprintf(&turns, `,"toolCalls":[{"id":"c%d","name":"Read","arguments":"{}","result":"r","resultEntryRef":"ref-c%d","usage":{"ownerId":"owner-c%d","sourceEntryRef":"ref-c%d","scope":"tool","completeness":"partial","tokens":{"input":%d}}}]`, i, i, i, i, i)
		}
		turns.WriteByte('}')
	}
	return []byte(strings.Replace(d.Document, "__GENERATED__", turns.String(), 1))
}

// servedGoldenViewer is one viewer projection of the same stored generation.
type servedGoldenViewer struct {
	Name           string
	ProfileVisible bool
}

var servedGoldenViewers = []servedGoldenViewer{{Name: "withheld"}, {Name: "profile", ProfileVisible: true}}

// TestServedBytesGolden records every positioned-read answer for a varied
// corpus through the production handlers and the production content source,
// with only the database and the encrypted store faked. It guards the content
// source's indexing: any change to what a read serves must be deliberate.
func TestServedBytesGolden(t *testing.T) {
	documents, err := decodeFixtureRows[servedGoldenDocument](contentSourceGoldenYAML)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, d := range documents {
		if d.Name == "" || d.Why == "" || d.Document == "" || seen[d.Name] {
			t.Fatalf("golden document must have a unique name, a reason and a document: %q", d.Name)
		}
		seen[d.Name] = true
	}
	for _, name := range requiredServedGoldenDocuments {
		if !seen[name] {
			t.Fatalf("required golden document %q is missing from the corpus", name)
		}
		delete(seen, name)
	}
	for name := range seen {
		t.Fatalf("golden document %q is not in the required-name manifest; add it", name)
	}

	got := map[string]map[string]servedGoldenAnswer{}
	for i, d := range documents {
		answers := map[string]servedGoldenAnswer{}
		for _, viewer := range servedGoldenViewers {
			for request, answer := range servedGoldenRun(t, i, d, viewer) {
				answers[viewer.Name+" "+request] = answer
			}
		}
		got[d.Name] = answers
	}

	if os.Getenv(servedGoldenUpdateEnv) != "" {
		encoded, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(servedGoldenPath, append(encoded, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("rewrote %s; unset %s to assert it", servedGoldenPath, servedGoldenUpdateEnv)
		return
	}
	var want map[string]map[string]servedGoldenAnswer
	if err := json.Unmarshal(contentSourceGoldenJSON, &want); err != nil {
		t.Fatalf("decode the served golden: %v; regenerate it with %s=1 only for a deliberate served-byte change", err, servedGoldenUpdateEnv)
	}
	for _, d := range documents {
		gotAnswers, wantAnswers := got[d.Name], want[d.Name]
		for _, request := range sortedGoldenKeys(wantAnswers) {
			answer, ok := gotAnswers[request]
			if !ok {
				t.Errorf("%s: %q was recorded and is no longer requested", d.Name, request)
				continue
			}
			if answer != wantAnswers[request] {
				t.Errorf("%s: %q served %+v, the golden records %+v", d.Name, request, answer, wantAnswers[request])
			}
		}
		for _, request := range sortedGoldenKeys(gotAnswers) {
			if _, ok := wantAnswers[request]; !ok {
				t.Errorf("%s: %q is requested and has no recorded answer", d.Name, request)
			}
		}
	}
	// Every document must reach the content source, or the golden guards a
	// refusal instead of served rows.
	for _, d := range documents {
		if want[d.Name]["profile header"].Status != http.StatusOK || want[d.Name]["profile directory positioned main"].Status != http.StatusOK {
			t.Errorf("%s: the golden records no served header and directory; the corpus document no longer reaches the content source", d.Name)
		}
	}
}

func sortedGoldenKeys(m map[string]servedGoldenAnswer) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// servedGoldenRun stores one document behind a fresh handler and records the
// answers of every read this viewer can make of it.
func servedGoldenRun(t *testing.T, ordinal int, d servedGoldenDocument, viewer servedGoldenViewer) map[string]servedGoldenAnswer {
	t.Helper()
	document := d.bytes()
	id := pgtype.UUID{Bytes: uuid.MustParse(fmt.Sprintf("30000000-0000-4000-8000-%012d", ordinal)), Valid: true}
	owner := pgtype.UUID{Bytes: uuid.MustParse("30000000-0000-4000-8000-ffffffffffff"), Valid: true}
	blobKey := fmt.Sprintf("transcripts/30000000-0000-4000-8000-%012d.bin", ordinal)
	blobs := newFakeBlobStore()
	blobs.put(blobKey, document)
	var stored struct {
		SessionDetail struct {
			Harness string `json:"harness"`
		} `json:"sessionDetail"`
	}
	if err := json.Unmarshal(document, &stored); err != nil {
		t.Fatalf("%s: the corpus document is not JSON: %v", d.Name, err)
	}
	// The row records the producer the document names, so the cached
	// generation's cooking class matches the key a read looks up.
	row := sqlc.Transcript{
		ID: id, OwnerID: owner, Visibility: "public", ContentRevision: 3, ModelProvider: stored.SessionDetail.Harness,
		BlobKey: blobKey, WrappedDataKey: []byte("test-wrapped-key"), EncryptionAlgorithm: "aes-256-gcm-random-nonce-v1", KeyVersion: 1,
		ContentHash:   pgtype.Text{String: schema.ComputeTranscriptHash(document), Valid: true},
		BlobSizeBytes: pgtype.Int8{Int64: int64(len(document)), Valid: true},
	}
	flagged := make([]int32, 0, len(d.Flagged))
	for _, stored := range d.Flagged {
		flagged = append(flagged, int32(stored))
	}
	q := &mockQuerier{
		getTranscriptByID: func(context.Context, pgtype.UUID) (sqlc.Transcript, error) { return row, nil },
		listPublicOwnersForViewer: func(context.Context, sqlc.ListPublicOwnersForViewerParams) ([]sqlc.ListPublicOwnersForViewerRow, error) {
			return []sqlc.ListPublicOwnersForViewerRow{{ID: owner, GithubUsername: "golden-owner", ProfileVisible: viewer.ProfileVisible}}, nil
		},
		servedIntegrityForContent: func(context.Context, sqlc.ServedIntegrityForContentParams) (sqlc.ServedIntegrityForContentRow, error) {
			return sqlc.ServedIntegrityForContentRow{Open: len(flagged) != 0, FlaggedTurns: flagged}, nil
		},
	}
	h := newTestHandler(q, blobs)
	h.contentCache = newTranscriptContentCache(transcriptContentCacheConfig{})
	mux := chi.NewRouter()
	mux.Get("/transcripts/{id}/segments", h.GetTranscriptSegments)
	mux.Get("/transcripts/{id}/segments/{index}", h.GetTranscriptSegment)
	mux.Get("/transcripts/{id}/window", h.GetTranscriptWindow)
	mux.Get("/transcripts/{id}/header", h.GetTranscriptHeader)
	base := "/transcripts/" + uuid.UUID(id.Bytes).String()
	if d.StoredLegacy {
		transcriptID := schema.VillageUUID(uuid.UUID(id.Bytes).String())
		index, err := newTranscriptIndex(transcriptIndexInput{Revision: contentGeneration(row.ContentRevision), ContentHash: storage.ContentHash(row.ContentHash.String), Durable: true, ModelProvider: row.ModelProvider, Bytes: document, Validation: schema.TranscriptReadServedLegacy, Transport: schema.TranscriptReadTransport{Kind: schema.TranscriptReadTransportVillageTranscript, TranscriptID: &transcriptID}})
		if err != nil {
			t.Fatalf("%s: index the stored document: %v", d.Name, err)
		}
		opened := &openedTranscript{Index: index, Bytes: index, SourceRow: row}
		if _, err := h.contentCache.Acquire(context.Background(), transcriptCacheKeyForRow(row), transcriptCacheLoad{Owner: uuid.UUID(owner.Bytes), Weight: int64(len(document))}, func(context.Context) (*openedTranscript, error) { return opened, nil }); err != nil {
			t.Fatalf("%s: install the stored document: %v", d.Name, err)
		}
	}

	answers := map[string]servedGoldenAnswer{}
	get := func(name, path string) []byte {
		r := httptest.NewRequest(http.MethodGet, base+path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		digest := sha256.Sum256(w.Body.Bytes())
		answers[name] = servedGoldenAnswer{Status: w.Code, ETag: w.Header().Get("ETag"), CacheControl: w.Header().Get("Cache-Control"), Length: w.Body.Len(), SHA256: hex.EncodeToString(digest[:])}
		if w.Code != http.StatusOK {
			return nil
		}
		return w.Body.Bytes()
	}

	get("header", "/header")
	get("header revision", "/header?revision=3")
	positioned := -1
	for _, generation := range schema.SegmentedReadGenerations() {
		if version, _ := schema.SegmentedReadProtocolVersion(generation); version == schema.SegmentedReadAPIVersion {
			positioned = generation
		}
	}
	if positioned < 0 {
		t.Fatal("the contract declares no positioned segmented-read generation")
	}
	type partition struct{ name, query string }
	partitions := []partition{{"main", ""}}
	main := get("directory positioned main", fmt.Sprintf("/segments?v=%d", positioned))
	var directory struct {
		History []json.RawMessage `json:"history"`
	}
	if main != nil {
		if err := json.Unmarshal(main, &directory); err != nil {
			t.Fatalf("%s: decode the main directory: %v", d.Name, err)
		}
	}
	for section := range len(directory.History) + 1 {
		partitions = append(partitions, partition{fmt.Sprintf("history-%d", section), fmt.Sprintf("&partition=history&section=%d", section)})
	}
	for _, generation := range schema.SegmentedReadGenerations() {
		for _, p := range partitions {
			if p.query != "" && generation != positioned {
				continue
			}
			prefix := fmt.Sprintf("v%d %s", generation, p.name)
			body := get("directory "+prefix, fmt.Sprintf("/segments?v=%d%s", generation, p.query))
			var segments struct {
				Context struct {
					TotalSegments int `json:"totalSegments"`
				} `json:"context"`
				TotalSegments int `json:"totalSegments"`
			}
			if body != nil {
				if err := json.Unmarshal(body, &segments); err != nil {
					t.Fatalf("%s: decode %s directory: %v", d.Name, prefix, err)
				}
			}
			total := max(segments.TotalSegments, segments.Context.TotalSegments)
			for ordinal := range total + 1 {
				get(fmt.Sprintf("segment %s %d", prefix, ordinal), fmt.Sprintf("/segments/%d?v=%d%s", ordinal, generation, p.query))
			}
			get("window default "+prefix, fmt.Sprintf("/window?v=%d%s", generation, p.query))
			for _, anchor := range []int{0, 1, 7, 299, 300, 599, 100000} {
				get(fmt.Sprintf("window anchor %d %s", anchor, prefix), fmt.Sprintf("/window?v=%d&anchor=%d&span=3%s", generation, anchor, p.query))
			}
		}
	}
	return answers
}
