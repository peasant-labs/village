package handler

import (
	"bytes"
	_ "embed"
	"fmt"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/peasant-labs/schema"
)

//go:embed testdata/cook_number_budget.yaml
var cookNumberBudgetFixture []byte

// BenchmarkCookNumber measures the contract cook itself, excluding JSON capture
// and the rest of Village's partition indexing. Repeated independent benchmark
// samples are needed for percentiles; this deliberately has no timing assertion.
func BenchmarkCookNumber(b *testing.B) {
	fixture, err := decodeFixtureDocument[struct {
		Name  string `yaml:"name"`
		Turns int    `yaml:"turns"`
	}](cookNumberBudgetFixture)
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

// BenchmarkCookNumberCold measures a fresh content index and its first
// partition, including the structural scan. GC and the warmups are outside
// each timed interval. Run with -benchtime=200x for meaningful percentiles.
func BenchmarkCookNumberCold(b *testing.B) {
	var document bytes.Buffer
	document.WriteString(`{"sessionDetail":{"harness":"claude-code","turns":[`)
	for i := 0; i < 100000; i++ {
		if i > 0 {
			document.WriteByte(',')
		}
		fmt.Fprintf(&document, `{"index":%d,"role":"user","content":"prompt %d","depth":0}`, i, i)
	}
	document.WriteString(`]}}`)
	data := document.Bytes()
	pass := func() {
		index, err := newTranscriptIndex(transcriptIndexInput{Bytes: data})
		if err != nil {
			b.Fatal(err)
		}
		part, err := index.Partition(mainPartitionKey())
		if err != nil || len(part.Rows) != 100000 {
			b.Fatalf("partition failed: %v", err)
		}
	}
	for i := 0; i < 5; i++ {
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
	if len(samples) >= 200 {
		b.Logf("GOMAXPROCS=%d n=%d p50=%s p95=%s p99=%s", runtime.GOMAXPROCS(0), len(samples), samples[(len(samples)*50+99)/100-1], samples[(len(samples)*95+99)/100-1], samples[(len(samples)*99+99)/100-1])
	}
}
