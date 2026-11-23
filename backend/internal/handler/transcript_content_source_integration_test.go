//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

// This family opens a real published generation: real PostgreSQL, the
// encrypted store over RustFS, and the production publish path that wrote the
// object. A unit test of the index cannot observe the decryption count, the
// identity the stored row names, or the refusal of an object that moved. The
// production read (readEncryptedTranscript) is the one under test; only a read
// counter wraps the store. The cooking rule is covered at the unit seam.

//go:embed testdata/content_source_encrypted.yaml
var contentSourceEncryptedYAML []byte

type contentSourceEncryptedCase struct {
	Name     string `yaml:"name"`
	Scenario string `yaml:"scenario"`
	Why      string `yaml:"why"`
}

var requiredContentSourceEncryptedCases = []string{
	"source-opens-published-generation-with-one-decryption",
	"source-agrees-with-whole-document-read-on-turns",
	"source-refuses-an-object-that-no-longer-matches-its-row",
	"source-history-read-shares-the-one-decryption",
}

// decryptionCountingStore counts the authenticated reads the content source
// makes through the store.
type decryptionCountingStore struct {
	storage.TranscriptBlobStore
	mu    sync.Mutex
	reads int
}

func (s *decryptionCountingStore) Read(ctx context.Context, id uuid.UUID, d storage.BlobDescriptor, loaded storage.LoadedContentIdentity) ([]byte, storage.ContentIdentity, error) {
	s.mu.Lock()
	s.reads++
	s.mu.Unlock()
	return s.TranscriptBlobStore.Read(ctx, id, d, loaded)
}

