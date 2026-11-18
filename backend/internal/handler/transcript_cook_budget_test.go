package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/cook_number_budget.yaml
var cookNumberBudgetFixture []byte

type cookBudgetFixture struct {
	Name  string `yaml:"name"`
	Turns int    `yaml:"turns"`
	Sizes []struct {
		Name  string `yaml:"name"`
		Turns int    `yaml:"turns"`
	} `yaml:"sizes"`
	Subjects []struct {
		Name  string `yaml:"name"`
		Text  string `yaml:"text"`
		Dense bool   `yaml:"dense"`
	} `yaml:"subjects"`
}

// TestCookBudgetGenerate is an operator-invoked fixture generator. Ordinary
// tests do not write a file or perform budget measurements.
func TestCookBudgetGenerate(t *testing.T) {
	path := os.Getenv("COOK_BUDGET_GENERATE")
	if path == "" {
		return
	}
	count, err := strconv.Atoi(os.Getenv("COOK_BUDGET_TURNS"))
	if err != nil || count <= 0 {
		t.Fatal("COOK_BUDGET_TURNS must be a positive integer")
	}
	fixture, err := decodeFixtureDocument[cookBudgetFixture](cookNumberBudgetFixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, subject := range fixture.Subjects {
		if subject.Name != os.Getenv("COOK_BUDGET_SUBJECT") {
			continue
		}
		data := cookBudgetDocument(count, subject.Text, subject.Dense)
		if len(data) > 127<<20 {
			t.Fatalf("subject exceeds 127 MiB: %d", len(data))
		}
		if _, err := validateContentBoundary(data, "claude-code", contentStoredRead); err != nil {
			t.Fatalf("stored read refused subject: %v", err)
		}
		if _, err := newTranscriptIndex(transcriptIndexInput{Bytes: data}); err != nil {
			t.Fatalf("content source refused subject: %v", err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("generated turns=%d bytes=%d", count, len(data))
		return
	}
	t.Fatal("unknown COOK_BUDGET_SUBJECT")
}

func TestDenseBudgetFixture(t *testing.T) {
	fixture, err := decodeFixtureDocument[cookBudgetFixture](cookNumberBudgetFixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, subject := range fixture.Subjects {
		if subject.Name != "cook-number-dense-largest-storable" {
			continue
		}
		if !subject.Dense {
			t.Fatal("dense fixture does not select the dense generator")
		}
		data := cookBudgetDocument(21, subject.Text, true)
		if len(data) < 21*1024 || len(data) > 21*3072 {
			t.Fatalf("dense fixture has unexpected size %d", len(data))
		}
		if _, err := validateContentBoundary(data, "claude-code", contentStoredRead); err != nil {
			t.Fatalf("dense fixture refused by stored-read boundary: %v", err)
		}
		if _, err := newTranscriptIndex(transcriptIndexInput{Bytes: data}); err != nil {
			t.Fatalf("dense fixture refused by content source: %v", err)
		}
		return
	}
	t.Fatal("dense subject missing from budget fixture")
}

func cookBudgetDocument(turns int, text string, dense bool) []byte {
	var document bytes.Buffer
	fmt.Fprintf(&document, `{"contractVersion":"0.1.0","kind":"session_detail","sessionDetail":{"id":"budget","harness":"claude-code","startTime":"2026-01-01T00:00:00Z","endTime":"2026-01-01T00:01:00Z","durationMins":1,"totalTokens":0,"tokensIn":0,"tokensOut":0,"turnCount":%d,"toolCallCount":0,"turns":[`, turns)
	for i := 0; i < turns; i++ {
		if i > 0 {
			document.WriteByte(',')
		}
		content := text
		if dense {
			content = strings.Repeat(text, 60)
		}
		fmt.Fprintf(&document, `{"index":%d,"role":"user","content":"prompt %d%s","timestamp":"2026-01-01T00:00:00Z","depth":0`, i, i, content)
		if dense {
			fmt.Fprintf(&document, `,"toolCalls":[{"id":"t%d","name":"Read","arguments":"{\"path\":\"é\\中文\"}","result":"résultat \\ 中文","resultEntryRef":"ref-%d","usage":{"ownerId":"owner-%d","sourceEntryRef":"ref-%d","scope":"tool","completeness":"partial","tokens":{"input":5}}}]`, i, i, i, i)
			if i%20 == 0 {
				document.WriteString(`,"contentParts":[{"type":"image","image":{"omittedReason":"not_published"}}]`)
			}
		}
		document.WriteByte('}')
	}
	document.WriteString(`]}}`)
	return document.Bytes()
}

// BenchmarkCookNumber measures the contract cook itself, excluding JSON capture
// and the rest of Village's partition indexing. Repeated independent benchmark
// samples are needed for percentiles; this deliberately has no timing assertion.
func BenchmarkCookNumber(b *testing.B) {
	fixture, err := decodeFixtureDocument[cookBudgetFixture](cookNumberBudgetFixture)
	if err != nil {
		b.Fatal(err)
	}
	if fixture.Name != "cook-number-100000-turns" || fixture.Turns != 100000 {
		b.Fatal("missing required 100,000-turn cook benchmark fixture")
	}
	var document bytes.Buffer
	document.WriteString(`{"turns":[`)
	for i := 0; i < fixture.Turns; i++ {
		if i != 0 {
			document.WriteByte(',')
		}
		fmt.Fprintf(&document, `{"index":%d,"role":"user","content":"prompt %d","depth":0}`, i, i)
	}
	document.WriteString(`]}`)
	turns, err := schema.NewTranscriptCookTurns(document.Bytes())
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		runtime.GC()
		if _, err := schema.CookTranscriptRows(turns, schema.HarnessClaudeCode); err != nil {
			b.Fatal(err)
		}
	}
	samples := make([]time.Duration, 0, b.N)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		runtime.GC()
		b.StartTimer()
		started := time.Now()
		rows, err := schema.CookTranscriptRows(turns, schema.HarnessClaudeCode)
		if err != nil || len(rows) != len(turns) {
			b.Fatalf("cook returned %d rows: %v", len(rows), err)
		}
		samples = append(samples, time.Since(started))
	}
	b.StopTimer()
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	if len(samples) >= 200 {
		b.Logf("GOMAXPROCS=%d n=%d p50=%s p95=%s p99=%s", runtime.GOMAXPROCS(0), len(samples), samples[(len(samples)*50+99)/100-1], samples[(len(samples)*95+99)/100-1], samples[(len(samples)*99+99)/100-1])
	}
}

// Both passes exclude database, object storage, decryption, authorization and
// owner resolution. The paged pass builds the index and its first partition;
// the content pass prepares and writes the response to an in-memory writer.
// GC and five warmups are outside the timed intervals.
func coldBudgetBench(b *testing.B, content bool) {
	if path := os.Getenv("COOK_BUDGET_INPUT"); path != "" {
		externalBudgetBench(b, path, content)
		return
	}
	fixture, err := decodeFixtureDocument[cookBudgetFixture](cookNumberBudgetFixture)
	if err != nil {
		b.Fatal(err)
	}
	for _, subject := range fixture.Subjects {
		for _, size := range fixture.Sizes {
			b.Run(subject.Name+"/"+size.Name, func(b *testing.B) {
				data := cookBudgetDocument(size.Turns, subject.Text, subject.Dense)
				pass := func() {
					if content {
						prepared, err := contentResponseBody(context.Background(), data)
						if err != nil {
							b.Fatal(err)
						}
						w := httptest.NewRecorder()
						w.WriteHeader(200)
						if _, err := w.Write(prepared.body); err != nil {
							b.Fatal(err)
						}
						return
					}
					index, err := newTranscriptIndex(transcriptIndexInput{Bytes: data})
					if err != nil {
						b.Fatal(err)
					}
					part, err := index.Partition(mainPartitionKey())
					if err != nil || len(part.Rows) != size.Turns {
						b.Fatalf("partition failed: %v", err)
					}
				}
				for i := 0; i < 5 && os.Getenv("COOK_PROFILE_SINGLE") == ""; i++ {
					runtime.GC()
					pass()
				}
				samples := make([]time.Duration, 0, b.N)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					runtime.GC()
					b.StartTimer()
					started := time.Now()
					pass()
					samples = append(samples, time.Since(started))
				}
				b.StopTimer()
				sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
				if len(samples) > 0 {
					b.Logf("input_bytes=%d GOMAXPROCS=%d n=%d p50=%s p95=%s p99=%s", len(data), runtime.GOMAXPROCS(0), len(samples), samples[(len(samples)*50+99)/100-1], samples[(len(samples)*95+99)/100-1], samples[(len(samples)*99+99)/100-1])
				}
			})
		}
	}
}

func externalBudgetBench(b *testing.B, path string, content bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	cold := os.Getenv("COOK_BUDGET_COLD") == "1"
	if cold && b.N != 1 {
		b.Fatal("cold mode requires -benchtime=1x")
	}
	var pass func()
	if content {
		id := pgtype.UUID{Bytes: uuid.MustParse("30000000-0000-4000-8000-000000000001"), Valid: true}
		owner := pgtype.UUID{Bytes: uuid.MustParse("30000000-0000-4000-8000-ffffffffffff"), Valid: true}
		blobs := newFakeBlobStore()
		blobs.put("transcripts/30000000-0000-4000-8000-000000000001.bin", data)
		row := sqlc.Transcript{ID: id, OwnerID: owner, Visibility: "public", ContentRevision: 3, ModelProvider: "claude-code", BlobKey: "transcripts/30000000-0000-4000-8000-000000000001.bin", WrappedDataKey: []byte("key"), EncryptionAlgorithm: "aes-256-gcm-random-nonce-v1", KeyVersion: 1, ContentHash: pgtype.Text{String: schema.ComputeTranscriptHash(data), Valid: true}, BlobSizeBytes: pgtype.Int8{Int64: int64(len(data)), Valid: true}}
		q := &mockQuerier{getTranscriptByID: func(context.Context, pgtype.UUID) (sqlc.Transcript, error) { return row, nil }}
		h := newTestHandler(q, blobs)
		mux := chi.NewRouter()
		mux.Get("/transcripts/{id}/content", h.GetTranscriptContent)
		target := "/transcripts/" + uuid.UUID(id.Bytes).String() + "/content"
		pass = func() {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
			if w.Code != http.StatusOK {
				b.Fatalf("content handler returned %d: %s", w.Code, w.Body.String())
			}
		}
	} else {
		pass = func() {
			index, err := newTranscriptIndex(transcriptIndexInput{Bytes: data})
			if err != nil {
				b.Fatal(err)
			}
			if _, err := index.Partition(mainPartitionKey()); err != nil {
				b.Fatal(err)
			}
		}
	}
	if !cold {
		for range 5 {
			pass()
		}
	}
	type sample struct {
		WallNS    int64  `json:"wall_ns"`
		CPUNS     int64  `json:"cpu_ns"`
		Allocated uint64 `json:"allocated_bytes"`
		MaxRSSKiB int64  `json:"max_rss_kib"`
	}
	samples := make([]sample, 0, b.N)
	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		if !cold {
			runtime.GC()
		}
		var before, after syscall.Rusage
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		if err := syscall.Getrusage(syscall.RUSAGE_SELF, &before); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		started := time.Now()
		pass()
		elapsed := time.Since(started)
		b.StopTimer()
		if err := syscall.Getrusage(syscall.RUSAGE_SELF, &after); err != nil {
			b.Fatal(err)
		}
		runtime.ReadMemStats(&m1)
		cpu := func(r syscall.Rusage) int64 { return r.Utime.Nano() + r.Stime.Nano() }
		samples = append(samples, sample{WallNS: elapsed.Nanoseconds(), CPUNS: cpu(after) - cpu(before), Allocated: m1.TotalAlloc - m0.TotalAlloc, MaxRSSKiB: after.Maxrss})
	}
	encoded, err := json.Marshal(samples)
	if err != nil {
		b.Fatal(err)
	}
	mode := "warm"
	if cold {
		mode = "cold"
	}
	fmt.Printf("mode=%s passes=%d samples=%s\n", mode, len(samples), encoded)
}

func BenchmarkCookNumberCold(b *testing.B) { coldBudgetBench(b, false) }
func BenchmarkContentCold(b *testing.B)    { coldBudgetBench(b, true) }
