package handler

import (
	"bytes"
	_ "embed"
	"fmt"
	"testing"

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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := schema.CookTranscriptRows(turns, schema.HarnessClaudeCode)
		if err != nil || len(rows) != len(turns) {
			b.Fatalf("cook returned %d rows: %v", len(rows), err)
		}
	}
}
