// Package digest projects the accepted transcripts and session-to-commit
// relationships behind a pull request into the reviewer-facing
// schema.PromptDigest, and renders it to GitHub Markdown at each tier's budget.
//
// It consumes an already-accepted input boundary: the matcher decides which
// transcripts and which session-to-commit relationships are accepted, and this
// package only orders, validates, and renders them. It never re-derives
// acceptance from branch names, repository equality, or raw SHA rows, so a
// weaker rule cannot reappear here.
//
// The caller supplies decoded turns, read through Village's single decrypting
// path. The package never touches storage and never writes turn text to a log.
package digest

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/peasant-labs/schema"
)

// Turn is one decoded transcript turn.
type Turn struct {
	Index     int
	Role      schema.Role
	Content   string
	Timestamp time.Time
	// Command is the structured slash-command invocation when the turn invoked
	// a skill or user command; nil for a plain prompt or an agent turn.
	Command *schema.CommandInvocation
}

// Session is one accepted attached transcript.
type Session struct {
	TranscriptID   schema.TranscriptID
	Harness        schema.Harness
	RedactionLevel string
	SessionStart   time.Time
	Turns          []Turn
}

// CommitMatch is one session-to-PR-commit relationship accepted by the matcher.
// Additions, Deletions, and FilesChanged are the provider's change counts and
// are either all present or all absent.
type CommitMatch struct {
	TranscriptID schema.TranscriptID
	CommitSHA    string
	AuthoredAt   time.Time
	Additions    *int
	Deletions    *int
	FilesChanged *int
}

// Input is the accepted evidence the builder projects.
type Input struct {
	VillageURL string
	// CommitSet is the pull request's current commit set. An accepted commit
	// anchor must intersect it; a stored SHA that no longer appears here does
	// not become coverage.
	CommitSet []string
	Sessions  []Session
	Commits   []CommitMatch
}

var commitSHAPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// Build orders the accepted evidence into a validated schema.PromptDigest. It
// calls PromptDigest.Validate before returning, so a builder defect fails loudly
// instead of shipping an inconsistent chain.
func Build(in Input) (schema.PromptDigest, error) {
	sessions := append([]Session(nil), in.Sessions...)
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].SessionStart.Before(sessions[j].SessionStart)
	})

	commitSet := normalizeCommitSet(in.CommitSet)
	anchors := acceptedAnchors(in.Commits, commitSet)

	// Distinct anchors, keyed by the current PR commit they resolve to, keeping
	// the earliest authored time. One anchor per commit.
	type anchor struct {
		sha          string
		transcriptID schema.TranscriptID
		authoredAt   time.Time
		additions    *int
		deletions    *int
		files        *int
	}
	bySHA := map[string]anchor{}
	var order []string
	for _, a := range anchors {
		existing, seen := bySHA[a.resolved]
		if seen {
			if a.authoredAt.Before(existing.authoredAt) {
				bySHA[a.resolved] = anchor{sha: a.resolved, transcriptID: a.transcriptID, authoredAt: a.authoredAt, additions: a.Additions, deletions: a.Deletions, files: a.FilesChanged}
			}
			continue
		}
		bySHA[a.resolved] = anchor{sha: a.resolved, transcriptID: a.transcriptID, authoredAt: a.authoredAt, additions: a.Additions, deletions: a.Deletions, files: a.FilesChanged}
		order = append(order, a.resolved)
	}

	var items []schema.PromptDigestItem
	skillCounts := map[string]int{}
	for si, session := range sessions {
		promptCount := 0
		for _, turn := range session.Turns {
			if isPrompt(turn) {
				promptCount++
			}
		}
		commitCount := 0
		for _, sha := range order {
			for _, a := range anchors {
				if a.transcriptID == session.TranscriptID && a.resolved == sha {
					commitCount++
					break
				}
			}
		}
		items = append(items, schema.PromptDigestItem{
			Kind:         schema.DigestItemSession,
			TranscriptID: session.TranscriptID,
			Timestamp:    session.SessionStart,
			Text:         fmt.Sprintf("session %d", si+1),
			PromptCount:  intPtr(promptCount),
			CommitCount:  intPtr(commitCount),
		})
		for _, turn := range session.Turns {
			switch {
			case turn.Command != nil:
				text := turn.Command.Name
				skillCounts[text]++
				items = append(items, schema.PromptDigestItem{
					Kind:         schema.DigestItemSkill,
					TranscriptID: session.TranscriptID,
					Timestamp:    turn.Timestamp,
					Text:         text,
					Args:         turn.Command.Args,
					TurnIndex:    intPtr(turn.Index),
				})
			case isPrompt(turn):
				items = append(items, schema.PromptDigestItem{
					Kind:         schema.DigestItemPrompt,
					TranscriptID: session.TranscriptID,
					Timestamp:    turn.Timestamp,
					Text:         strings.TrimSpace(turn.Content),
					TurnIndex:    intPtr(turn.Index),
				})
			}
		}
	}

	for _, sha := range order {
		a := bySHA[sha]
		item := schema.PromptDigestItem{
			Kind:         schema.DigestItemCommit,
			TranscriptID: a.transcriptID,
			Timestamp:    a.authoredAt,
			Text:         sha,
			CommitSHA:    sha,
		}
		if a.additions != nil && a.deletions != nil && a.files != nil {
			item.Additions = a.additions
			item.Deletions = a.deletions
			item.FilesChanged = a.files
		}
		items = append(items, item)
	}

	// Chronological order, stable so equal timestamps keep insertion order
	// (a session boundary precedes its own turns at the same instant, and an
	// anchor lands after prompts that share its instant).
	sort.SliceStable(items, func(i, j int) bool { return items[i].Timestamp.Before(items[j].Timestamp) })

	prompts := 0
	for i := range items {
		if items[i].Kind == schema.DigestItemPrompt {
			prompts++
			items[i].Ordinal = intPtr(prompts)
		}
	}

	skills := make([]schema.PromptDigestSkill, 0, len(skillCounts))
	for name, count := range skillCounts {
		skills = append(skills, schema.PromptDigestSkill{Name: name, InvocationCount: count})
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })

	header := schema.PromptDigestHeader{
		SessionCount:   len(sessions),
		PromptCount:    prompts,
		CommitsCovered: len(order),
		CommitsTotal:   len(commitSet),
		VillageURL:     in.VillageURL,
	}
	if len(sessions) > 0 {
		header.Harness = sessions[0].Harness
		header.RedactionLevel = sessions[0].RedactionLevel
	}

	built := schema.PromptDigest{Header: header, Skills: skills, Items: items}
	if err := built.Validate(); err != nil {
		return schema.PromptDigest{}, fmt.Errorf("built prompt digest did not validate: %w", err)
	}
	return built, nil
}

