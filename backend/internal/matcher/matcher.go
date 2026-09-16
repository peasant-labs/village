// Package matcher decides which of a pull request author's transcripts belong
// to a pull request, and which of their recorded commits are anchors in it.
//
// It is deliberately its own package, sibling to digest and promptattach, so
// the acceptance rule exists in exactly one place. digest projects an accepted
// input and promptattach moves attachment state; neither may re-derive
// acceptance from a repository, a branch name, or a stored SHA row, and this
// package is what they both ask.
//
// The rule has two layers, and keeping them apart is the whole point:
//
//   - Discovery is cheap and permissive. A transcript is a candidate when the
//     author owns it and its stored remote names the pull request's repository.
//     That is only a reason to look, never a reason to attach.
//   - Acceptance is strict. A candidate is accepted only when at least one of
//     its recorded commits resolves, unambiguously, to a commit the pull
//     request currently contains. That intersection is the evidence. Because an
//     anchor is only as good as the commit set it resolved against, a set that
//     is not known to be complete may resolve exact full SHAs but never an
//     abbreviation, and may not call an absent commit absent.
//
// Branch names are reused, rewritten, and force-pushed, and a recorded
// session-to-commit association is heuristic, so neither is acceptance on its
// own. Everything that does not resolve stays a candidate: it is reported with
// the evidence that was found and why the commits did not resolve, never
// silently promoted and never discarded. A repository can also be rewritten
// such that a genuine historical commit is no longer reachable from the pull
// request's current commits; that commit is still reported as an observation
// and does not become coverage, and no successor commit is invented for it.
//
// The package is pure: it takes values in and returns values out, with no
// database, network, or clock, so every case the policy turns on is a fixture.
package matcher

import (
	"sort"
	"strings"
	"time"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/reponame"
)

// minSHALength is the shortest recorded SHA the matcher will try to resolve.
// Below it a prefix stops being evidence, because it would match many commits.
const minSHALength = 7

// RecordedCommit is one commit a transcript recorded, as stored in
// transcript_commits. The change counts are the provider's and are either all
// present or all absent.
type RecordedCommit struct {
	SHA          string
	AuthoredAt   time.Time
	Additions    *int
	Deletions    *int
	FilesChanged *int
}

// Transcript is one of the author's own transcripts, offered as a candidate.
// SessionStart is the zero time when the transcript recorded none.
type Transcript struct {
	ID           schema.TranscriptID
	ProjectName  string
	GitRemote    string
	GitBranch    string
	SessionStart time.Time
	Commits      []RecordedCommit
}

// PullCommit is one commit the pull request currently contains, from
// GET /repos/{owner}/{repo}/pulls/{number}/commits. Only the SHA is modelled:
// an anchor's time and statistics come from the transcript's own recorded
// commit, never from the pull request side.
type PullCommit struct {
	SHA string
}

// PullRequest is the pull request side of the comparison.
//
// CommitSetComplete says whether Commits is the pull request's COMPLETE commit
// list. It is fail-closed: the zero value means incomplete, and an incomplete
// set supports only exact full-SHA matches. Prefix resolution needs a complete
// set, because an abbreviation that matches one supplied commit may match
// another that was never fetched, and "not in the pull request" is unknowable
// when part of the pull request was not read. Callers must set this only on
// evidence that nothing was omitted — for example a paginated fetch that
// finished with no page left to follow and did not reach GitHub's endpoint cap.
//
// For a pull request from a fork, HeadRepo is the fork the commits came from;
// it is only consulted when IsFork is set, so a same-repository pull request
// cannot be matched by a stray HeadRepo value.
type PullRequest struct {
	BaseRepo          string
	HeadRepo          string
	HeadRef           string
	IsFork            bool
	CommitSetComplete bool
	Commits           []PullCommit
}

// Anchor is an accepted session-to-pull-request-commit relationship: the
// recorded commit resolved to a commit the pull request contains, so the
// transcript is attached to it.
type Anchor struct {
	TranscriptID schema.TranscriptID
	CommitSHA    string
	AuthoredAt   time.Time
	Additions    *int
	Deletions    *int
	FilesChanged *int
}

