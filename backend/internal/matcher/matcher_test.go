package matcher

import (
	"bytes"
	_ "embed"
	"io"
	"sort"
	"testing"
	"time"

	"github.com/peasant-labs/schema"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/matching.yaml
var matchingYAML []byte

//go:embed testdata/authorization.yaml
var authorizationYAML []byte

//go:embed testdata/commands.yaml
var commandsYAML []byte

// requiredMatchCaseNames is the name manifest for testdata/matching.yaml. Every
// case exists because it pins one arm of the acceptance policy: what accepts,
// what stays unresolved, what is not even a candidate, and what is retained
// rather than rewritten. Asserted as exact membership, never a count.
var requiredMatchCaseNames = []string{
	"abbreviated-sha-that-names-one-commit-accepts",
	"accepted-transcripts-order-by-session-start",
	"ambiguous-abbreviated-sha-names-no-commit",
	"branch-only-evidence-stays-unresolved",
	"fork-pull-request-matches-its-head-repository",
	"historical-commit-on-another-ref-is-not-coverage",
	"incomplete-commit-set-resolves-only-exact-full-sha",
	"no-recorded-commits-branch-match-unresolved",
	"one-commit-recorded-twice-is-one-anchor",
	"one-unresolved-commit-does-not-hold-back-an-accepted-anchor",
	"over-attributed-legacy-sha-is-retained-not-accepted",
	"project-path-only-transcript-is-not-a-candidate",
	"reused-branch-name-does-not-prove-relevance",
	"same-repository-pull-request-cannot-match-a-stray-head-repo",
	"sha-only-evidence-accepts",
	"sha-shorter-than-a-resolving-prefix-unresolved",
	"still-existing-branch-after-rebase-unresolved",
	"two-anchors-are-both-kept",
	"unknown-session-start-orders-last",
	"unrelated-repository-is-not-even-a-candidate",
}

// requiredAuthorizationCaseNames is the name manifest for
// testdata/authorization.yaml: one case per row of the actor table, the two
// unresolved-sender arms, and the fail-closed arms.
var requiredAuthorizationCaseNames = []string{
	"author-attach-proceeds",
	"author-detach-proceeds",
	"collaborator-attach-records-a-request",
	"contributor-attach-is-ignored",
	"first-time-contributor-attach-is-ignored",
	"lowercase-association-still-stands",
	"member-attach-records-a-request",
	"member-detach-is-ignored",
	"owner-attach-records-a-request",
	"unknown-command-from-the-author-is-ignored",
	"unknown-sender-id-is-not-the-author",
	"unresolved-sender-is-ignored-even-when-the-id-is-the-author",
	"zero-sender-id-owner-attach-is-ignored",
}

// requiredCommandCaseNames is the name manifest for testdata/commands.yaml.
var requiredCommandCaseNames = []string{
	"bare-command-word-is-not-a-command",
	"case-insensitive-command",
	"empty-body-is-not-a-command",
	"leading-whitespace-and-trailing-sentence",
	"mention-in-a-sentence-is-not-a-command",
	"plain-attach",
	"plain-detach",
	"unknown-subcommand-is-not-a-command",
}

type commitFixture struct {
	SHA          string    `yaml:"sha"`
	AuthoredAt   time.Time `yaml:"authored_at"`
	Additions    *int      `yaml:"additions"`
	Deletions    *int      `yaml:"deletions"`
	FilesChanged *int      `yaml:"files_changed"`
}

type pullRequestFixture struct {
	BaseRepo          string   `yaml:"base_repo"`
	HeadRepo          string   `yaml:"head_repo"`
	HeadRef           string   `yaml:"head_ref"`
	IsFork            bool     `yaml:"is_fork"`
	CommitSetComplete bool     `yaml:"commit_set_complete"`
	Commits           []string `yaml:"commits"`
}

type candidateFixture struct {
	Transcript   string          `yaml:"transcript"`
	ProjectName  string          `yaml:"project_name"`
	GitRemote    string          `yaml:"git_remote"`
	GitBranch    string          `yaml:"git_branch"`
	SessionStart time.Time       `yaml:"session_start"`
	Commits      []commitFixture `yaml:"commits"`
}

type anchorDetailFixture struct {
	SHA          string    `yaml:"sha"`
	AuthoredAt   time.Time `yaml:"authored_at"`
	Additions    *int      `yaml:"additions"`
	Deletions    *int      `yaml:"deletions"`
	FilesChanged *int      `yaml:"files_changed"`
}

type acceptedFixture struct {
	Transcript    string                `yaml:"transcript"`
	Anchors       []string              `yaml:"anchors"`
	AnchorDetails []anchorDetailFixture `yaml:"anchor_details"`
	Unresolved    map[string]string     `yaml:"unresolved"`
}

type unresolvedFixture struct {
	Transcript    string            `yaml:"transcript"`
	BranchMatched bool              `yaml:"branch_matched"`
	Reasons       map[string]string `yaml:"reasons"`
}

type expectedResultFixture struct {
	Accepted   []acceptedFixture   `yaml:"accepted"`
	Unresolved []unresolvedFixture `yaml:"unresolved"`
	Excluded   []string            `yaml:"excluded"`
}

type matchCase struct {
	Name        string                `yaml:"name"`
	PullRequest pullRequestFixture    `yaml:"pull_request"`
	Candidates  []candidateFixture    `yaml:"candidates"`
	Expect      expectedResultFixture `yaml:"expect"`
}

type matchFixture struct {
	Cases []matchCase `yaml:"cases"`
}

type actorFixture struct {
	SenderResolved bool   `yaml:"sender_resolved"`
	SenderGitHubID int64  `yaml:"sender_github_id"`
	AuthorGitHubID int64  `yaml:"author_github_id"`
	Association    string `yaml:"association"`
}

type authorizationCase struct {
	Name    string       `yaml:"name"`
	Actor   actorFixture `yaml:"actor"`
	Command string       `yaml:"command"`
	Expect  string       `yaml:"expect"`
}

type authorizationFixture struct {
	Cases []authorizationCase `yaml:"cases"`
}

type commandCase struct {
	Name   string `yaml:"name"`
	Body   string `yaml:"body"`
	Expect string `yaml:"expect"`
}

type commandFixture struct {
	Cases []commandCase `yaml:"cases"`
}

// decodeStrict decodes exactly one document with unknown fields rejected, which
// is what makes a typo in a fixture a failure rather than a silently ignored key.
func decodeStrict(t *testing.T, name string, data []byte, out any) {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(out); err != nil {
		t.Fatalf("decode strict %s fixture: %v", name, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("%s fixture must contain exactly one YAML document: %v", name, err)
	}
}

// assertExactCaseNames holds a fixture to exact membership against its manifest,
// in both directions: a missing name is a boundary that stopped being covered,
// and an undeclared name is a case nothing protects.
func assertExactCaseNames(t *testing.T, fixture string, present map[string]bool, required []string) {
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
		t.Fatalf("testdata/%s.yaml no longer carries %v, which its manifest declares in matcher_test.go: "+
			"each case pins an arm of the policy. Restore the row under its exact name rather than deleting "+
			"the name from the manifest.", fixture, missing)
	}
	if len(undeclared) > 0 {
		t.Fatalf("testdata/%s.yaml carries %v, which its manifest in matcher_test.go does not declare: an "+
			"undeclared case is unprotected, so add each new name to the manifest in the same change.", fixture, undeclared)
	}
}

func loadMatchCases(t *testing.T) []matchCase {
	t.Helper()
	var fixture matchFixture
	decodeStrict(t, "matching", matchingYAML, &fixture)
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" {
			t.Fatal("matching fixture has a case with an empty name")
		}
		if seen[c.Name] {
			t.Fatalf("matching fixture repeats case name %q", c.Name)
		}
		seen[c.Name] = true
	}
	assertExactCaseNames(t, "matching", seen, requiredMatchCaseNames)
	return fixture.Cases
}

