package reponame

import (
	"bytes"
	_ "embed"
	"io"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/repository_names.yaml
var repositoryNamesYAML []byte

// requiredCaseNames is the name manifest for testdata/repository_names.yaml.
// Every name the fixture must carry to keep proving the rule: the remote path,
// the project-path fallbacks, and the empty-input boundary. Asserted as exact
// membership, never a count, so a deleted case is named when it goes missing
// and an added case cannot slip in unprotected.
var requiredCaseNames = []string{
	"git remote URL",
	"git remote URL without suffix",
	"fallback to project name last segment",
	"project name with slashes",
	"dash-delimited multi-word project",
	"dash-delimited with version suffix",
	"dash-delimited analysis toolkit",
	"simple project name",
	"empty inputs",
	"git remote preferred over project name",
}

type repositoryNameCase struct {
	Name        string `yaml:"name"`
	ProjectName string `yaml:"project_name"`
	GitRemote   string `yaml:"git_remote"`
	Expected    string `yaml:"expected"`
}

func loadRepositoryNameCases(t *testing.T) []repositoryNameCase {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(repositoryNamesYAML))
	decoder.KnownFields(true)
	var cases []repositoryNameCase
	if err := decoder.Decode(&cases); err != nil {
		t.Fatalf("decode strict repository-name fixture: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("repository-name fixture must contain exactly one YAML document: %v", err)
	}

	seen := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" {
			t.Fatal("repository-name fixture has a case with an empty name")
		}
		if seen[c.Name] {
			t.Fatalf("repository-name fixture repeats case name %q", c.Name)
		}
		seen[c.Name] = true
	}

	declared := make(map[string]bool, len(requiredCaseNames))
	for _, name := range requiredCaseNames {
		declared[name] = true
	}
	var missing, undeclared []string
	for name := range declared {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	for name := range seen {
		if !declared[name] {
			undeclared = append(undeclared, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(undeclared)
	if len(missing) > 0 {
		t.Fatalf("testdata/repository_names.yaml no longer carries %v, which requiredCaseNames declares: "+
			"each of those cases guards a real branch of the normalization rule. Restore the row under its "+
			"exact name rather than deleting it from the manifest.", missing)
	}
	if len(undeclared) > 0 {
		t.Fatalf("testdata/repository_names.yaml carries %v, which requiredCaseNames does not declare: an "+
			"undeclared case is unprotected, so add each new name to the manifest in the same change.", undeclared)
	}
	return cases
}

func TestNormalize(t *testing.T) {
	for _, tc := range loadRepositoryNameCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			if got := Normalize(tc.ProjectName, tc.GitRemote); got != tc.Expected {
				t.Errorf("Normalize(%q, %q) = %q, want %q", tc.ProjectName, tc.GitRemote, got, tc.Expected)
			}
		})
	}
}
