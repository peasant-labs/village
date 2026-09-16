package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/digest"
	"github.com/peasant-labs/village/backend/internal/github"
	"github.com/peasant-labs/village/backend/internal/matcher"
	"github.com/peasant-labs/village/backend/internal/promptattach"
	"github.com/peasant-labs/village/backend/internal/reponame"
)

// attachmentRedactionLevel is the level Village reports for an attached
// transcript. Standard is the only level the offer policy allows today, and the
// redact module owns that menu, so the value comes from there rather than from a
// literal here. When more levels are offered, the source becomes the level
// actually applied to the stored content; this constant is the one place that
// change lands, and nothing else in the lifecycle assumes a level.
const attachmentRedactionLevel = string(redact.Standard)

// attachmentHookTimeout bounds the post-publish completion work, which runs
// inside the publish request but must not be tied to the client's connection.
const attachmentHookTimeout = 30 * time.Second

// Sentinel failures the attachment lifecycle reports so a caller can map them to
// the right status: a collective that no longer exists or a repository that is
// no longer linked cannot be acted on (the attachment is unbound), and a GitHub
// failure is retryable rather than a client error.
var (
	errAttachmentUnbound           = errors.New("this attachment's collective is gone or its repository is no longer linked, so nothing can be posted for it")
	errAttachmentGitHubUnavailable = errors.New("the GitHub App client is not configured, so the pull request cannot be read or updated")
	// errAttachmentGitHub wraps any failure of a GitHub call so the routes can
	// answer 502 and leave the attachment untouched for a retry.
	errAttachmentGitHub = errors.New("a GitHub call for this attachment failed")
)

// attachmentRepository is the repository context an attachment's effects need,
// resolved through the collective that linked the repository rather than frozen
// on the attachment row: the installation and the repository's privacy are
// refreshed by every re-link, so a reinstall is picked up here instead of
// failing later.
type attachmentRepository struct {
	groupID        pgtype.UUID
	installationID int64
	isPrivate      bool
	checkMode      promptattach.CheckMode
	postCheck      bool
}

// resolveAttachmentRepository reads the linking collective's current repository
// link and check settings for one attachment. It fails closed: an attachment
// whose collective was deleted, or whose repository is no longer linked, cannot
// be posted for, and the caller must not guess an installation.
func (h *Handler) resolveAttachmentRepository(ctx context.Context, q Querier, attachment sqlc.PullRequestAttachment) (attachmentRepository, error) {
	if !attachment.GroupID.Valid {
		return attachmentRepository{}, errAttachmentUnbound
	}
	repo, err := q.GetCollectiveRepository(ctx, sqlc.GetCollectiveRepositoryParams{
		GroupID: attachment.GroupID,
		Lower:   strings.ToLower(attachment.RepoOwner),
		Lower_2: strings.ToLower(attachment.RepoName),
	})
	if err != nil {
		return attachmentRepository{}, fmt.Errorf("%w: %v", errAttachmentUnbound, err)
	}
	group, err := q.GetGroupByID(ctx, attachment.GroupID)
	if err != nil {
		return attachmentRepository{}, fmt.Errorf("%w: %v", errAttachmentUnbound, err)
	}
	mode := promptattach.CheckMode(group.PromptsCheckMode)
	if !mode.Valid() {
		return attachmentRepository{}, fmt.Errorf("the collective's prompts check mode %q is not one of %s, so no check conclusion can be chosen",
			group.PromptsCheckMode, promptattach.CheckModeMenu())
	}
	return attachmentRepository{
		groupID:        attachment.GroupID,
		installationID: repo.InstallationID,
		isPrivate:      repo.IsPrivate,
		checkMode:      mode,
		postCheck:      group.PostPromptsCheck,
	}, nil
}

// matchAttachmentCandidates applies #109's acceptance policy to one attachment:
// it reads the pull request's current commits and the author's own candidate
// transcripts, and returns the matcher's one answer. Repository equality only
// narrows the search here; nothing is accepted without a recorded commit
// resolving into the pull request.
//
// The accepted result is the ONLY input to completion, refresh, and initial
// attachment, so no caller can re-derive a weaker rule from a repository, a
// branch name, or a stored SHA.
func (h *Handler) matchAttachmentCandidates(ctx context.Context, attachment sqlc.PullRequestAttachment, repo attachmentRepository) (matcher.Result, []string, error) {
	if h.gh == nil {
		return matcher.Result{}, nil, errAttachmentGitHubUnavailable
	}

	listed, err := h.gh.ListPullRequestCommits(ctx, repo.installationID, attachment.RepoOwner, attachment.RepoName, int(attachment.Number), github.ListCommitsOptions{})
	if err != nil {
		return matcher.Result{}, nil, fmt.Errorf("%w: listing the pull request's commits: %v", errAttachmentGitHub, err)
	}

	candidates, err := h.loadAttachmentCandidates(ctx, attachment.AuthorID)
	if err != nil {
		return matcher.Result{}, nil, err
	}

	commits := make([]matcher.PullCommit, 0, len(listed.Commits))
	commitSet := make([]string, 0, len(listed.Commits))
	for _, commit := range listed.Commits {
		commits = append(commits, matcher.PullCommit{SHA: commit.SHA})
		commitSet = append(commitSet, commit.SHA)
	}

	baseRepo := reponame.NormalizeRemote(attachment.BaseRemote)
	headRepo := reponame.NormalizeRemote(attachment.HeadRemote)
	pullRequest := matcher.PullRequest{
		BaseRepo: baseRepo,
		HeadRepo: headRepo,
		IsFork:   headRepo != "" && headRepo != baseRepo,
		// Fail-closed on the endpoint's completeness: an incomplete walk may
		// anchor an exact full SHA but must never resolve an abbreviation or call
		// a commit absent.
		CommitSetComplete: listed.Complete,
		Commits:           commits,
	}

	return matcher.Match(pullRequest, candidates), commitSet, nil
}

