package promptattach

import (
	"bytes"
	_ "embed"
	"io"
	"testing"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/transitions.yaml
var transitionsYAML []byte

//go:embed testdata/visibility_restore.yaml
var visibilityRestoreYAML []byte

// transitionCase is one named row of the closed transition table.
type transitionCase struct {
	Name    string `yaml:"name"`
	From    State  `yaml:"from"`
	To      State  `yaml:"to"`
	Allowed bool   `yaml:"allowed"`
}

// visibilityCase is one named row proving the recorded previous visibility is
// exact rather than a default.
type visibilityCase struct {
	Name       string `yaml:"name"`
	Visibility string `yaml:"visibility"`
}

// loadTransitionCases reads the fixture strictly: one document, known fields
// only, non-empty unique names. The fixture is the acceptance artifact, so it
// must never be an inline table and must never be silently empty.
func loadTransitionCases(t *testing.T) []transitionCase {
	t.Helper()
	decoder := strictDecoder(transitionsYAML)
	var cases []transitionCase
	if err := decoder.Decode(&cases); err != nil {
		t.Fatalf("decode the transitions fixture: %v", err)
	}
	assertSingleDocument(t, decoder, len(cases), "transitions")
	seen := make(map[string]struct{}, len(cases))
	for _, c := range cases {
		if c.Name == "" {
			t.Fatal("every transitions fixture row requires a non-empty name")
		}
		if _, repeated := seen[c.Name]; repeated {
			t.Fatalf("transitions fixture repeats name %q; each row is named once", c.Name)
		}
		seen[c.Name] = struct{}{}
	}
	return cases
}

// loadVisibilityCases reads the visibility-restore fixture strictly.
func loadVisibilityCases(t *testing.T) []visibilityCase {
	t.Helper()
	decoder := strictDecoder(visibilityRestoreYAML)
	var cases []visibilityCase
	if err := decoder.Decode(&cases); err != nil {
		t.Fatalf("decode the visibility_restore fixture: %v", err)
	}
	assertSingleDocument(t, decoder, len(cases), "visibility_restore")
	seen := make(map[string]struct{}, len(cases))
	for _, c := range cases {
		if c.Name == "" {
			t.Fatal("every visibility_restore fixture row requires a non-empty name")
		}
		if _, repeated := seen[c.Name]; repeated {
			t.Fatalf("visibility_restore fixture repeats name %q; each row is named once", c.Name)
		}
		seen[c.Name] = struct{}{}
	}
	return cases
}

func strictDecoder(raw []byte) *yaml.Decoder {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	return decoder
}

// assertSingleDocument fails when the fixture is empty or holds more than one
// YAML document, so a malformed fixture is never silently truncated.
func assertSingleDocument(t *testing.T, decoder *yaml.Decoder, rowCount int, label string) {
	t.Helper()
	if rowCount == 0 {
		t.Fatalf("the %s fixture is empty; it must enumerate its cases", label)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("the %s fixture must be exactly one YAML document; found a second: %v", label, trailing)
	}
}