// AcceptedTranscript is a candidate the matcher accepted, with every anchor
// that accepted it. A transcript is accepted only when it has at least one.
//
// UnresolvedCommits carries the recorded commits that did not resolve even
// though another commit accepted the transcript. They are reported so an
// observation is never lost just because the transcript was accepted for a
// different commit.
type AcceptedTranscript struct {
	TranscriptID      schema.TranscriptID
	SessionStart      time.Time
	Anchors           []Anchor
	UnresolvedCommits []UnresolvedCommit
}

// UnresolvedReason says why a recorded commit did not resolve to a commit in
// the pull request.
type UnresolvedReason string

const (
	// ReasonTooShort means the recorded SHA is shorter than the shortest prefix
	// that can be evidence.
	ReasonTooShort UnresolvedReason = "too_short"
	// ReasonNotInPullRequest means the commit is not in the pull request's
	// current commits. The recorded observation is kept, not erased.
	ReasonNotInPullRequest UnresolvedReason = "not_in_pull_request"
	// ReasonAmbiguous means the recorded abbreviation prefixes more than one of
	// the pull request's commits, so it names no single commit.
	ReasonAmbiguous UnresolvedReason = "ambiguous"
	// ReasonIncomplete means the pull request's commit list is not known to be
	// complete, so the recorded commit was not resolved to anything: an
	// abbreviation cannot be trusted against a partial list, and a full SHA that
	// is absent proves nothing when part of the pull request was never read.
	ReasonIncomplete UnresolvedReason = "incomplete_commit_list"
)

// UnresolvedCommit is one recorded commit that did not resolve, with the
// reason. A pull request that was rebased or squashed leaves these behind
// rather than rewriting what the transcript recorded.
type UnresolvedCommit struct {
	RecordedSHA string
	Reason      UnresolvedReason
}

// UnresolvedCandidate is a repository-gated transcript that the matcher did not
// accept. It is reported rather than dropped: it is why the pull request page
// can offer a candidate for a person to attach deliberately, and why an
// over-attributed legacy SHA stays visible instead of being deleted.
type UnresolvedCandidate struct {
	TranscriptID      schema.TranscriptID
	BranchMatched     bool
	UnresolvedCommits []UnresolvedCommit
}

// Result is the matcher's one answer for one pull request. Accepted transcripts
// are ordered by session start; unresolved candidates keep discovery order.
type Result struct {
	Accepted   []AcceptedTranscript
	Unresolved []UnresolvedCandidate
}

// Match applies the acceptance policy to the candidates and returns the
// accepted transcripts and the unresolved candidates. A candidate whose remote
// does not name the pull request's repository is not returned at all: it was
// never a candidate, and reporting every unrelated transcript as "unresolved"
// would make the result useless.
func Match(pr PullRequest, candidates []Transcript) Result {
	commits := indexCommits(pr.Commits)

	var result Result
	for _, candidate := range candidates {
		if !namesPullRequestRepository(pr, candidate) {
			continue
		}

		anchors, unresolved := resolveRecordedCommits(candidate, pr.Commits, commits, pr.CommitSetComplete)

		if len(anchors) == 0 {
			result.Unresolved = append(result.Unresolved, UnresolvedCandidate{
				TranscriptID:      candidate.ID,
				BranchMatched:     branchMatches(pr, candidate),
				UnresolvedCommits: unresolved,
			})
			continue
		}
		result.Accepted = append(result.Accepted, AcceptedTranscript{
			TranscriptID:      candidate.ID,
			SessionStart:      candidate.SessionStart,
			Anchors:           anchors,
			UnresolvedCommits: unresolved,
		})
	}

	sortAcceptedBySessionStart(result.Accepted)
	return result
}

// namesPullRequestRepository reports whether the candidate's stored remote
// names the pull request's base repository, or its head repository when the
// pull request comes from a fork. The comparison is case-insensitive because
// GitHub repository names are, and it goes through reponame.NormalizeRemote so
// the publish path and the matcher cannot disagree about what a remote names.
//
// Only the remote is consulted. A transcript with no remote is not a candidate
// even when its local project path happens to name the repository: the project
// path is a display-name fallback, and the candidate query the caller runs
// requires a remote anyway, so a directory name must never widen the pool.
func namesPullRequestRepository(pr PullRequest, candidate Transcript) bool {
	name := reponame.NormalizeRemote(candidate.GitRemote)
	if name == "" {
		return false
	}
	if strings.EqualFold(name, pr.BaseRepo) {
		return true
	}
	return pr.IsFork && pr.HeadRepo != "" && strings.EqualFold(name, pr.HeadRepo)
}

