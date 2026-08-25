package handler

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// openShareAttemptIndex is the partial unique index that admits at most one
// open attempt per transcript and collective.
const openShareAttemptIndex = "uq_share_attempt_open"

// uniqueViolation is PostgreSQL's SQLSTATE for a unique-constraint conflict.
const uniqueViolation = "23505"

// shareAttemptIsLive reports whether an attempt in this state still occupies
// the transcript's place in a collective. A live attempt makes a second
// submission a duplicate; any closed state makes it the next attempt.
func shareAttemptIsLive(status string) bool {
	return status == "pending" || status == "approved"
}

// isOpenShareAttemptConflict recognises the database's refusal of a second
// open attempt, so a concurrent duplicate submission reaches the caller as the
// same actionable answer the application-level check gives rather than as a
// raw constraint violation.
func isOpenShareAttemptConflict(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == uniqueViolation && pgErr.ConstraintName == openShareAttemptIndex
}

func pluralCollectives(n int) string {
	if n == 1 {
		return "this collective"
	}
	return "every collective named in this request"
}