func isPrompt(turn Turn) bool {
	return turn.Command == nil && turn.Role == schema.RoleUser && strings.TrimSpace(turn.Content) != ""
}

type rawAnchor struct {
	transcriptID schema.TranscriptID
	resolved     string
	authoredAt   time.Time
	Additions    *int
	Deletions    *int
	FilesChanged *int
}

// acceptedAnchors keeps only the matches whose commit is present in the PR's
// current commit set (prefix match, at least 7 hex characters). A stored SHA
// that no longer intersects the current set is dropped, not substituted.
func acceptedAnchors(matches []CommitMatch, commitSet map[string]string) []rawAnchor {
	var out []rawAnchor
	for _, m := range matches {
		sha := strings.ToLower(strings.TrimSpace(m.CommitSHA))
		if len(sha) < 7 {
			continue
		}
		resolved, ok := resolveCommit(sha, commitSet)
		if !ok {
			continue
		}
		out = append(out, rawAnchor{
			transcriptID: m.TranscriptID,
			resolved:     resolved,
			authoredAt:   m.AuthoredAt,
			Additions:    m.Additions,
			Deletions:    m.Deletions,
			FilesChanged: m.FilesChanged,
		})
	}
	return out
}

// resolveCommit maps an accepted (possibly abbreviated) SHA to the full current
// commit it prefixes. Exactly one candidate must match.
func resolveCommit(sha string, commitSet map[string]string) (string, bool) {
	if full, ok := commitSet[sha]; ok {
		return full, true
	}
	match := ""
	for candidate := range commitSet {
		if strings.HasPrefix(candidate, sha) {
			return candidate, true
		}
	}
	return match, false
}

// normalizeCommitSet lowercases, validates, and dedupes the PR commit set.
func normalizeCommitSet(set []string) map[string]string {
	out := map[string]string{}
	for _, sha := range set {
		lower := strings.ToLower(strings.TrimSpace(sha))
		if !commitSHAPattern.MatchString(lower) {
			continue
		}
		out[lower] = lower
	}
	return out
}

func intPtr(v int) *int { return &v }
