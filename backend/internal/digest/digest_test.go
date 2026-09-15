package digest

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/peasant-labs/schema"
)

func mustTID(t *testing.T, raw string) schema.TranscriptID {
	t.Helper()
	id, err := schema.NewTranscriptID(raw)
	if err != nil {
		t.Fatalf("transcript id %q: %v", raw, err)
	}
	return id
}

func at(s string) time.Time {
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return ts
}

// fixtureInput is a two-session chain: a short session and a longer one, a
// slash-command invocation, a redaction placeholder, one accepted commit, and
// one stored commit match that is NOT in the PR's current commit set.
func fixtureInput(t *testing.T) Input {
	t.Helper()
	tidA := mustTID(t, "7b1e4d2a-9c3f-4e8b-a1d6-2f5c8e9a0b13")
	tidB := mustTID(t, "8c2f5e3b-0d4a-4f9c-b2e7-3a6d9f0b1c24")
	cmd := schema.CommandInvocation{Name: "/superpowers:brainstorming"}
	add, del, files := 10, 2, 3
	return Input{
		VillageURL: "https://village.example/transcripts",
		CommitSet: []string{
			"a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
			"1111111111111111111111111111111111111111",
			"2222222222222222222222222222222222222222",
		},
		Sessions: []Session{
			{ // later session, listed first to prove ordering
				TranscriptID: tidB, Harness: "opencode", RedactionLevel: "standard",
				SessionStart: at("2026-09-06T15:00:00Z"),
				Turns: []Turn{
					{Index: 0, Role: schema.RoleUser, Content: "fork ask", Timestamp: at("2026-09-06T15:01:00Z")},
				},
			},
			{
				TranscriptID: tidA, Harness: "claude-code", RedactionLevel: "standard",
				SessionStart: at("2026-09-06T14:00:00Z"),
				Turns: []Turn{
					{Index: 0, Role: schema.RoleUser, Content: "first ask", Timestamp: at("2026-09-06T14:02:00Z")},
					{Index: 1, Role: schema.RoleUser, Content: "run the plan", Timestamp: at("2026-09-06T14:02:30Z"), Command: &cmd},
					{Index: 2, Role: schema.RoleAssistant, Content: "working on it", Timestamp: at("2026-09-06T14:03:00Z")},
					{Index: 3, Role: schema.RoleUser, Content: "second ask with <REDACTED>", Timestamp: at("2026-09-06T14:05:00Z")},
				},
			},
		},
		Commits: []CommitMatch{
			{TranscriptID: tidA, CommitSHA: "a1b2c3d", AuthoredAt: at("2026-09-06T14:04:00Z"), Additions: &add, Deletions: &del, FilesChanged: &files},
			{TranscriptID: tidA, CommitSHA: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", AuthoredAt: at("2026-09-06T14:06:00Z")},
		},
	}
}

func TestBuildOrdersAndCountsTheAcceptedChain(t *testing.T) {
	d, err := Build(fixtureInput(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if d.Header.SessionCount != 2 || d.Header.PromptCount != 3 {
		t.Fatalf("header sessions/prompts = %d/%d, want 2/3", d.Header.SessionCount, d.Header.PromptCount)
	}
	if d.Header.CommitsCovered != 1 || d.Header.CommitsTotal != 3 {
		t.Fatalf("header commits = %d/%d, want 1/3 (the out-of-set match must not count)", d.Header.CommitsCovered, d.Header.CommitsTotal)
	}
	if d.Header.Harness != "claude-code" {
		t.Fatalf("header harness = %q, want the first session's claude-code", d.Header.Harness)
	}

	kinds := map[schema.DigestItemKind]int{}
	for _, item := range d.Items {
		kinds[item.Kind]++
	}
	if kinds[schema.DigestItemSession] != 2 || kinds[schema.DigestItemPrompt] != 3 || kinds[schema.DigestItemSkill] != 1 || kinds[schema.DigestItemCommit] != 1 {
		t.Fatalf("item kinds = %v, want 2 session / 3 prompt / 1 skill / 1 commit", kinds)
	}

	// Ordinals run 1..N and the chain is chronological (Validate already proved
	// both; re-assert ordinals so a builder regression is legible).
	ordinal := 0
	var previous time.Time
	for i, item := range d.Items {
		if i > 0 && item.Timestamp.Before(previous) {
			t.Fatalf("items[%d] is out of order: %s before %s", i, item.Timestamp, previous)
		}
		previous = item.Timestamp
		if item.Kind == schema.DigestItemPrompt {
			ordinal++
			if item.Ordinal == nil || *item.Ordinal != ordinal {
				t.Fatalf("prompt %d ordinal = %v, want %d", ordinal, item.Ordinal, ordinal)
			}
		}
	}
	if ordinal != 3 {
		t.Fatalf("numbered %d prompts, want 3", ordinal)
	}

	// The single accepted commit is the in-set one; the out-of-set stored match
	// contributed no anchor.
	for _, item := range d.Items {
		if item.Kind == schema.DigestItemCommit && item.CommitSHA != "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0" {
			t.Fatalf("unexpected commit anchor %q", item.CommitSHA)
		}
	}

	if len(d.Skills) != 1 || d.Skills[0].Name != "/superpowers:brainstorming" || d.Skills[0].InvocationCount != 1 {
		t.Fatalf("skills header = %+v, want one /superpowers:brainstorming x1", d.Skills)
	}
}

func TestBuildPreservesRedactionPlaceholders(t *testing.T) {
	d, err := Build(fixtureInput(t))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range d.Items {
		if item.Kind == schema.DigestItemPrompt && strings.Contains(item.Text, "<REDACTED>") {
			found = true
		}
	}
	if !found {
		t.Fatal("the redaction placeholder did not survive into the prompt item")
	}
}

func TestBuildRejectsAnOutOfSetCommitAnchor(t *testing.T) {
	in := fixtureInput(t)
	// Remove the accepted commit from the PR's current set; the anchor must
	// disappear rather than being substituted or retained.
	in.CommitSet = in.CommitSet[1:]
	d, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if d.Header.CommitsCovered != 0 {
		t.Fatalf("commitsCovered = %d, want 0 when no accepted match intersects the current set", d.Header.CommitsCovered)
	}
	for _, item := range d.Items {
		if item.Kind == schema.DigestItemCommit {
			t.Fatalf("an anchor %q was emitted for a commit outside the current set", item.CommitSHA)
		}
	}
}

func TestRenderTiersAndBudgets(t *testing.T) {
	d, err := Build(fixtureInput(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, tier := range []Tier{CommentTier, CheckRunTier, VillageTier} {
		out, err := Render(d, tier)
		if err != nil {
			t.Fatalf("Render(%s): %v", tier.Name, err)
		}
		if tier.MaxBytes > 0 && len(out) > tier.MaxBytes {
			t.Fatalf("Render(%s) produced %d bytes, over the %d cap", tier.Name, len(out), tier.MaxBytes)
		}
		if !strings.Contains(out, "first ask") || !strings.Contains(out, "second ask with <REDACTED>") {
			t.Fatalf("Render(%s) omitted a prompt:\n%s", tier.Name, out)
		}
	}
}

func TestRenderCollapsesBeyondTheInlineBudget(t *testing.T) {
	tid := mustTID(t, "7b1e4d2a-9c3f-4e8b-a1d6-2f5c8e9a0b13")
	var turns []Turn
	for i := 0; i < 15; i++ {
		turns = append(turns, Turn{Index: i, Role: schema.RoleUser, Content: "prompt " + time.Duration(i).String(), Timestamp: at("2026-09-06T14:00:00Z").Add(time.Duration(i) * time.Minute)})
	}
	d, err := Build(Input{
		VillageURL: "https://village.example/transcripts",
		CommitSet:  nil,
		Sessions:   []Session{{TranscriptID: tid, Harness: "claude-code", RedactionLevel: "standard", SessionStart: at("2026-09-06T14:00:00Z"), Turns: turns}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render(d, CommentTier)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > CommentTier.MaxBytes {
		t.Fatalf("collapsed render is %d bytes, over the cap", len(out))
	}
	if !strings.Contains(out, "5 more prompts omitted") {
		t.Fatalf("expected 5 prompts to collapse beyond the 10-inline budget:\n%s", out)
	}
	if !strings.Contains(out, "https://village.example/transcripts") {
		t.Fatal("the collapse line does not link to Village")
	}
}

func TestRenderRefusesAnInvalidDigest(t *testing.T) {
	d, err := Build(fixtureInput(t))
	if err != nil {
		t.Fatal(err)
	}

	missingHeaderSkill := d
	missingHeaderSkill.Skills = nil
	if _, err := Render(missingHeaderSkill, VillageTier); err == nil {
		t.Fatal("rendering a digest whose skill item is missing from the header must fail")
	}

	outOfOrder := d
	outOfOrder.Items = append([]schema.PromptDigestItem(nil), d.Items...)
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

	d, err := Build(fixtureInput(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render(d, VillageTier); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"first ask", "second ask with <REDACTED>", "/superpowers:brainstorming"} {
		if strings.Contains(buf.String(), secret) {
			t.Fatalf("a log line leaked prompt text %q: %s", secret, buf.String())
		}
	}
}