func loadAuthorizationCases(t *testing.T) []authorizationCase {
	t.Helper()
	var fixture authorizationFixture
	decodeStrict(t, "authorization", authorizationYAML, &fixture)
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" {
			t.Fatal("authorization fixture has a case with an empty name")
		}
		if seen[c.Name] {
			t.Fatalf("authorization fixture repeats case name %q", c.Name)
		}
		seen[c.Name] = true
	}
	assertExactCaseNames(t, "authorization", seen, requiredAuthorizationCaseNames)
	return fixture.Cases
}

func loadCommandCases(t *testing.T) []commandCase {
	t.Helper()
	var fixture commandFixture
	decodeStrict(t, "commands", commandsYAML, &fixture)
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" {
			t.Fatal("commands fixture has a case with an empty name")
		}
		if seen[c.Name] {
			t.Fatalf("commands fixture repeats case name %q", c.Name)
		}
		seen[c.Name] = true
	}
	assertExactCaseNames(t, "commands", seen, requiredCommandCaseNames)
	return fixture.Cases
}

func toPullRequest(f pullRequestFixture) PullRequest {
	pr := PullRequest{
		BaseRepo:          f.BaseRepo,
		HeadRepo:          f.HeadRepo,
		HeadRef:           f.HeadRef,
		IsFork:            f.IsFork,
		CommitSetComplete: f.CommitSetComplete,
	}
	for _, sha := range f.Commits {
		pr.Commits = append(pr.Commits, PullCommit{SHA: sha})
	}
	return pr
}