// loadAttachmentCandidates reads the author's own transcripts that carry a
// stored remote, each with its recorded commits, in the shape the matcher takes.
// It reads through the same query the matcher's pool boundary uses, so the
// candidate rule (owner, stored remote) is not re-stated here.
func (h *Handler) loadAttachmentCandidates(ctx context.Context, authorID pgtype.UUID) ([]matcher.Transcript, error) {
	rows, err := h.queries.ListOwnerTranscriptsForMatching(ctx, authorID)
	if err != nil {
		return nil, fmt.Errorf("could not read the author's transcripts for matching: %w", err)
	}

	candidates := make([]matcher.Transcript, 0, len(rows))
	for _, row := range rows {
		commits, err := h.queries.ListTranscriptCommits(ctx, row.ID)
		if err != nil {
			return nil, fmt.Errorf("could not read the recorded commits for one candidate transcript: %w", err)
		}
		candidate := matcher.Transcript{
			ID:           schema.TranscriptID(uuidFromPg(row.ID).String()),
			GitRemote:    row.GitRemote.String,
			GitBranch:    row.GitBranch.String,
			SessionStart: row.SessionStart.Time,
			Commits:      make([]matcher.RecordedCommit, 0, len(commits)),
		}
		for _, commit := range commits {
			candidate.Commits = append(candidate.Commits, matcher.RecordedCommit{
				SHA:        commit.Sha,
				AuthoredAt: commit.AuthoredAt.Time,
				Additions:  intPointerFromPg(commit.Additions),
				Deletions:  intPointerFromPg(commit.Deletions),
			})
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

// buildAttachmentDigest projects an accepted match into the reviewer-facing
// digest. It reads each accepted transcript's turns through Village's single
// decrypting read path, and takes its anchor time and change counts from the
// transcript's own recorded commit, never from the pull request side.
func (h *Handler) buildAttachmentDigest(ctx context.Context, transcriptIDs []schema.TranscriptID, commitSet []string, match matcher.Result) (schema.PromptDigest, error) {
	sessions := make([]digest.Session, 0, len(transcriptIDs))
	var commits []digest.CommitMatch

	anchorsByTranscript := map[schema.TranscriptID][]matcher.Anchor{}
	for _, accepted := range match.Accepted {
		anchorsByTranscript[accepted.TranscriptID] = accepted.Anchors
	}

	for _, accepted := range transcriptIDs {
		transcriptID, err := uuid.Parse(string(accepted))
		if err != nil {
			return schema.PromptDigest{}, fmt.Errorf("an accepted transcript id was not a uuid: %w", err)
		}
		row, err := h.queries.GetTranscriptByID(ctx, pgtype.UUID{Bytes: transcriptID, Valid: true})
		if err != nil {
			return schema.PromptDigest{}, fmt.Errorf("could not read an accepted transcript for the digest: %w", err)
		}

		read, err := h.readEncryptedTranscript(ctx, row, "", func(candidate sqlc.Transcript) bool {
			return candidate.ID == row.ID
		})
		if err != nil {
			return schema.PromptDigest{}, fmt.Errorf("could not read an accepted transcript's turns for the digest: %w", err)
		}
		detail, err := decodePublicationDetail(read.Plaintext)
		if err != nil {
			return schema.PromptDigest{}, fmt.Errorf("could not decode an accepted transcript's turns for the digest: %w", err)
		}
		if detail == nil {
			return schema.PromptDigest{}, errors.New("an accepted transcript's stored content was not a decodable publication, so no digest could be built")
		}

		turns := make([]digest.Turn, 0, len(detail.Turns))
		for _, turn := range detail.Turns {
			turns = append(turns, digest.Turn{
				Index:     turn.Index,
				Role:      turn.Role,
				Content:   turn.Content,
				Timestamp: turn.Timestamp,
				Command:   turn.Command,
			})
		}
		sessions = append(sessions, digest.Session{
			TranscriptID:   accepted,
			Harness:        schema.Harness(row.ModelProvider),
			RedactionLevel: attachmentRedactionLevel,
			SessionStart:   row.SessionStart.Time,
			Turns:          turns,
		})

		for _, anchor := range anchorsByTranscript[accepted] {
			commits = append(commits, digest.CommitMatch{
				TranscriptID: accepted,
				CommitSHA:    anchor.CommitSHA,
				AuthoredAt:   anchor.AuthoredAt,
				Additions:    anchor.Additions,
				Deletions:    anchor.Deletions,
				FilesChanged: anchor.FilesChanged,
			})
		}
	}

	return digest.Build(digest.Input{
		VillageURL: strings.TrimRight(h.cfg.FrontendURL, "/"),
		CommitSet:  commitSet,
		Sessions:   sessions,
		Commits:    commits,
	})
}

// intPointerFromPg converts a nullable integer column to the optional count the
// matcher and digest expect (all-or-nothing per commit).
func intPointerFromPg(value pgtype.Int4) *int {
	if !value.Valid {
		return nil
	}
	converted := int(value.Int32)
	return &converted
}
