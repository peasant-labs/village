//go:build integration

package promptattach

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// parkedWriteQuerier delegates the transition path's two calls to the generated
// queries over a real database, but pauses the conditional write until the test
// releases it. The pause happens AFTER the real read returns, which is exactly
// the window Transition leaves open between reading the current state and
// applying the write conditional on it. The wrapper reimplements nothing: both
// calls delegate to the real queries; only the pause is added.
type parkedWriteQuerier struct {
	inner   *sqlc.Queries
	read    chan struct{}
	release chan struct{}
	once    sync.Once
}

func (q *parkedWriteQuerier) GetPullRequestAttachment(ctx context.Context, id pgtype.UUID) (sqlc.PullRequestAttachment, error) {
	row, err := q.inner.GetPullRequestAttachment(ctx, id)
	if err == nil {
		q.once.Do(func() { close(q.read) })
	}
	return row, err
}

func (q *parkedWriteQuerier) UpdatePullRequestAttachmentState(ctx context.Context, arg sqlc.UpdatePullRequestAttachmentStateParams) (sqlc.PullRequestAttachment, error) {
	<-q.release
	return q.inner.UpdatePullRequestAttachmentState(ctx, arg)
}

// TestTransitionConditionalWriteLosesCleanlyUnderInterleaving is the executable
// proof that the state write is conditional on the state Transition read. The
// loser reads `waiting` and is parked before its UPDATE; a second connection
// moves the same row to `attached` inside that window; the parked UPDATE then
// matches no row and Transition reports ErrStaleState.
//
// The sequential matrix cannot substitute for this: it never has two readers of
// one row, so removing the `state = expected_state` predicate or mis-translating
// its no-row result would leave it green. The interleaving is deterministic
// (channels, never sleeps) and both connections run the production SQL.
func TestTransitionConditionalWriteLosesCleanlyUnderInterleaving(t *testing.T) {
	ctx := context.Background()
	pool := newScratchPool(t)
	q := sqlc.New(pool)
	owner := insertOwner(t, ctx, pool)
	attachment := createAttachment(t, ctx, q, pool, owner, 700, Waiting)

	parked := &parkedWriteQuerier{
		inner:   q,
		read:    make(chan struct{}),
		release: make(chan struct{}),
	}

	loser := make(chan error, 1)
	go func() {
		_, err := Transition(ctx, parked, attachment.ID, Attached)
		loser <- err
	}()

	// Wait until the loser has read `waiting` and is parked before its write.
	<-parked.read

	// The winner moves the same row through the real queries while the loser is
	// parked: the stored state is now `attached`.
	if _, err := Transition(ctx, q, attachment.ID, Attached); err != nil {
		t.Fatalf("the winner's transition failed: %v", err)
	}

	close(parked.release)
	if err := <-loser; !errors.Is(err, ErrStaleState) {
		t.Fatalf("the parked transition's error = %v, want ErrStaleState: the conditional write must lose when the state it read is gone", err)
	}

	final, err := q.GetPullRequestAttachment(ctx, attachment.ID)
	if err != nil {
		t.Fatalf("re-read the attachment after the interleaving: %v", err)
	}
	if final.State != string(Attached) {
		t.Fatalf("final state = %q, want the winner's %q", final.State, Attached)
	}
	if !final.AttachedAt.Valid {
		t.Fatal("the winner's move did not stamp attached_at")
	}
}

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
			attachment := createAttachment(t, ctx, q, pool, owner, i, c.From)

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
			attachment := createAttachment(t, ctx, q, pool, owner, 100+i, Attached)

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
	attachment := createAttachment(t, ctx, q, pool, owner, 500, Attached)

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

// createAttachment inserts one attachment and seeds the given lifecycle state.
// Creation always initialises 'requested' (the store's rule); a test that needs
// another source state sets it directly, the documented test-only path. The repo
// key is unique to index i, so rows never collide on (github_repo_id, number).
func createAttachment(t *testing.T, ctx context.Context, q *sqlc.Queries, pool *pgxpool.Pool, owner pgtype.UUID, i int, state State) sqlc.PullRequestAttachment {
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
	})
	if err != nil {
		t.Fatalf("create attachment fixture: %v", err)
	}
	if state != Requested {
		if _, err := pool.Exec(ctx, `UPDATE pull_request_attachments SET state=$2 WHERE id=$1`, attachment.ID, string(state)); err != nil {
			t.Fatalf("seed attachment state %s: %v", state, err)
		}
		attachment.State = string(state)
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
