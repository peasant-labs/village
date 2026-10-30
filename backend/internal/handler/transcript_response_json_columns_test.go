package handler

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"io"
	"testing"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/transcript-response-json-columns.yaml
var transcriptResponseJSONColumnsYAML []byte

type transcriptResponseJSONColumnsCase struct {
	Name                string `yaml:"name"`
	Subagents           string `yaml:"subagents"`
	DiagnosticsWarnings string `yaml:"diagnostics_warnings"`
	Absent              bool   `yaml:"absent"`
	WantSubagentsLen    int    `yaml:"want_subagents_len"`
	WantDiagnosticsLen  int    `yaml:"want_diagnostics_len"`
}

func loadTranscriptResponseJSONColumnsCases(t *testing.T) []transcriptResponseJSONColumnsCase {
	t.Helper()
	d := yaml.NewDecoder(bytes.NewReader(transcriptResponseJSONColumnsYAML))
	d.KnownFields(true)
	var f struct {
		Cases []transcriptResponseJSONColumnsCase `yaml:"cases"`
	}
	if err := d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		t.Fatalf("expected one fixture document, got %v", err)
	}
	required := map[string]bool{
		"populated-columns-serve-arrays": false, "empty-arrays-serve-empty-arrays": false,
		"empty-bytes-serve-null": false, "nil-columns-serve-null": false,
	}
	for _, c := range f.Cases {
		if _, ok := required[c.Name]; ok {
			required[c.Name] = true
		}
	}
	for name, seen := range required {
		if !seen {
			t.Fatalf("required case %q missing", name)
		}
	}
	return f.Cases
}

// TestTranscriptResponseServesJSONColumnsAsJSON pins the wire shape of the two
// JSON columns: the projection must decode with the contract's collective
// transcript type, so a base64 string can never reach a client again.
func TestTranscriptResponseServesJSONColumnsAsJSON(t *testing.T) {
	for _, tc := range loadTranscriptResponseJSONColumnsCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			row := sqlc.Transcript{Subagents: []byte(tc.Subagents), DiagnosticsWarnings: []byte(tc.DiagnosticsWarnings)}
			if tc.Absent {
				row.Subagents, row.DiagnosticsWarnings = nil, nil
			}
			encoded, err := json.Marshal(toTranscriptResponse(row))
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			var decoded schema.VillageCollectiveTranscript
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("decode with the contract type: %v", err)
			}
			if got := subagentsLen(decoded.Subagents); got != tc.WantSubagentsLen {
				t.Fatalf("subagents length = %d, want %d", got, tc.WantSubagentsLen)
			}
			if got := diagnosticsLen(decoded.DiagnosticsWarnings); got != tc.WantDiagnosticsLen {
				t.Fatalf("diagnostics_warnings length = %d, want %d", got, tc.WantDiagnosticsLen)
			}
		})
	}
}

func subagentsLen(v *[]map[string]interface{}) int {
	if v == nil {
		return -1
	}
	return len(*v)
}

func diagnosticsLen(v *[]schema.DiagnosticEntry) int {
	if v == nil {
		return -1
	}
	return len(*v)
}
