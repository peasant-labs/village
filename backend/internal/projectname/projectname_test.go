package projectname

import (
	"bytes"
	_ "embed"
	"io"
	"sort"
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

// requiredResolutionCaseNames is the name manifest for resolution.yaml: every
// name the fixture must carry for it to still prove the full precedence
// lattice plus the documented edge cases. It is asserted as EXACT membership
// (see assertExactFixtureCaseNames), so a new case is added here in the same
// change that adds the row, and a deleted case is named when it goes missing.
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

// assertExactFixtureCaseNames holds one fixture in this package to EXACT
// membership against its required-name manifest, in BOTH directions: a
// declared name that is no longer in the file fails, and a case in the file
// that the manifest does not declare fails too.
//
// Both directions matter for different reasons. A missing name means a
// boundary silently stopped being covered — the deletion the manifest exists
// to catch. An undeclared name means a case is running that nothing protects:
// deleting it later would be invisible, so an addition that skips the manifest
// quietly re-opens the hole the manifest closed.
//
// This is a name manifest, never a count. A count says only that somebody
// changed the file; it cannot say WHICH case went missing, it goes stale on
// every legitimate addition, and two branches that each add a case collide on
// the same integer.
func assertExactFixtureCaseNames(t *testing.T, fixture string, present map[string]bool, required []string) {
	t.Helper()
	declared := make(map[string]bool, len(required))
	for _, name := range required {
		declared[name] = true
	}
	var missing, undeclared []string
	for name := range declared {
		if !present[name] {
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
		t.Fatalf("testdata/%s.yaml no longer contains the case(s) %v, which its manifest in projectname_test.go "+
			"declares. Each of those cases exists because losing it hides a real failure in project-name "+
			"resolution, and it went missing when the fixture was last edited. Restore the row under its exact "+
			"name rather than deleting the name from the manifest; if the case really is obsolete, say why in the "+
			"same change that removes both.", fixture, missing)
	}
	if len(undeclared) > 0 {
		t.Fatalf("testdata/%s.yaml carries the case(s) %v, which its manifest in projectname_test.go does not "+
			"declare. An undeclared case is unprotected: a later edit could delete it and no test would notice, "+
			"which is exactly what the manifest exists to prevent. Add each new name to the manifest in the same "+
			"change that adds the row.", fixture, undeclared)
	}
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
	assertExactFixtureCaseNames(t, "resolution", seen, requiredResolutionCaseNames)
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

// requiredPrivacyLabelCaseNames is the name manifest for privacy-label.yaml,
// asserted as EXACT membership.
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
	assertExactFixtureCaseNames(t, "privacy-label", seen, requiredPrivacyLabelCaseNames)
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

//go:embed testdata/path-tier.yaml
var pathTierCasesYAML []byte

// pathTierCase is one row of the path-tier fixture. Every row asserts the
// full Resolved triple, and every row states why it exists.
type pathTierCase struct {
	Name            string `yaml:"name"`
	Why             string `yaml:"why"`
	ProjectHash     string `yaml:"project_hash"`
	OverrideName    string `yaml:"override_name"`
	ConsentedName   string `yaml:"consented_name"`
	GitRemote       string `yaml:"git_remote"`
	ProjectPath     string `yaml:"project_path"`
	PrivacyLabel    string `yaml:"privacy_label"`
	WantDisplayName string `yaml:"want_display_name"`
	WantSource      string `yaml:"want_source"`
	WantRemoteLabel string `yaml:"want_remote_label"`
}

type pathTierFixture struct {
	Cases []pathTierCase `yaml:"cases"`
}

// requiredPathTierCaseNames is the name manifest for path-tier.yaml, asserted
// as EXACT membership: a declared row that goes missing and an undeclared row
// that appears both fail by name.
//
// Each name is load-bearing. remote_beats_path is the only row that fails
// when the path tier is moved above the remote tier; path_when_no_remote is
// the only row that fails when the tier is deleted; override_beats_path and
// consented_beats_path pin the two tiers above; no_path_privacy_label pins
// that an ABSENT path changes nothing.
var requiredPathTierCaseNames = []string{
	"remote_beats_path",
	"path_when_no_remote",
	"override_beats_path",
	"consented_beats_path",
	"no_path_privacy_label",
}

func loadPathTierFixture(t *testing.T) []pathTierCase {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(pathTierCasesYAML))
	decoder.KnownFields(true)
	var fixture pathTierFixture
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("decode strict path-tier fixture: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("path-tier fixture must contain exactly one YAML document: %v", err)
	}

	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" {
			t.Fatal("path-tier fixture has a case with an empty name")
		}
		if seen[c.Name] {
			t.Fatalf("path-tier fixture repeats case name %q", c.Name)
		}
		if c.Why == "" {
			t.Fatalf("path-tier case %q states no reason it exists; a case nobody can justify cannot be maintained", c.Name)
		}
		seen[c.Name] = true
	}
	assertExactFixtureCaseNames(t, "path-tier", seen, requiredPathTierCaseNames)
	return fixture.Cases
}

// TestResolve_PathTier drives the path tier through the same exported
// Resolve every surface calls. It also pins the literal wire value "path":
// want_source is a plain YAML string, decoupled from the NameSourcePath
// constant, so silently changing that literal fails here.
func TestResolve_PathTier(t *testing.T) {
	for _, c := range loadPathTierFixture(t) {
		t.Run(c.Name, func(t *testing.T) {
			got := Resolver{Label: testLabeler}.Resolve(Evidence{
				ProjectHash:   c.ProjectHash,
				OverrideName:  c.OverrideName,
				ConsentedName: c.ConsentedName,
				GitRemote:     c.GitRemote,
				ProjectPath:   c.ProjectPath,
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

// TestResolve_PathIsDisplayedVerbatim proves the village applies NO transform
// of its own to a stored path. The value arrives already redacted from the
// publishing client, so re-deriving or masking it here would either corrupt a
// correct value or paper over an incorrect one; both are worse than showing
// what was stored. This is the resolver-level half of the same guarantee the
// handler's wire-level fixture row asserts end to end.
func TestResolve_PathIsDisplayedVerbatim(t *testing.T) {
	// Deliberately awkward shapes: a trailing slash, an inner space, a
	// Windows form and a value that is not path-shaped at all. None of them
	// is a value the village may rewrite.
	for _, stored := range []string{
		"/<PATH>/app",
		"/<PATH>/app/",
		"/<PATH>/my app",
		`C:\<PATH>\app`,
		"not-a-path",
	} {
		got := Resolver{Label: testLabeler}.Resolve(Evidence{
			ProjectHash: "6666666666666666666666666666666666666666666666666666666666666666",
			ProjectPath: stored,
		})
		if got.DisplayName != stored {
			t.Errorf("stored path %q rendered as %q; the village must display a stored path byte for byte", stored, got.DisplayName)
		}
		if got.Source != NameSourcePath {
			t.Errorf("stored path %q resolved under source %q, want %q", stored, got.Source, NameSourcePath)
		}
	}
}