func toTranscripts(fixtures []candidateFixture) []Transcript {
	var out []Transcript
	for _, f := range fixtures {
		candidate := Transcript{
			ID:           schema.TranscriptID(f.Transcript),
			ProjectName:  f.ProjectName,
			GitRemote:    f.GitRemote,
			GitBranch:    f.GitBranch,
			SessionStart: f.SessionStart,
		}
		for _, c := range f.Commits {
			candidate.Commits = append(candidate.Commits, RecordedCommit{
				SHA:          c.SHA,
				AuthoredAt:   c.AuthoredAt,
				Additions:    c.Additions,
				Deletions:    c.Deletions,
				FilesChanged: c.FilesChanged,
			})
		}
		out = append(out, candidate)
	}
	return out
}

func TestMatch(t *testing.T) {
	for _, tc := range loadMatchCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			result := Match(toPullRequest(tc.PullRequest), toTranscripts(tc.Candidates))

			acceptedByID := map[string]AcceptedTranscript{}
			var acceptedOrder []string
			for _, a := range result.Accepted {
				acceptedByID[string(a.TranscriptID)] = a
				acceptedOrder = append(acceptedOrder, string(a.TranscriptID))
			}
			unresolvedByID := map[string]UnresolvedCandidate{}
			var unresolvedOrder []string
			for _, u := range result.Unresolved {
				unresolvedByID[string(u.TranscriptID)] = u
				unresolvedOrder = append(unresolvedOrder, string(u.TranscriptID))
			}

			var wantAccepted []string
			for _, a := range tc.Expect.Accepted {
				wantAccepted = append(wantAccepted, a.Transcript)
			}
			assertSameStrings(t, "accepted transcripts, in session-start order", acceptedOrder, wantAccepted)

			var wantUnresolved []string
			for _, u := range tc.Expect.Unresolved {
				wantUnresolved = append(wantUnresolved, u.Transcript)
			}
			assertSameStrings(t, "unresolved candidates", unresolvedOrder, wantUnresolved)

			for _, want := range tc.Expect.Accepted {
				got, ok := acceptedByID[want.Transcript]
				if !ok {
					t.Fatalf("expected %q to be accepted, got neither accepted nor (in the fixture) unresolved", want.Transcript)
				}
				assertSameStrings(t, "anchors for "+want.Transcript, anchorSHAs(got.Anchors), want.Anchors)
				assertAnchorDetails(t, want.Transcript, got.Anchors, want.AnchorDetails)
				assertSameReasons(t, "unresolved commits for accepted "+want.Transcript, got.UnresolvedCommits, want.Unresolved)
			}
			for _, want := range tc.Expect.Unresolved {
				got, ok := unresolvedByID[want.Transcript]
				if !ok {
					t.Fatalf("expected %q to be an unresolved candidate", want.Transcript)
				}
				if got.BranchMatched != want.BranchMatched {
					t.Errorf("branch_matched for %q = %v, want %v", want.Transcript, got.BranchMatched, want.BranchMatched)
				}
				assertSameReasons(t, "reasons for "+want.Transcript, got.UnresolvedCommits, want.Reasons)
			}
			for _, id := range tc.Expect.Excluded {
				if _, ok := acceptedByID[id]; ok {
					t.Errorf("%q must not be a candidate, but it was accepted", id)
				}
				if _, ok := unresolvedByID[id]; ok {
					t.Errorf("%q must not be a candidate, but it was reported unresolved", id)
				}
			}

			// Nothing may vanish: every candidate the fixture declares is either
			// accepted, unresolved, or explicitly excluded.
			if accounted := len(result.Accepted) + len(result.Unresolved) + len(tc.Expect.Excluded); accounted != len(tc.Candidates) {
				t.Errorf("candidates accounted for = %d (accepted %d + unresolved %d + excluded %d), want %d",
					accounted, len(result.Accepted), len(result.Unresolved), len(tc.Expect.Excluded), len(tc.Candidates))
			}
		})
	}
}