func (s *decryptionCountingStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

func TestCookNumberEncryptedRead(t *testing.T) {
	fixture, err := decodeFixtureDocument[struct {
		Content        string                       `yaml:"content"`
		HistoryContent string                       `yaml:"history_content"`
		Cases          []contentSourceEncryptedCase `yaml:"cases"`
	}](contentSourceEncryptedYAML)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" || c.Scenario == "" || c.Why == "" || seen[c.Name] {
			t.Fatalf("encrypted content source case must have a unique name, scenario and reason: %+v", c)
		}
		seen[c.Name] = true
	}
	for _, name := range requiredContentSourceEncryptedCases {
		if !seen[name] {
			t.Fatalf("required encrypted content source case %q is missing", name)
		}
	}
	if len(seen) != len(requiredContentSourceEncryptedCases) {
		t.Fatalf("the fixture holds cases outside the required-name manifest: %v", seen)
	}

	pool := publishLockPool(t, 8)
	t.Cleanup(pool.Close)
	objects := publishEvidenceObjects(t)
	for ordinal, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			w := newOrderingWorld(t, pool, objects, 30000+ordinal, []byte(fixture.Content))
			if c.Scenario == "history-shares-decryption" {
				history := strings.ReplaceAll(fixture.HistoryContent, "__SESSION_ID__", w.localID)
				if replaced := w.request([]byte(history), "history")(w.ctx); replaced.Code != http.StatusOK {
					t.Fatalf("republish with earlier history status=%d body=%s", replaced.Code, replaced.Body.String())
				}
			}
			counting := &decryptionCountingStore{TranscriptBlobStore: w.handler.blobs}
			w.handler.blobs = counting
			row := w.row()
			allow := func(sqlc.Transcript) bool { return true }

			switch c.Scenario {
			case "open-once":
				opened, err := w.handler.openTranscriptContent(w.ctx, row, allow)
				if err != nil {
					t.Fatalf("open the stored generation: %v", err)
				}
				if got := counting.count(); got != 1 {
					t.Fatalf("opening decrypted the object %d times, want 1", got)
				}
				index := opened.Index
				if index.Revision != contentGeneration(row.ContentRevision) || index.Revision.String() != "1" {
					t.Fatalf("index revision = %s, row revision %d", index.Revision, row.ContentRevision)
				}
				if !row.ContentHash.Valid || string(index.ContentHash) != row.ContentHash.String {
					t.Fatalf("index content hash %q, row names %+v", index.ContentHash, row.ContentHash)
				}
				if index.ModelProvider != row.ModelProvider {
					t.Fatalf("index model provider %q, row %q", index.ModelProvider, row.ModelProvider)
				}
				first, err := index.Partition(mainPartitionKey(), noTranscriptBuildReserve)
				if err != nil {
					t.Fatal(err)
				}
				second, err := index.Partition(mainPartitionKey(), noTranscriptBuildReserve)
				if err != nil {
					t.Fatal(err)
				}
				if first != second {
					t.Fatal("a second main read built a second index")
				}
				wantNumbers := map[int]string{0: "1", 2: "1a", 3: "2"}
				if len(first.Rows) != len(wantNumbers) || len(first.Verdict) != 4 {
					t.Fatalf("main serves %d rows over %d stored rows, want %d over 4", len(first.Rows), len(first.Verdict), len(wantNumbers))
				}
				for served, row := range first.Rows {
					if wantNumbers[row.Stored] != row.DisplayNumber || row.ServedPosition != served {
						t.Fatalf("served row %d = stored %d number %q, want number %q", served, row.Stored, row.DisplayNumber, wantNumbers[row.Stored])
					}
					raw, err := opened.Bytes.ReadTurnBytes(w.ctx, row.Start, row.End)
					if err != nil || !bytes.Contains(raw, []byte(`"index":`)) {
						t.Fatalf("served row %d bytes %q (%v) are not its stored turn", served, raw, err)
					}
				}
				if got := counting.count(); got != 1 {
					t.Fatalf("indexing a partition decrypted again: %d reads, want 1", got)
				}

			case "whole-document-parity":
				opened, err := w.handler.openTranscriptContent(w.ctx, row, allow)
				if err != nil {
					t.Fatalf("open the stored generation: %v", err)
				}
				served := getContent(t, w.handler, uuidFromPg(row.ID))
				if served.Code != http.StatusOK {
					t.Fatalf("whole-document read status=%d body=%s", served.Code, served.Body.String())
				}
				turns := func(document []byte) []json.RawMessage {
					var envelope struct {
						SessionDetail struct {
							Turns []json.RawMessage `json:"turns"`
						} `json:"sessionDetail"`
					}
					if err := json.Unmarshal(document, &envelope); err != nil {
						t.Fatal(err)
					}
					return envelope.SessionDetail.Turns
				}
				var detail struct {
					Turns []json.RawMessage `json:"turns"`
				}
				if err := json.Unmarshal(opened.Index.SessionDetail(), &detail); err != nil {
					t.Fatal(err)
				}
				want := turns(served.Body.Bytes())
				if len(detail.Turns) != len(want) || len(want) != 4 {
					t.Fatalf("source holds %d turns, whole-document read serves %d, want 4", len(detail.Turns), len(want))
				}
				for i := range want {
					var a, b bytes.Buffer
					if json.Compact(&a, detail.Turns[i]) != nil || json.Compact(&b, want[i]) != nil || !bytes.Equal(a.Bytes(), b.Bytes()) {
						t.Fatalf("turn %d differs between the source (%s) and the whole-document read (%s)", i, a.Bytes(), b.Bytes())
					}
				}

			case "history-shares-decryption":
				opened, err := w.handler.openTranscriptContent(w.ctx, row, allow)
				if err != nil {
					t.Fatalf("open the stored generation: %v", err)
				}
				main, err := opened.Index.Partition(mainPartitionKey(), noTranscriptBuildReserve)
				if err != nil {
					t.Fatal(err)
				}
				history, err := opened.Index.Partition(historyPartitionKey(0), noTranscriptBuildReserve)
				if err != nil {
					t.Fatal(err)
				}
				if got := counting.count(); got != 1 {
					t.Fatalf("a main read then a history read decrypted %d times, want 1", got)
				}
				numbers := func(p *partitionIndex) []string {
					var out []string
					for _, row := range p.Rows {
						out = append(out, row.DisplayNumber)
					}
					return out
				}
				if got := numbers(main); len(got) != 2 || got[0] != "1" || got[1] != "1a" {
					t.Fatalf("main numbers = %v, want [1 1a]", got)
				}
				if got := numbers(history); len(got) != 3 || got[0] != "1" || got[1] != "1a" || got[2] != "2" {
					t.Fatalf("history numbers = %v, want [1 1a 2]", got)
				}
				if opened.Index.HistorySections() != 1 {
					t.Fatalf("history sections = %d, want 1", opened.Index.HistorySections())
				}

			case "moved-object":
				if err := w.overwrite(w.ctx, []byte(`{"contractVersion":"0.1.0","kind":"session_detail","sessionDetail":{"harness":"claude-code","turns":[]}}`)); err != nil {
					t.Fatalf("move the stored object: %v", err)
				}
				opened, err := w.handler.openTranscriptContent(w.ctx, row, allow)
				if err == nil || opened != nil {
					t.Fatalf("open = (%v, %v), want a refusal and no index", opened, err)
				}
				if errors.Is(err, errUncookable) {
					t.Fatalf("a moved object was reported as an uncookable partition: %v", err)
				}

			default:
				t.Fatalf("fixture scenario %q is unknown", c.Scenario)
			}
		})
	}
}
