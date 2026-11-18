package handler

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/peasant-labs/schema"
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
		Name string `yaml:"name"`
		Text string `yaml:"text"`
	} `yaml:"subjects"`
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
	fixture, err := decodeFixtureDocument[cookBudgetFixture](cookNumberBudgetFixture)
	if err != nil {
		b.Fatal(err)
	}
	for _, subject := range fixture.Subjects {
		for _, size := range fixture.Sizes {
			b.Run(subject.Name+"/"+size.Name, func(b *testing.B) {
				var document bytes.Buffer
				fmt.Fprintf(&document, `{"contractVersion":"0.1.0","kind":"session_detail","sessionDetail":{"id":"budget","harness":"claude-code","startTime":"2026-01-01T00:00:00Z","endTime":"2026-01-01T00:01:00Z","durationMins":1,"totalTokens":0,"tokensIn":0,"tokensOut":0,"turnCount":%d,"toolCallCount":0,"turns":[`, size.Turns)
				for i := 0; i < size.Turns; i++ {
					if i > 0 {
						document.WriteByte(',')
					}
					fmt.Fprintf(&document, `{"index":%d,"role":"user","content":"prompt %d%s","timestamp":"2026-01-01T00:00:00Z","depth":0}`, i, i, subject.Text)
				}
				document.WriteString(`]}}`)
				data := document.Bytes()
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

func BenchmarkCookNumberCold(b *testing.B) { coldBudgetBench(b, false) }
func BenchmarkContentCold(b *testing.B)    { coldBudgetBench(b, true) }
