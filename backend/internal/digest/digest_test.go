package digest

import (
	"bytes"
	_ "embed"
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/schema"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden render fixtures")

//go:embed testdata/cases.yaml
var casesYAML []byte

type fixtureFile struct {
	Cases []fixtureCase `yaml:"cases"`
}

type fixtureCase struct {
	Name       string             `yaml:"name"`
	VillageURL string             `yaml:"villageUrl"`
	CommitSet  []string           `yaml:"commitSet"`
	Sessions   []fixtureSession   `yaml:"sessions"`
	Commits    []fixtureCommit    `yaml:"commits"`
	Expected   fixtureExpectation `yaml:"expected"`
}

type fixtureSession struct {
	TranscriptID   string        `yaml:"transcriptId"`
	Harness        string        `yaml:"harness"`
	RedactionLevel string        `yaml:"redactionLevel"`
	SessionStart   string        `yaml:"sessionStart"`
	Turns          []fixtureTurn `yaml:"turns"`
}

type fixtureTurn struct {
	Index     int             `yaml:"index"`
	Role      string          `yaml:"role"`
	Content   string          `yaml:"content"`
	Timestamp string          `yaml:"timestamp"`
	Command   *fixtureCommand `yaml:"command"`
}

type fixtureCommand struct {
	Name string `yaml:"name"`
	Args string `yaml:"args"`
}

type fixtureCommit struct {
	TranscriptID string `yaml:"transcriptId"`
	CommitSHA    string `yaml:"commitSha"`
	AuthoredAt   string `yaml:"authoredAt"`
	Additions    *int   `yaml:"additions"`
	Deletions    *int   `yaml:"deletions"`
	FilesChanged *int   `yaml:"filesChanged"`
}

type fixtureExpectation struct {
	SessionCount   int            `yaml:"sessionCount"`
	PromptCount    int            `yaml:"promptCount"`
	CommitsCovered int            `yaml:"commitsCovered"`
	CommitsTotal   int            `yaml:"commitsTotal"`
	Harness        string         `yaml:"harness"`
	RedactionLevel string         `yaml:"redactionLevel"`
	Skills         []fixtureSkill `yaml:"skills"`
}

type fixtureSkill struct {
	Name            string `yaml:"name"`
	InvocationCount int    `yaml:"invocationCount"`
}

func loadCases(t *testing.T) []fixtureCase {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(casesYAML))
	decoder.KnownFields(true)
	var file fixtureFile
	if err := decoder.Decode(&file); err != nil {
		t.Fatalf("decode the digest fixture: %v", err)
	}
	if len(file.Cases) == 0 {
		t.Fatal("the digest fixture is empty")
	}
	seen := map[string]bool{}
	for _, c := range file.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("digest fixture has an empty or repeated case name %q", c.Name)
		}
		seen[c.Name] = true
	}
	return file.Cases
}

func (c fixtureCase) toInput(t *testing.T) Input {
	t.Helper()
	in := Input{VillageURL: c.VillageURL, CommitSet: c.CommitSet}
	for _, s := range c.Sessions {
		id, err := schema.NewTranscriptID(s.TranscriptID)
		if err != nil {
			t.Fatalf("case %s: transcript id %q: %v", c.Name, s.TranscriptID, err)
		}
		session := Session{
			TranscriptID:   id,
			Harness:        schema.Harness(s.Harness),
			RedactionLevel: s.RedactionLevel,
			SessionStart:   parseTime(t, c.Name, s.SessionStart),
		}
		for _, turn := range s.Turns {
			converted := Turn{
				Index:     turn.Index,
				Role:      schema.Role(turn.Role),
				Content:   turn.Content,
				Timestamp: parseTime(t, c.Name, turn.Timestamp),
			}
			if turn.Command != nil {
				converted.Command = &schema.CommandInvocation{Name: turn.Command.Name, Args: turn.Command.Args}
			}
			session.Turns = append(session.Turns, converted)
		}
		in.Sessions = append(in.Sessions, session)
	}
	for _, m := range c.Commits {
		id, err := schema.NewTranscriptID(m.TranscriptID)
		if err != nil {
			t.Fatalf("case %s: commit transcript id %q: %v", c.Name, m.TranscriptID, err)
		}
		in.Commits = append(in.Commits, CommitMatch{
			TranscriptID: id,
			CommitSHA:    m.CommitSHA,
			AuthoredAt:   parseTime(t, c.Name, m.AuthoredAt),
			Additions:    m.Additions,
			Deletions:    m.Deletions,
			FilesChanged: m.FilesChanged,
		})
	}
	return in
}

func parseTime(t *testing.T, caseName, value string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("case %s: timestamp %q: %v", caseName, value, err)
	}
	return ts
}