func TestAuthorize(t *testing.T) {
	for _, tc := range loadAuthorizationCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			actor := Actor{
				SenderResolved:            tc.Actor.SenderResolved,
				SenderGitHubID:            tc.Actor.SenderGitHubID,
				PullRequestAuthorGitHubID: tc.Actor.AuthorGitHubID,
				AuthorAssociation:         tc.Actor.Association,
			}
			got := Authorize(actor, Command(tc.Command))
			if string(got) != tc.Expect {
				t.Errorf("Authorize(%+v, %q) = %q, want %q", actor, tc.Command, got, tc.Expect)
			}
		})
	}
}

func TestParseCommand(t *testing.T) {
	for _, tc := range loadCommandCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			command, ok := ParseCommand(tc.Body)
			if tc.Expect == "none" {
				if ok {
					t.Fatalf("ParseCommand(%q) = %q, true; want no command", tc.Body, command)
				}
				return
			}
			if !ok {
				t.Fatalf("ParseCommand(%q) found no command, want %q", tc.Body, tc.Expect)
			}
			if string(command) != tc.Expect {
				t.Errorf("ParseCommand(%q) = %q, want %q", tc.Body, command, tc.Expect)
			}
		})
	}
}

// anchorSHAs lists the anchors in the order the matcher produced them, so the
// fixture also pins anchor order rather than just membership.
func anchorSHAs(anchors []Anchor) []string {
	var out []string
	for _, a := range anchors {
		out = append(out, a.CommitSHA)
	}
	return out
}

// assertAnchorDetails checks an accepted transcript's anchor fields when the
// fixture describes them, so time and statistics are pinned and not only SHAs.
func assertAnchorDetails(t *testing.T, transcript string, anchors []Anchor, want []anchorDetailFixture) {
	t.Helper()
	if len(want) == 0 {
		return
	}
	bySHA := map[string]Anchor{}
	for _, a := range anchors {
		bySHA[a.CommitSHA] = a
	}
	if len(bySHA) != len(want) {
		t.Fatalf("anchors for %s = %d, want the %d the fixture describes", transcript, len(bySHA), len(want))
	}
	for _, w := range want {
		got, ok := bySHA[w.SHA]
		if !ok {
			t.Fatalf("no anchor for %s on %s", w.SHA, transcript)
		}
		if !w.AuthoredAt.IsZero() && !got.AuthoredAt.Equal(w.AuthoredAt) {
			t.Errorf("anchor %s authored_at = %v, want %v", w.SHA, got.AuthoredAt, w.AuthoredAt)
		}
		assertIntPointer(t, "additions", got.Additions, w.Additions)
		assertIntPointer(t, "deletions", got.Deletions, w.Deletions)
		assertIntPointer(t, "files_changed", got.FilesChanged, w.FilesChanged)
	}
}

func assertIntPointer(t *testing.T, what string, got, want *int) {
	t.Helper()
	switch {
	case want == nil && got != nil:
		t.Errorf("%s = %d, want absent", what, *got)
	case want != nil && got == nil:
		t.Errorf("%s absent, want %d", what, *want)
	case want != nil && got != nil && *got != *want:
		t.Errorf("%s = %d, want %d", what, *got, *want)
	}
}

func assertSameStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}
}

func assertSameReasons(t *testing.T, what string, got []UnresolvedCommit, want map[string]string) {
	t.Helper()
	gotReasons := map[string]string{}
	for _, u := range got {
		gotReasons[u.RecordedSHA] = string(u.Reason)
	}
	if len(gotReasons) != len(want) {
		t.Fatalf("%s = %v, want %v", what, gotReasons, want)
	}
	for recorded, reason := range want {
		if gotReasons[recorded] != reason {
			t.Fatalf("%s = %v, want %v", what, gotReasons, want)
		}
	}
}
