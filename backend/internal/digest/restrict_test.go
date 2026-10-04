package digest

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/schema"
)

//go:embed testdata/restrict.yaml
var restrictYAML []byte

type restrictFile struct {
	Cases []restrictCase `yaml:"cases"`
}

type restrictCase struct {
	Name                 string              `yaml:"name"`
	VillageURL           string              `yaml:"villageUrl"`
	CommitSet            []string            `yaml:"commitSet"`
	Sessions             []fixtureSession    `yaml:"sessions"`
	Commits              []fixtureCommit     `yaml:"commits"`
	Keep                 []string            `yaml:"keep"`
	MatchesFilteredBuild bool                `yaml:"matches_filtered_build"`
	Expected             restrictExpectation `yaml:"expected"`
}

type restrictExpectation struct {
	SessionCount   int            `yaml:"sessionCount"`
	PromptCount    int            `yaml:"promptCount"`
	CommitsCovered int            `yaml:"commitsCovered"`
	Prompts        []string       `yaml:"prompts"`
	Skills         []fixtureSkill `yaml:"skills"`
	Absent         []string       `yaml:"absent"`
}

// requiredRestrictCaseNames is the name manifest for testdata/restrict.yaml.
// Exact membership, never a count.
var requiredRestrictCaseNames = []string{
	"the_later_session_alone",
	"the_earlier_session_alone",
	"every_transcript_is_unchanged",
	"no_transcript_is_an_empty_chain",
	"a_shared_commit_goes_with_the_removed_transcript",
}

func loadRestrictCases(t *testing.T) []restrictCase {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(restrictYAML))
	decoder.KnownFields(true)
	var file restrictFile
	if err := decoder.Decode(&file); err != nil {
		t.Fatalf("decode the restrict fixture: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range file.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("restrict fixture has an empty or repeated case name %q", c.Name)
		}
		seen[c.Name] = true
	}
	declared := map[string]bool{}
	var missing, undeclared []string
	for _, name := range requiredRestrictCaseNames {
		declared[name] = true
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
		t.Fatalf("testdata/restrict.yaml no longer carries %v, which its manifest declares: restore each case under its exact name.", missing)
	}
	if len(undeclared) > 0 {
		t.Fatalf("testdata/restrict.yaml carries %v, which its manifest does not declare: add each new name to the manifest in the same change.", undeclared)
	}
	return file.Cases
}

func (c restrictCase) input(t *testing.T) Input {
	t.Helper()
	return fixtureCase{Name: c.Name, VillageURL: c.VillageURL, CommitSet: c.CommitSet, Sessions: c.Sessions, Commits: c.Commits}.toInput(t)
}

