package projectname

import (
	"bytes"
	_ "embed"
	"io"
	"testing"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/resolution.yaml
var resolutionCasesYAML []byte

//go:embed testdata/privacy-label.yaml
var privacyLabelCasesYAML []byte

// resolutionCase is one row of the precedence-lattice fixture. Every case
// asserts want_display_name, want_source, and want_remote_label.
type resolutionCase struct {
	Name          string `yaml:"name"`
	ProjectHash   string `yaml:"project_hash"`
	OverrideName  string `yaml:"override_name"`
	ConsentedName string `yaml:"consented_name"`
	GitRemote     string `yaml:"git_remote"`
	PrivacyLabel  string `yaml:"privacy_label"`
	// NoLabeler selects the zero-value Resolver (nil Label) instead of the
	// fixture's recognizing testLabeler, proving the zero value is safe.
	NoLabeler       bool   `yaml:"no_labeler"`
	WantDisplayName string `yaml:"want_display_name"`
	WantSource      string `yaml:"want_source"`
	WantRemoteLabel string `yaml:"want_remote_label"`
}

type resolutionFixture struct {
	Cases []resolutionCase `yaml:"cases"`
}

// requiredResolutionCaseNames is the deletion-protection manifest for
// resolution.yaml: every name that must be present for the fixture to
// still prove the full precedence lattice plus the documented edge cases.
// This is a required-NAME manifest, not a count guard — adding a new case
// never breaks this list, only removing a required one does.
var requiredResolutionCaseNames = []string{
	"all-evidence-absent-synthesizes-privacy-label-from-hash",
	"privacy-label-only-tier",
	"remote-only-tier",
	"remote-and-privacy-present-remote-wins",
	"consented-only-tier",
	"consented-and-privacy-present-consented-wins",
	"consented-and-remote-present-consented-wins",
	"consented-remote-privacy-present-consented-wins",
	"override-only-tier",
	"override-and-privacy-present-override-wins",
	"override-and-remote-present-override-wins",
	"override-remote-privacy-present-override-wins",
	"override-and-consented-present-override-wins",
	"override-consented-privacy-present-override-wins",
	"override-consented-remote-present-override-wins",
	"override-consented-remote-privacy-all-present-override-wins",
	"remote-unrecognized-falls-through-to-privacy",
	"published-at-tie-id-asc-tiebreak-consented-pick",
	"mixed-name-same-hash-one-project-one-name",
	"zero-value-resolver-nil-label-is-safe",
}

func loadResolutionFixture(t *testing.T) []resolutionCase {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(resolutionCasesYAML))
	decoder.KnownFields(true)
	var fixture resolutionFixture
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("decode strict resolution fixture: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("resolution fixture must contain exactly one YAML document: %v", err)
	}

	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" {
			t.Fatal("resolution fixture has a case with an empty name")
		}
		if seen[c.Name] {
			t.Fatalf("resolution fixture repeats case name %q", c.Name)
		}
		seen[c.Name] = true
	}
	for _, required := range requiredResolutionCaseNames {
		if !seen[required] {
			t.Fatalf("resolution fixture is missing required case %q", required)
		}
	}
	return fixture.Cases
}

type privacyLabelCase struct {
	Name        string `yaml:"name"`
	ProjectName string `yaml:"project_name"`
	Want        bool   `yaml:"want"`
}

type privacyLabelFixture struct {
	Cases []privacyLabelCase `yaml:"cases"`
}

// requiredPrivacyLabelCaseNames is the deletion-protection manifest for
// privacy-label.yaml.
var requiredPrivacyLabelCaseNames = []string{
	"exact-12-hex-match",
	"eleven-hex-rejected",
	"thirteen-hex-rejected",
	"uppercase-hex-rejected",
	"non-hex-suffix-rejected",
	"real-project-name-collision-is-accepted-as-privacy-label",
	"empty-string-rejected",
}

func loadPrivacyLabelFixture(t *testing.T) []privacyLabelCase {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(privacyLabelCasesYAML))
	decoder.KnownFields(true)
	var fixture privacyLabelFixture
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("decode strict privacy-label fixture: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("privacy-label fixture must contain exactly one YAML document: %v", err)
	}

	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" {
			t.Fatal("privacy-label fixture has a case with an empty name")
		}
		if seen[c.Name] {
			t.Fatalf("privacy-label fixture repeats case name %q", c.Name)
		}
		seen[c.Name] = true
	}
	for _, required := range requiredPrivacyLabelCaseNames {
		if !seen[required] {
			t.Fatalf("privacy-label fixture is missing required case %q", required)
		}
	}
	return fixture.Cases
}

