package digest

import (
	"bytes"
	_ "embed"
	"flag"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
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
	Cases   []fixtureCase   `yaml:"cases"`
	Budgets []fixtureBudget `yaml:"budgets"`
}

type fixtureBudget struct {
	Name          string   `yaml:"name"`
	Case          string   `yaml:"case"`
	MaxBytes      int      `yaml:"maxBytes"`
	Present       []string `yaml:"present"`
	Absent        []string `yaml:"absent"`
	ExpectedError bool     `yaml:"expectedError"`
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
	Title          string        `yaml:"title"`
	Author         string        `yaml:"author"`
	Listed         bool          `yaml:"listed"`
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
	// Present is text every render must contain, and Absent text no render may
	// contain, at every digest tier.
	Present []string `yaml:"present"`
	Absent  []string `yaml:"absent"`
}

type fixtureSkill struct {
	Name            string `yaml:"name"`
	InvocationCount int    `yaml:"invocationCount"`
}

// requiredDigestCaseNames is the name manifest for testdata/cases.yaml: the
// short single-session case, the long multi-prompt case, the fork case with two
// sessions and a commit anchor, the mixed-audience case with a transcript only a
// collective can read, and the hostile-text case. Exact membership, never a
// count, so a deleted or renamed case fails by name instead of silently
// shrinking the corpus its golden renders pin.
var requiredDigestCaseNames = []string{
	"short_session",
	"long_session",
	"fork_pull_request",
	"mixed_audience",
	"hostile_text",
	"clipped_text_and_commit_tails",
	"skills_only",
}

func loadCases(t *testing.T) []fixtureCase {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(casesYAML))
	decoder.KnownFields(true)
	var file fixtureFile
	if err := decoder.Decode(&file); err != nil {
		t.Fatalf("decode the digest fixture: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatal("digest fixtures must contain one YAML document")
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
	declared := make(map[string]bool, len(requiredDigestCaseNames))
	for _, name := range requiredDigestCaseNames {
		declared[name] = true
	}
	var missing, undeclared []string
	for _, name := range requiredDigestCaseNames {
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
		t.Fatalf("testdata/cases.yaml no longer carries %v, which its manifest declares: restore each case under its exact name.", missing)
	}
	if len(undeclared) > 0 {
		t.Fatalf("testdata/cases.yaml carries %v, which its manifest does not declare: an undeclared case is unprotected, so add each new name to the manifest in the same change.", undeclared)
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

// toPullRequest is what the handler hands the renderer for one case: the built
// digest, the per-transcript rows the fixture declares, and the commit set.
func (c fixtureCase) toPullRequest(built schema.PromptDigest) PullRequest {
	pull := PullRequest{Digest: built, CommitSet: c.CommitSet}
	for _, s := range c.Sessions {
		pull.Rows = append(pull.Rows, Row{
			TranscriptID: schema.TranscriptID(s.TranscriptID),
			Title:        s.Title,
			Author:       s.Author,
			Listed:       s.Listed,
			URL:          "https://village.example/transcripts/" + s.TranscriptID,
		})
	}
	return pull
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
				out, err := Render(c.toPullRequest(built), tier)
				if err != nil {
					t.Fatalf("Render(%s): %v", tier.Name, err)
				}
				if tier.MaxBytes > 0 && len(out) > tier.MaxBytes {
					t.Fatalf("Render(%s) produced %d bytes, over the %d cap", tier.Name, len(out), tier.MaxBytes)
				}
				assertRenderText(t, tier.Name, out, c.Expected.Present, c.Expected.Absent)
				assertMarkdownStaysInert(t, tier.Name, out)
				pinGolden(t, c.Name, tier.Name, out)
			}

			// The preview comment names how many transcripts match and where to
			// review them, and nothing any of them says.
			preview := RenderPreview(built.Header.SessionCount, c.VillageURL)
			pinGolden(t, c.Name, "preview", preview)
		})
	}
}

