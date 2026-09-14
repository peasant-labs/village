package promptattach

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// ErrTransitionNotAllowed is returned when the state machine refuses a move.
// Callers map it to a 409: the caller asked for something the lifecycle does
// not permit from the attachment's current state.
var ErrTransitionNotAllowed = errors.New("pull request attachment transition not allowed")

// ErrStaleState is returned when the row moved between the read and the write.
// The write is conditional on the state that was read, so a concurrent
// transition loses cleanly instead of clobbering the other move; the caller
// re-reads and retries.
var ErrStaleState = errors.New("pull request attachment state changed concurrently")

// Querier is the subset of *sqlc.Queries the transition path needs. Callers
// pass the generated queries; tests pass the same type over a real database.
type Querier interface {
	GetPullRequestAttachment(ctx context.Context, id pgtype.UUID) (sqlc.PullRequestAttachment, error)
	UpdatePullRequestAttachmentState(ctx context.Context, arg sqlc.UpdatePullRequestAttachmentStateParams) (sqlc.PullRequestAttachment, error)
}

// Transition is the ONLY path allowed to change an attachment's state. It
// reads the current state, validates the edge against the closed table, and
// writes the new state conditionally on the state it read. Anything else that
// wanted to move an attachment must call this function, so the table above is
// the single source of truth for the lifecycle.
//
// It does not run side effects: sharing transcripts, building a digest, posting
// the comment, or restoring visibility are the caller's job. This function only
// makes the move and stamps the target state's timestamp.
func Transition(ctx context.Context, q Querier, id pgtype.UUID, next State) (sqlc.PullRequestAttachment, error) {
	if err := next.Validate(); err != nil {
		return sqlc.PullRequestAttachment{}, err
	}

	current, err := q.GetPullRequestAttachment(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.PullRequestAttachment{}, fmt.Errorf("pull request attachment %s was not found, so no transition to %s was attempted; confirm the attachment id and that the pull request it belongs to was observed", id, next)
		}
		return sqlc.PullRequestAttachment{}, fmt.Errorf("could not read pull request attachment %s before transitioning to %s; no state was changed: %w", id, next, err)
	}

	from, err := Parse(current.State)
	if err != nil {
		return sqlc.PullRequestAttachment{}, err
	}
	if !CanTransition(from, next) {
		return sqlc.PullRequestAttachment{}, fmt.Errorf("%w: attachment %s cannot move from %s to %s; the allowed moves from %s are %s", ErrTransitionNotAllowed, id, from, next, from, allowedFrom(from))
	}

	updated, err := q.UpdatePullRequestAttachmentState(ctx, sqlc.UpdatePullRequestAttachmentStateParams{
		State:         string(next),
		ID:            id,
		ExpectedState: string(from),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.PullRequestAttachment{}, fmt.Errorf("%w: attachment %s was no longer in %s when the move to %s was applied; re-read and retry", ErrStaleState, id, from, next)
		}
		return sqlc.PullRequestAttachment{}, fmt.Errorf("could not move attachment %s from %s to %s; the previous state is unchanged: %w", id, from, next, err)
	}
	return updated, nil
}

// allowedFrom renders the legal targets for an actionable refusal message.
func allowedFrom(from State) string {
	targets, ok := allowedTransitions[from]
	if !ok {
		return "none"
	}
	var names []string
	for _, candidate := range All {
		if targets[candidate] {
			names = append(names, string(candidate))
		}
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