// testLabeler is the fixture's mock RemoteLabeler. It recognizes exactly one
// raw remote and reports every other value, including empty, as unknown —
// standing in for the real schema.RemoteLabel this package is deliberately
// not allowed to import (see RemoteLabeler's doc comment).
func testLabeler(remote string) (string, bool) {
	if remote == "git@github.com:peasant-labs/village.git" {
		return "github.com:peasant-labs/village", true
	}
	return "", false
}

// TestResolve also pins the closed NameSource set's four literal wire
// values: WantSource in each fixture row is a plain YAML string, decoupled
// from the Go constants, so a silently changed NameSourceOverride (etc.)
// literal fails here exactly as it would in a dedicated enum test. See
// requiredResolutionCaseNames for the source-tier coverage this depends on:
// every one of "override", "consented", "remote", "privacy" appears as a
// want_source in resolution.yaml.
func TestResolve(t *testing.T) {
	for _, c := range loadResolutionFixture(t) {
		t.Run(c.Name, func(t *testing.T) {
			r := Resolver{Label: testLabeler}
			if c.NoLabeler {
				r = Resolver{}
			}
			got := r.Resolve(Evidence{
				ProjectHash:   c.ProjectHash,
				OverrideName:  c.OverrideName,
				ConsentedName: c.ConsentedName,
				GitRemote:     c.GitRemote,
				PrivacyLabel:  c.PrivacyLabel,
			})

			if got.DisplayName != c.WantDisplayName {
				t.Errorf("DisplayName = %q, want %q", got.DisplayName, c.WantDisplayName)
			}
			if string(got.Source) != c.WantSource {
				t.Errorf("Source = %q, want %q", got.Source, c.WantSource)
			}
			if got.RemoteLabel != c.WantRemoteLabel {
				t.Errorf("RemoteLabel = %q, want %q", got.RemoteLabel, c.WantRemoteLabel)
			}
			if got.DisplayName == "" {
				t.Error("DisplayName must never be empty")
			}
			if got.DisplayName == "Other" {
				t.Error("DisplayName must never be the literal \"Other\"")
			}
		})
	}
}

// TestResolve_ZeroValueResolverIsSafe proves the RemoteLabeler seam is
// optional at construction time: a Resolver{} with a nil Label behaves
// exactly as one whose labeler always reports ok=false — never panics, and
// still respects the rest of the precedence chain. testdata/resolution.yaml
// carries the same assertion as a named fixture case
// (zero-value-resolver-nil-label-is-safe); this direct unit test pins the
// same behavior without depending on fixture wiring.
func TestResolve_ZeroValueResolverIsSafe(t *testing.T) {
	var r Resolver
	got := r.Resolve(Evidence{
		ProjectHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		GitRemote:   "git@github.com:peasant-labs/village.git",
	})

	if got.RemoteLabel != "" {
		t.Errorf("RemoteLabel = %q, want empty with a zero-value Resolver", got.RemoteLabel)
	}
	if got.Source != NameSourcePrivacy {
		t.Errorf("Source = %q, want %q (last-resort synthesis with the remote unusable)", got.Source, NameSourcePrivacy)
	}
	if got.DisplayName != "project-0123456789ab" {
		t.Errorf("DisplayName = %q, want the last-resort synthesized label", got.DisplayName)
	}
}

// TestResolve_EntirelyEmptyEvidenceNeverReturnsEmptyOrOther proves the hard
// invariant holds even for evidence no real caller can produce (a project
// with no hash at all — the publish boundary elsewhere in the system
// enforces ProjectHash as NOT NULL, so this branch is otherwise
// unreachable). Resolve must still never return an empty DisplayName or the
// literal "Other".
func TestResolve_EntirelyEmptyEvidenceNeverReturnsEmptyOrOther(t *testing.T) {
	var r Resolver
	got := r.Resolve(Evidence{})
	if got.DisplayName == "" {
		t.Error("DisplayName must never be empty, even for entirely empty evidence")
	}
	if got.DisplayName == "Other" {
		t.Error("DisplayName must never be the literal \"Other\", even for entirely empty evidence")
	}
}

func TestIsPrivacyLabel(t *testing.T) {
	for _, c := range loadPrivacyLabelFixture(t) {
		t.Run(c.Name, func(t *testing.T) {
			if got := IsPrivacyLabel(c.ProjectName); got != c.Want {
				t.Errorf("IsPrivacyLabel(%q) = %v, want %v", c.ProjectName, got, c.Want)
			}
		})
	}
}