// TestDigestRedactionPlaceholderSurvives proves a redaction placeholder in a
// prompt is carried into the complete-tier render, escaped so GitHub shows it as
// the text it is rather than dropping it as an unknown tag.
func TestDigestRedactionPlaceholderSurvives(t *testing.T) {
	for _, c := range loadCases(t) {
		if c.Name != "short_session" {
			continue
		}
		built, err := Build(c.toInput(t))
		if err != nil {
			t.Fatal(err)
		}
		out, err := Render(c.toPullRequest(built), VillageTier)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, `second ask with \<REDACTED\>`) {
			t.Fatalf("the redaction placeholder did not survive rendering:\n%s", out)
		}
		return
	}
	t.Fatal("the short_session fixture is missing")
}

// TestDigestSkillArgsAreCarriedAndRendered proves a skill invocation's argument
// is carried on the item and rendered next to the command name.
func TestDigestSkillArgsAreCarriedAndRendered(t *testing.T) {
	for _, c := range loadCases(t) {
		if c.Name != "short_session" {
			continue
		}
		built, err := Build(c.toInput(t))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range built.Items {
			if item.Kind != schema.DigestItemSkill {
				continue
			}
			found = true
			if item.Text != "/superpowers:brainstorming" || item.Args != "the header story" {
				t.Fatalf("skill item = %q / args %q, want the name and its argument", item.Text, item.Args)
			}
		}
		if !found {
			t.Fatal("no skill item was built")
		}
		out, err := Render(c.toPullRequest(built), VillageTier)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "/superpowers:brainstorming the header story") {
			t.Fatalf("rendered output does not show the skill args:\n%s", out)
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
	var base PullRequest
	for _, c := range loadCases(t) {
		if c.Name != "short_session" {
			continue
		}
		built, err := Build(c.toInput(t))
		if err != nil {
			t.Fatal(err)
		}
		base = c.toPullRequest(built)
	}
	if base.Digest.Header.SessionCount == 0 {
		t.Fatal("the short_session fixture is missing")
	}
	if _, err := Render(base, VillageTier); err != nil {
		t.Fatalf("the unmodified case must render before its mutations are refused: %v", err)
	}

	missingHeaderSkill := base
	missingHeaderSkill.Digest.Skills = nil
	if _, err := Render(missingHeaderSkill, VillageTier); err == nil {
		t.Fatal("rendering a digest whose skill item is missing from the header must fail")
	}

	outOfOrder := base
	outOfOrder.Digest.Items = append([]schema.PromptDigestItem(nil), base.Digest.Items...)
	last := len(outOfOrder.Digest.Items) - 1
	outOfOrder.Digest.Items[0], outOfOrder.Digest.Items[last] = outOfOrder.Digest.Items[last], outOfOrder.Digest.Items[0]
	if _, err := Render(outOfOrder, VillageTier); err == nil {
		t.Fatal("rendering an out-of-chronological-order digest must fail")
	}

	// A transcript with no row has no stated audience, so nothing is guessed:
	// the render is refused rather than defaulting to listed or unlisted.
	noRows := base
	noRows.Rows = nil
	if _, err := Render(noRows, CommentTier); err == nil {
		t.Fatal("rendering a digest session that has no row must fail")
	}

	// Every row links the pull request's page, so a digest that names none
	// cannot be rendered into links that go nowhere.
	noPage := base
	noPage.Digest.Header.VillageURL = ""
	if _, err := Render(noPage, CommentTier); err == nil {
		t.Fatal("rendering a digest with no village page must fail")
	}
}

// assertRenderText checks one render against a case's present and absent text.
func assertRenderText(t *testing.T, surface, out string, present, absent []string) {
	t.Helper()
	for _, want := range present {
		if !strings.Contains(out, want) {
			t.Fatalf("the %s render is missing %q:\n%s", surface, want, out)
		}
	}
	for _, unwanted := range absent {
		if strings.Contains(out, unwanted) {
			t.Fatalf("the %s render carries %q, which it must not:\n%s", surface, unwanted, out)
		}
	}
}

// renderMarkup is the HTML the renderer writes itself. Every other angle bracket
// in a render is user text and must be escaped.
var renderMarkup = []string{"<details>", "</details>", "<summary>", "</summary>", "<!-- -->"}

// assertMarkdownStaysInert checks the structure user text must never change:
// each table row has exactly its four cell borders, no backtick opens code, and
// no angle bracket opens a tag other than the renderer's own.
func assertMarkdownStaysInert(t *testing.T, surface, out string) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "| ") {
			if pipes := unescapedCount(line, '|'); pipes != 4 {
				t.Fatalf("the %s table row has %d cell borders, want 4: %q", surface, pipes, line)
			}
		}
	}
	if ticks := unescapedCount(out, '`'); ticks != 0 {
		t.Fatalf("the %s render has %d unescaped backticks, want none:\n%s", surface, ticks, out)
	}
	stripped := out
	for _, markup := range renderMarkup {
		stripped = strings.ReplaceAll(stripped, markup, "")
	}
	if angles := unescapedCount(stripped, '<'); angles != 0 {
		t.Fatalf("the %s render has %d unescaped angle brackets outside its own markup, want none:\n%s", surface, angles, out)
	}
}