// TestDigestFixtureCases builds each fixture case, pins the header, and pins the
// rendered output at all three tiers against testdata/golden/*.md (regenerate
// with -update).
func TestDigestFixtureCases(t *testing.T) {
	for _, c := range loadCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			built, err := Build(c.toInput(t))
			if err != nil {
				t.Fatalf("Build: %v", err)
			}

			if built.Header.SessionCount != c.Expected.SessionCount || built.Header.PromptCount != c.Expected.PromptCount {
				t.Fatalf("header sessions/prompts = %d/%d, want %d/%d",
					built.Header.SessionCount, built.Header.PromptCount, c.Expected.SessionCount, c.Expected.PromptCount)
			}
			if built.Header.CommitsCovered != c.Expected.CommitsCovered || built.Header.CommitsTotal != c.Expected.CommitsTotal {
				t.Fatalf("header commits = %d/%d, want %d/%d",
					built.Header.CommitsCovered, built.Header.CommitsTotal, c.Expected.CommitsCovered, c.Expected.CommitsTotal)
			}
			if string(built.Header.Harness) != c.Expected.Harness {
				t.Fatalf("header harness = %q, want %q", built.Header.Harness, c.Expected.Harness)
			}
			if built.Header.RedactionLevel != c.Expected.RedactionLevel {
				t.Fatalf("header redaction level = %q, want %q", built.Header.RedactionLevel, c.Expected.RedactionLevel)
			}
			if len(built.Skills) != len(c.Expected.Skills) {
				t.Fatalf("skills = %+v, want %+v", built.Skills, c.Expected.Skills)
			}
			for i, skill := range c.Expected.Skills {
				if built.Skills[i].Name != skill.Name || built.Skills[i].InvocationCount != skill.InvocationCount {
					t.Fatalf("skills[%d] = %+v, want %+v", i, built.Skills[i], skill)
				}
			}

			for _, tier := range []Tier{CommentTier, CheckRunTier, VillageTier} {
				out, err := Render(built, tier)
				if err != nil {
					t.Fatalf("Render(%s): %v", tier.Name, err)
				}
				if tier.MaxBytes > 0 && len(out) > tier.MaxBytes {
					t.Fatalf("Render(%s) produced %d bytes, over the %d cap", tier.Name, len(out), tier.MaxBytes)
				}
				if strings.Contains(out, "redaction") {
					t.Fatalf("Render(%s) exposed the session redaction level:\n%s", tier.Name, out)
				}
				pinGolden(t, c.Name, tier.Name, out)
			}
		})
	}
}

// TestDigestRedactionPlaceholderSurvives proves a redaction placeholder in a
// prompt is carried verbatim into the complete-tier render.
func TestDigestRedactionPlaceholderSurvives(t *testing.T) {
	for _, c := range loadCases(t) {
		if c.Name != "short_session" {
			continue
		}
		built, err := Build(c.toInput(t))
		if err != nil {
			t.Fatal(err)
		}
		out, err := Render(built, VillageTier)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "second ask with <REDACTED>") {
			t.Fatalf("the redaction placeholder did not survive rendering:\n%s", out)
		}
		return
	}
	t.Fatal("the short_session fixture is missing")
}

// pinGolden compares out to testdata/golden/<case>.<tier>.md, or rewrites it
// under -update.
func pinGolden(t *testing.T, caseName, tier, out string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", caseName+"."+tier+".md")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (regenerate with -update)", path, err)
	}
	if string(want) != out {
		t.Fatalf("golden mismatch for %s / %s\n--- want ---\n%s\n--- got ---\n%s", caseName, tier, want, out)
	}
}

func TestBuildRejectsAnOutOfSetCommitAnchor(t *testing.T) {
	tid := mustTID(t, "7b1e4d2a-9c3f-4e8b-a1d6-2f5c8e9a0b13")
	built, err := Build(Input{
		VillageURL: "https://village.example/transcripts",
		CommitSet:  []string{"1111111111111111111111111111111111111111"},
		Sessions: []Session{{
			TranscriptID: tid, Harness: "claude-code", RedactionLevel: "standard",
			SessionStart: parseTime(t, "out-of-set", "2026-09-06T14:00:00Z"),
			Turns:        []Turn{{Index: 0, Role: schema.RoleUser, Content: "ask", Timestamp: parseTime(t, "out-of-set", "2026-09-06T14:01:00Z")}},
		}},
		Commits: []CommitMatch{{
			TranscriptID: tid,
			CommitSHA:    "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
			AuthoredAt:   parseTime(t, "out-of-set", "2026-09-06T14:02:00Z"),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if built.Header.CommitsCovered != 0 {
		t.Fatalf("commitsCovered = %d, want 0 when no accepted match intersects the current set", built.Header.CommitsCovered)
	}
	for _, item := range built.Items {
		if item.Kind == schema.DigestItemCommit {
			t.Fatalf("an anchor %q was emitted for a commit outside the current set", item.CommitSHA)
		}
	}
}

func TestRenderRefusesAnInvalidDigest(t *testing.T) {
	var base schema.PromptDigest
	for _, c := range loadCases(t) {
		if c.Name != "short_session" {
			continue
		}
		built, err := Build(c.toInput(t))
		if err != nil {
			t.Fatal(err)
		}
		base = built
	}
	if base.Header.SessionCount == 0 {
		t.Fatal("the short_session fixture is missing")
	}

	missingHeaderSkill := base
	missingHeaderSkill.Skills = nil
	if _, err := Render(missingHeaderSkill, VillageTier); err == nil {
		t.Fatal("rendering a digest whose skill item is missing from the header must fail")
	}

	outOfOrder := base
	outOfOrder.Items = append([]schema.PromptDigestItem(nil), base.Items...)
	outOfOrder.Items[0], outOfOrder.Items[len(outOfOrder.Items)-1] = outOfOrder.Items[len(outOfOrder.Items)-1], outOfOrder.Items[0]
	if _, err := Render(outOfOrder, VillageTier); err == nil {
		t.Fatal("rendering an out-of-chronological-order digest must fail")
	}
}

func TestBuildAndRenderNeverLogPromptText(t *testing.T) {
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(previous)

	for _, c := range loadCases(t) {
		built, err := Build(c.toInput(t))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Render(built, VillageTier); err != nil {
			t.Fatal(err)
		}
	}
	logged := buf.String()
	for _, secret := range []string{"first ask", "second ask with <REDACTED>", "/superpowers:brainstorming", "base repo ask"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("a log line leaked prompt text %q: %s", secret, logged)
		}
	}
}

func mustTID(t *testing.T, raw string) schema.TranscriptID {
	t.Helper()
	id, err := schema.NewTranscriptID(raw)
	if err != nil {
		t.Fatalf("transcript id %q: %v", raw, err)
	}
	return id
}