// TestRestrictShowsOnlyWhatTheReaderMayOpen narrows each built chain to the
// transcripts one reader may open and pins what remains: no item and no text of
// a removed transcript, the prompts renumbered from one, the header and skills
// recounted, and, where the case says so, exactly the chain a fresh build over
// the kept transcripts produces.
func TestRestrictShowsOnlyWhatTheReaderMayOpen(t *testing.T) {
	for _, c := range loadRestrictCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			in := c.input(t)
			full, err := Build(in)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			keep := map[schema.TranscriptID]bool{}
			for _, raw := range c.Keep {
				keep[mustTID(t, raw)] = true
			}
			restricted, err := Restrict(full, func(id schema.TranscriptID) bool { return keep[id] })
			if err != nil {
				t.Fatalf("Restrict: %v", err)
			}

			if restricted.Header.SessionCount != c.Expected.SessionCount ||
				restricted.Header.PromptCount != c.Expected.PromptCount ||
				restricted.Header.CommitsCovered != c.Expected.CommitsCovered {
				t.Fatalf("header sessions/prompts/covered = %d/%d/%d, want %d/%d/%d",
					restricted.Header.SessionCount, restricted.Header.PromptCount, restricted.Header.CommitsCovered,
					c.Expected.SessionCount, c.Expected.PromptCount, c.Expected.CommitsCovered)
			}
			if restricted.Header.Harness != full.Header.Harness || restricted.Header.RedactionLevel != full.Header.RedactionLevel {
				t.Fatalf("header harness/level = %q/%q, want the chain's %q/%q: the contract requires both", restricted.Header.Harness, restricted.Header.RedactionLevel, full.Header.Harness, full.Header.RedactionLevel)
			}
			if restricted.Header.CommitsTotal != full.Header.CommitsTotal {
				t.Fatalf("commitsTotal = %d, want the pull request's %d: the commit set is the pull request's, not a transcript's",
					restricted.Header.CommitsTotal, full.Header.CommitsTotal)
			}

			prompts := []string{}
			for _, item := range restricted.Items {
				if !keep[item.TranscriptID] {
					t.Fatalf("item %+v belongs to a transcript the reader may not open", item)
				}
				if item.Kind == schema.DigestItemPrompt {
					prompts = append(prompts, item.Text)
					if *item.Ordinal != len(prompts) {
						t.Fatalf("prompt %q has ordinal %d, want %d: a gap would count the prompts the reader cannot see", item.Text, *item.Ordinal, len(prompts))
					}
				}
			}
			if !reflect.DeepEqual(prompts, c.Expected.Prompts) {
				t.Fatalf("prompts = %q, want %q", prompts, c.Expected.Prompts)
			}

			skills := make([]fixtureSkill, 0, len(restricted.Skills))
			for _, skill := range restricted.Skills {
				skills = append(skills, fixtureSkill{Name: skill.Name, InvocationCount: skill.InvocationCount})
			}
			if !reflect.DeepEqual(skills, c.Expected.Skills) {
				t.Fatalf("skills = %+v, want %+v", skills, c.Expected.Skills)
			}

			encoded, err := json.Marshal(restricted)
			if err != nil {
				t.Fatal(err)
			}
			for _, needle := range c.Expected.Absent {
				if strings.Contains(string(encoded), needle) {
					t.Fatalf("the restricted digest still carries %q:\n%s", needle, encoded)
				}
			}

			filteredInput := in
			filteredInput.Sessions = nil
			filteredInput.Commits = nil
			for _, session := range in.Sessions {
				if keep[session.TranscriptID] {
					filteredInput.Sessions = append(filteredInput.Sessions, session)
				}
			}
			for _, commit := range in.Commits {
				if keep[commit.TranscriptID] {
					filteredInput.Commits = append(filteredInput.Commits, commit)
				}
			}
			fresh, err := Build(filteredInput)
			if err != nil {
				t.Fatalf("Build over the kept transcripts: %v", err)
			}
			if fresh.Items == nil {
				// Build leaves an empty chain's items nil; Restrict keeps the
				// non-null array the contract declares. Same chain either way.
				fresh.Items = []schema.PromptDigestItem{}
			}
			if c.MatchesFilteredBuild {
				// The header's harness is the whole chain's first session; a fresh
				// build over nothing has none, and these cases share one.
				fresh.Header.Harness = restricted.Header.Harness
				fresh.Header.RedactionLevel = restricted.Header.RedactionLevel
				if !reflect.DeepEqual(restricted, fresh) {
					t.Fatalf("restricted chain differs from a fresh build over the kept transcripts\n--- restricted ---\n%+v\n--- fresh ---\n%+v", restricted, fresh)
				}
			} else if reflect.DeepEqual(restricted, fresh) {
				t.Fatal("the case says the restricted chain cannot equal a fresh build, but it does: update matches_filtered_build")
			}

			// The restricted chain is still a digest the pull request can render:
			// every kept transcript as a listed row.
			pull := PullRequest{Digest: restricted, CommitSet: c.CommitSet}
			for _, item := range restricted.Items {
				if item.Kind == schema.DigestItemSession {
					pull.Rows = append(pull.Rows, Row{TranscriptID: item.TranscriptID, Title: "kept", Author: "reader", Listed: true, URL: "https://village.example/transcripts/" + string(item.TranscriptID)})
				}
			}
			if _, err := Render(pull, CommentTier); err != nil {
				t.Fatalf("Render the restricted chain: %v", err)
			}
		})
	}
}

// TestRestrictKeepingEverythingIsTheSameDigest proves the reader who may open
// every transcript sees the stored digest byte for byte, over every case the
// render goldens pin as well as the restrict cases.
func TestRestrictKeepingEverythingIsTheSameDigest(t *testing.T) {
	inputs := map[string]Input{}
	for _, c := range loadCases(t) {
		inputs["cases/"+c.Name] = c.toInput(t)
	}
	for _, c := range loadRestrictCases(t) {
		inputs["restrict/"+c.Name] = c.input(t)
	}
	for name, in := range inputs {
		full, err := Build(in)
		if err != nil {
			t.Fatalf("%s: Build: %v", name, err)
		}
		same, err := Restrict(full, func(schema.TranscriptID) bool { return true })
		if err != nil {
			t.Fatalf("%s: Restrict: %v", name, err)
		}
		want, _ := json.Marshal(full)
		got, _ := json.Marshal(same)
		if !bytes.Equal(want, got) {
			t.Fatalf("%s: keeping every transcript changed the digest\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
		}
	}
}
