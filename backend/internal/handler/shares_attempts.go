package handler

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// openShareAttemptIndex is the partial unique index that admits at most one
// open attempt per transcript and collective.
const openShareAttemptIndex = "uq_share_attempt_open"

// shareAttemptEventNumConstraint is the UNIQUE (transcript_id, group_id,
// event_num) constraint that keeps the event ordering of one pair dense and
// unambiguous. Its name is PostgreSQL's generated one, pinned here because the
// error carries the NAME and nothing else; a rename would silently turn every
// conflict below back into an unexplained failure, which is why
// TestShareAttemptConstraintNamesMatchTheCatalog asserts both names against the
// live catalog.
const shareAttemptEventNumConstraint = "transcript_share_attempts_transcript_id_group_id_event_num_key"

// uniqueViolation is PostgreSQL's SQLSTATE for a unique-constraint conflict.
const uniqueViolation = "23505"

// shareAttemptIsLive reports whether an attempt in this state still occupies
// the transcript's place in a collective. A live attempt makes a second
// submission a duplicate; any closed state makes it the next attempt.
func shareAttemptIsLive(status string) bool {
	return status == "pending" || status == "approved"
}

// shareAttemptConflict names the ways PostgreSQL refuses a NEW share attempt.
// It is a closed set so both share paths answer a conflict by its kind rather
// than by matching on a driver message, and so a conflict class nobody
// classified stays visibly unhandled instead of being mistaken for one that is.
type shareAttemptConflict string

const (
	// shareAttemptConflictNone means the error is not a refusal of a new
	// attempt at all - it is some other database failure.
	shareAttemptConflictNone shareAttemptConflict = ""

	// shareAttemptConflictOpen is the partial unique index refusing a second
	// submission while one is still awaiting review. The submission the caller
	// asked for is a DUPLICATE: retrying changes nothing until the live
	// submission is decided or withdrawn.
	shareAttemptConflictOpen shareAttemptConflict = "open_attempt"

	// shareAttemptConflictEventNum is the event-ordering constraint refusing
	// two events that claim the same ordinal in one pair. It happens when
	// another writer appended an event this statement could not yet see, so the
	// ordinal it computed was taken by the time it wrote. Unlike the duplicate
	// above, RETRYING SUCCEEDS: the next read sees the committed event and
	// either computes the following ordinal or reports the pair as already
	// contributed. Telling the two apart is the whole point of this type - they
	// need opposite advice.
	shareAttemptConflictEventNum shareAttemptConflict = "event_num"
)

// classifyShareAttemptConflict recognises the database's refusals of a new share
// attempt, so a concurrent writer reaches the caller as an answer they can act
// on rather than as a raw constraint violation or an unexplained failure.
//
// BOTH share paths - the single transcript and the whole project - classify
// through this one function. They previously agreed only about the open-attempt
// index and both fell through to a generic failure for the ordering conflict;
// keeping one classifier is what stops them from drifting apart again.
func classifyShareAttemptConflict(err error) shareAttemptConflict {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return shareAttemptConflictNone
	}
	if pgErr.Code != uniqueViolation {
		return shareAttemptConflictNone
	}
	switch pgErr.ConstraintName {
	case openShareAttemptIndex:
		return shareAttemptConflictOpen
	case shareAttemptEventNumConstraint:
		return shareAttemptConflictEventNum
	default:
		return shareAttemptConflictNone
	}
}

func pluralCollectives(n int) string {
	if n == 1 {
		return "this collective"
	}
	return "every collective named in this request"
}