// branchMatches reports whether the transcript recorded the pull request's head
// ref. It is evidence to report, never a reason to accept on its own.
func branchMatches(pr PullRequest, candidate Transcript) bool {
	return candidate.GitBranch != "" && pr.HeadRef != "" && candidate.GitBranch == pr.HeadRef
}

// resolveRecordedCommits resolves each recorded commit against the pull
// request's commits and returns the accepted anchors plus the recorded commits
// that did not resolve. Anchors are deduplicated by the commit they resolved
// to, keeping the earliest authored time, so recording both a full SHA and an
// abbreviation of one commit is one anchor.
func resolveRecordedCommits(candidate Transcript, commits []PullCommit, index map[string]PullCommit, complete bool) ([]Anchor, []UnresolvedCommit) {
	var anchors []Anchor
	var unresolved []UnresolvedCommit
	byResolved := map[string]int{}

	for _, recorded := range candidate.Commits {
		commit, reason, ok := resolveCommit(recorded.SHA, commits, index, complete)
		if !ok {
			unresolved = append(unresolved, UnresolvedCommit{RecordedSHA: recorded.SHA, Reason: reason})
			continue
		}
		anchor := Anchor{
			TranscriptID: candidate.ID,
			CommitSHA:    commit.SHA,
			AuthoredAt:   recorded.AuthoredAt,
			Additions:    recorded.Additions,
			Deletions:    recorded.Deletions,
			FilesChanged: recorded.FilesChanged,
		}
		if at, seen := byResolved[commit.SHA]; seen {
			if recorded.AuthoredAt.Before(anchors[at].AuthoredAt) {
				anchors[at].AuthoredAt = recorded.AuthoredAt
			}
			continue
		}
		byResolved[commit.SHA] = len(anchors)
		anchors = append(anchors, anchor)
	}

	return anchors, unresolved
}

// resolveCommit maps a recorded (possibly abbreviated) SHA to the one commit in
// the pull request it names.
//
// An exact match against a supplied commit is sound whatever the set: a commit
// the pull request returned IS a commit the pull request contains. Everything
// else needs a complete set. An abbreviation that prefixes exactly one supplied
// commit may prefix another that was never fetched, so on an incomplete set it
// is not resolved at all; and a recorded commit absent from a partial list
// proves nothing, so it is reported as incomplete rather than "not in the pull
// request". On a complete set, an abbreviation prefixing two commits names
// neither, and one absent from the list is genuinely not in the pull request.
func resolveCommit(recorded string, commits []PullCommit, index map[string]PullCommit, complete bool) (PullCommit, UnresolvedReason, bool) {
	sha := strings.ToLower(strings.TrimSpace(recorded))
	if len(sha) < minSHALength {
		return PullCommit{}, ReasonTooShort, false
	}
	if commit, ok := index[sha]; ok {
		return commit, "", true
	}
	if !complete {
		return PullCommit{}, ReasonIncomplete, false
	}

	found := -1
	for i, commit := range commits {
		if strings.HasPrefix(strings.ToLower(commit.SHA), sha) {
			if found >= 0 {
				return PullCommit{}, ReasonAmbiguous, false
			}
			found = i
		}
	}
	if found < 0 {
		return PullCommit{}, ReasonNotInPullRequest, false
	}
	return commits[found], "", true
}

// indexCommits keys the pull request's commits by their lowercased SHA, so a
// full recorded SHA is one lookup rather than a scan.
func indexCommits(commits []PullCommit) map[string]PullCommit {
	index := make(map[string]PullCommit, len(commits))
	for _, commit := range commits {
		index[strings.ToLower(strings.TrimSpace(commit.SHA))] = commit
	}
	return index
}

// sortAcceptedBySessionStart orders the accepted transcripts by session start,
// which is attachment order. A transcript with no recorded session start is
// ordered last, matching the candidate query's NULLS LAST, so the database and
// the matcher cannot disagree about attachment order. It is stable, so
// transcripts that tie keep discovery order rather than shuffling between runs.
func sortAcceptedBySessionStart(accepted []AcceptedTranscript) {
	sort.SliceStable(accepted, func(i, j int) bool {
		left, right := accepted[i].SessionStart, accepted[j].SessionStart
		if left.IsZero() != right.IsZero() {
			return !left.IsZero()
		}
		return left.Before(right)
	})
}