// unescapedCount counts the occurrences of c not preceded by an odd run of
// backslashes.
func unescapedCount(text string, c byte) int {
	n := 0
	for i := 0; i < len(text); i++ {
		if text[i] != c {
			continue
		}
		slashes := 0
		for j := i - 1; j >= 0 && text[j] == '\\'; j-- {
			slashes++
		}
		if slashes%2 == 0 {
			n++
		}
	}
	return n
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
		if _, err := Render(c.toPullRequest(built), VillageTier); err != nil {
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

// The same renderer is exercised with small fixture-owned budgets, so the
// overflow branches are observable without oversized process fixtures.
func TestRenderFixtureBudgets(t *testing.T) {
	cases := loadCases(t)
	byName := make(map[string]fixtureCase, len(cases))
	for _, c := range cases {
		byName[c.Name] = c
	}
	var file fixtureFile
	decoder := yaml.NewDecoder(bytes.NewReader(casesYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		"table_rows_stop_at_the_byte_budget":        true,
		"prompt_blocks_stop_at_the_byte_budget":     true,
		"a_prompt_frame_that_cannot_fit_is_omitted": true,
		"a_table_that_cannot_fit_is_refused":        true,
	}
	for _, c := range file.Budgets {
		if !required[c.Name] {
			t.Fatalf("unknown or repeated budget fixture %q", c.Name)
		}
		delete(required, c.Name)
		t.Run(c.Name, func(t *testing.T) {
			base, exists := byName[c.Case]
			if !exists || c.MaxBytes <= 0 {
				t.Fatal("budget fixture needs a named input and positive cap")
			}
			built, err := Build(base.toInput(t))
			if err != nil {
				t.Fatal(err)
			}
			out, err := Render(base.toPullRequest(built), Tier{Name: c.Name, MaxBytes: c.MaxBytes})
			if (err != nil) != c.ExpectedError {
				t.Fatalf("render error = %v, want error %t", err, c.ExpectedError)
			}
			if c.ExpectedError {
				if out != "" {
					t.Fatal("refused render returned text")
				}
				return
			}
			if len(out) > c.MaxBytes {
				t.Fatalf("render used %d bytes above cap %d", len(out), c.MaxBytes)
			}
			assertRenderText(t, c.Name, out, c.Present, c.Absent)
			assertMarkdownStaysInert(t, c.Name, out)
			pinGolden(t, c.Case, c.Name, out)
		})
	}
	if len(required) != 0 {
		t.Fatalf("missing budget fixtures: %v", required)
	}
}
