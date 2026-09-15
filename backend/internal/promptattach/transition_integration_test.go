//go:build integration

package promptattach

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// TestTransitionEnforcesTheClosedTable drives every ordered pair from the
// fixture through the production transition function against real PostgreSQL.
// An allowed pair moves and stamps the target state's timestamp; a refused pair
// returns ErrTransitionNotAllowed and leaves the stored state untouched.
func TestTransitionEnforcesTheClosedTable(t *testing.T) {
	ctx := context.Background()
	pool := newScratchPool(t)
	q := sqlc.New(pool)
	owner := insertOwner(t, ctx, pool)

	for i, c := range loadTransitionCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			attachment := createAttachment(t, ctx, q, owner, i, c.From)

			updated, err := Transition(ctx, q, attachment.ID, c.To)

			if c.Allowed {
				if err != nil {
					t.Fatalf("allowed transition %s -> %s failed: %v", c.From, c.To, err)
				}
				if updated.State != string(c.To) {
					t.Fatalf("state after allowed transition %s -> %s = %q, want %q", c.From, c.To, updated.State, c.To)
				}
				if !timestampFor(updated, c.To).Valid {
					t.Fatalf("allowed transition %s -> %s did not stamp %s_at", c.From, c.To, c.To)
				}
				return
			}

			if !errors.Is(err, ErrTransitionNotAllowed) {
				t.Fatalf("refused transition %s -> %s error = %v, want ErrTransitionNotAllowed", c.From, c.To, err)
			}
			reloaded, getErr := q.GetPullRequestAttachment(ctx, attachment.ID)
			if getErr != nil {
				t.Fatalf("re-read after refused transition %s -> %s: %v", c.From, c.To, getErr)
			}
			if reloaded.State != string(c.From) {
				t.Fatalf("refused transition %s -> %s changed the stored state to %q", c.From, c.To, reloaded.State)
			}
		})
	}
}

// TestTransitionDetachPreservesRecordedPreviousVisibility proves the recorded
// previous visibility survives a detach exactly, for each starting tier, so the
// later restore has the real prior value rather than a default. The transition
// itself must not touch transcript visibility - restoration is the route's job.
func TestTransitionDetachPreservesRecordedPreviousVisibility(t *testing.T) {
	ctx := context.Background()
	pool := newScratchPool(t)
	q := sqlc.New(pool)
	owner := insertOwner(t, ctx, pool)

	for i, c := range loadVisibilityCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			transcriptID := insertTranscript(t, ctx, pool, owner, c.Visibility)
			attachment := createAttachment(t, ctx, q, owner, 100+i, Attached)

			if err := q.AttachPullRequestTranscript(ctx, sqlc.AttachPullRequestTranscriptParams{
				AttachmentID:       attachment.ID,
				TranscriptID:       transcriptID,
				Position:           0,
				PreviousVisibility: c.Visibility,
			}); err != nil {
				t.Fatalf("record previous visibility %q: %v", c.Visibility, err)
			}

			if _, err := Transition(ctx, q, attachment.ID, Detached); err != nil {
				t.Fatalf("detach transition failed: %v", err)
			}

			links, err := q.ListPullRequestAttachmentTranscripts(ctx, attachment.ID)
			if err != nil {
				t.Fatalf("list attachment transcripts after detach: %v", err)
			}
			if len(links) != 1 {
				t.Fatalf("attachment holds %d transcripts after detach, want 1", len(links))
			}
			if links[0].PreviousVisibility != c.Visibility {
				t.Fatalf("recorded previous visibility after detach = %q, want %q exactly (not a default)", links[0].PreviousVisibility, c.Visibility)
			}

			var stored string
			if err := pool.QueryRow(ctx, `SELECT visibility FROM transcripts WHERE id=$1`, transcriptID).Scan(&stored); err != nil {
				t.Fatalf("read transcript visibility: %v", err)
			}
			if stored != c.Visibility {
				t.Fatalf("detach changed the transcript visibility to %q; the transition must leave restoration to the route", stored)
			}
		})
	}
}

// TestAttachTranscriptPreservesOriginalSnapshot proves a re-bind after the
// transcript was widened does not overwrite the recorded previous visibility:
// the snapshot describes the transcript before the FIRST widening, so detach
// still restores the original tier.
func TestAttachTranscriptPreservesOriginalSnapshot(t *testing.T) {
	ctx := context.Background()
	pool := newScratchPool(t)
	q := sqlc.New(pool)
	owner := insertOwner(t, ctx, pool)

	transcriptID := insertTranscript(t, ctx, pool, owner, "private")
	attachment := createAttachment(t, ctx, q, owner, 500, Attached)

	if err := q.AttachPullRequestTranscript(ctx, sqlc.AttachPullRequestTranscriptParams{
		AttachmentID:       attachment.ID,
		TranscriptID:       transcriptID,
		Position:           0,
		PreviousVisibility: "private",
	}); err != nil {
		t.Fatalf("first bind: %v", err)
	}

	// A refresh or retry re-observes the pair after the transcript was widened.
	if err := q.AttachPullRequestTranscript(ctx, sqlc.AttachPullRequestTranscriptParams{
		AttachmentID:       attachment.ID,
		TranscriptID:       transcriptID,
		Position:           0,
		PreviousVisibility: "public",
	}); err != nil {
		t.Fatalf("re-bind: %v", err)
	}

	links, err := q.ListPullRequestAttachmentTranscripts(ctx, attachment.ID)
	if err != nil {
		t.Fatalf("list after re-bind: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("attachment holds %d transcripts, want 1", len(links))
	}
	if links[0].PreviousVisibility != "private" {
		t.Fatalf("re-bind overwrote the snapshot: previous_visibility = %q, want private", links[0].PreviousVisibility)
	}
}

// createAttachment inserts one attachment in the given state with a repo key
// unique to index i, so rows never collide on (github_repo_id, number).
func createAttachment(t *testing.T, ctx context.Context, q *sqlc.Queries, owner pgtype.UUID, i int, state State) sqlc.PullRequestAttachment {
	t.Helper()
	attachment, err := q.CreatePullRequestAttachment(ctx, sqlc.CreatePullRequestAttachmentParams{
		RepoOwner:    "acme",
		RepoName:     "widgets",
		GithubRepoID: int64(1000 + i),
		Number:       int32(i + 1),
		HeadSha:      "0123456789abcdef0123456789abcdef01234567",
		BaseRemote:   "https://github.com/acme/widgets.git",
		HeadRemote:   "https://github.com/acme/widgets.git",
		AuthorID:     owner,
		State:        string(state),
	})
	if err != nil {
		t.Fatalf("create attachment fixture in state %s: %v", state, err)
	}
	return attachment
}

// timestampFor returns the lifecycle timestamp a state stamps.
func timestampFor(attachment sqlc.PullRequestAttachment, state State) pgtype.Timestamptz {
	switch state {
	case Requested:
		return attachment.RequestedAt
	case Waiting:
		return attachment.WaitingAt
	case Preview:
		return attachment.PreviewAt
	case Attached:
		return attachment.AttachedAt
	case Detached:
		return attachment.DetachedAt
	}
	return pgtype.Timestamptz{}
}
