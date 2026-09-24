package promptattach

import (
	"bytes"
	_ "embed"
	"io"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/transitions.yaml
var transitionsYAML []byte

//go:embed testdata/visibility_restore.yaml
var visibilityRestoreYAML []byte

// requiredTransitionCaseNames is the name manifest for testdata/transitions.yaml:
// every ordered pair of the closed state menu. Exact membership, never a count,
// so a deleted or renamed row fails by name. The cross-product check in
// TestTransitionTableMatchesFixture pins the SHAPE; this pins the names.
var requiredTransitionCaseNames = []string{
	"requested_to_requested", "requested_to_waiting", "requested_to_preview", "requested_to_attached", "requested_to_detached",
	"waiting_to_requested", "waiting_to_waiting", "waiting_to_preview", "waiting_to_attached", "waiting_to_detached",
	"preview_to_requested", "preview_to_waiting", "preview_to_preview", "preview_to_attached", "preview_to_detached",
	"attached_to_requested", "attached_to_waiting", "attached_to_preview", "attached_to_attached", "attached_to_detached",
	"detached_to_requested", "detached_to_waiting", "detached_to_preview", "detached_to_attached", "detached_to_detached",
}

// requiredVisibilityCaseNames is the name manifest for
// testdata/visibility_restore.yaml: one row per tier a transcript can hold
// before an attach widens it. A deleted row would silently stop proving that
// tier is restored exactly rather than by a coincidental default.
var requiredVisibilityCaseNames = []string{
	"private_is_preserved_exactly",
	"shared_is_preserved_exactly",
	"public_is_preserved_exactly",
}

// assertExactCaseNames holds a fixture to exact membership against its manifest.
// A missing name is a lost case; an undeclared one is unprotected, so it must
// be added to the manifest in the same change. Never a count: a count churns on
// every addition and does not say what disappeared.
func assertExactCaseNames(t *testing.T, fixture string, present map[string]struct{}, required []string) {
	t.Helper()
	declared := make(map[string]bool, len(required))
	for _, name := range required {
		declared[name] = true
	}
	var missing, undeclared []string
	for _, name := range required {
		if _, ok := present[name]; !ok {
			missing = append(missing, name)
		}
	}
	for name := range present {
		if !declared[name] {
			undeclared = append(undeclared, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(undeclared)
	if len(missing) > 0 {
		t.Fatalf("testdata/%s.yaml no longer carries %v, which its manifest declares: restore each row under its exact name.", fixture, missing)
	}
	if len(undeclared) > 0 {
		t.Fatalf("testdata/%s.yaml carries %v, which its manifest does not declare: an undeclared case is unprotected, so add each new name to the manifest in the same change.", fixture, undeclared)
	}
}

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
	assertExactCaseNames(t, "transitions", seen, requiredTransitionCaseNames)
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
	assertExactCaseNames(t, "visibility_restore", seen, requiredVisibilityCaseNames)
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
