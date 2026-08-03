//go:build integration

package sqlc

// Integration tests for the SQLC-generated cli_auth_sessions queries.
//
// These tests require a running PostgreSQL instance with the cli_auth_sessions
// table already created (migration 003 applied).
//
// Run with:
//
//	TEST_DATABASE_URL="postgres://peasant:peasant@localhost:5432/peasant_test?sslmode=disable" \
//	  go test -tags=integration ./internal/database/sqlc/...
//
// Each test runs inside a transaction that is rolled back on completion, so
// tests are fully isolated and leave no state behind.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// testDatabaseURL returns the Postgres DSN to use for integration tests.
// Falls back to a local docker-compose default when TEST_DATABASE_URL is unset.
func testDatabaseURL(t *testing.T) string {
	t.Helper()
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		return url
	}
	// sslmode=disable is appropriate for local docker-compose postgres.
	return "postgres://peasant:peasant@localhost:5432/peasant_test?sslmode=disable"
}

// newTestQueries opens a single pgx connection, begins a transaction, and
// returns a *Queries scoped to that transaction plus the raw transaction (for
// direct SQL manipulation in time-travel tests).
//
// The transaction is always rolled back via t.Cleanup, keeping every test
// hermetic regardless of ordering.
func newTestQueries(t *testing.T) (*Queries, pgx.Tx) {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testDatabaseURL(t))
	if err != nil {
		t.Skipf("cannot connect to test database (%v); set TEST_DATABASE_URL or run without -short", err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		conn.Close(ctx)
		t.Fatalf("begin transaction: %v", err)
	}

	t.Cleanup(func() {
		// Always roll back — tests must not persist state.
		_ = tx.Rollback(ctx)
		conn.Close(ctx)
	})

	return New(tx), tx
}

// requireValidUUID fails the test if the UUID is null/invalid.
func requireValidUUID(t *testing.T, u pgtype.UUID) {
	t.Helper()
	if !u.Valid {
		t.Fatal("expected a valid UUID but got null/invalid")
	}
}

// --------------------------------------------------------------------------
// Test 1 — InsertCLISession
// --------------------------------------------------------------------------

func TestInsertCLISession(t *testing.T) {
	q, _ := newTestQueries(t)
	ctx := context.Background()

	id, err := q.InsertCLISession(ctx, InsertCLISessionParams{
		OauthState: "state-insert-1",
		CliPort:    9000,
		CliState:   "cli-state-insert-1",
	})
	if err != nil {
		t.Fatalf("InsertCLISession: %v", err)
	}
	requireValidUUID(t, id)
}

// --------------------------------------------------------------------------
// Test 2 — GetCLISessionByState
// --------------------------------------------------------------------------

func TestGetCLISessionByState(t *testing.T) {
	q, _ := newTestQueries(t)
	ctx := context.Background()

	const oauthState = "state-get-by-state-1"
	const cliState = "cli-state-get-1"
	const port int32 = 9001

	_, err := q.InsertCLISession(ctx, InsertCLISessionParams{
		OauthState: oauthState,
		CliPort:    port,
		CliState:   cliState,
	})
	if err != nil {
		t.Fatalf("InsertCLISession: %v", err)
	}

	session, err := q.GetCLISessionByState(ctx, oauthState)
	if err != nil {
		t.Fatalf("GetCLISessionByState: %v", err)
	}

	if session.OauthState != oauthState {
		t.Errorf("OauthState: got %q, want %q", session.OauthState, oauthState)
	}
	if session.CliState != cliState {
		t.Errorf("CliState: got %q, want %q", session.CliState, cliState)
	}
	if session.CliPort != port {
		t.Errorf("CliPort: got %d, want %d", session.CliPort, port)
	}
	// exchange_code, user_id, username must all be null on a fresh insert.
	if session.ExchangeCode.Valid {
		t.Errorf("ExchangeCode should be null after insert, got %q", session.ExchangeCode.String)
	}
	if session.UserID.Valid {
		t.Errorf("UserID should be null after insert")
	}
	if session.ExchangedAt.Valid {
		t.Errorf("ExchangedAt should be null after insert")
	}
}

// --------------------------------------------------------------------------
// Test 3 — UpdateCLISessionWithCode
// --------------------------------------------------------------------------

func TestUpdateCLISessionWithCode(t *testing.T) {
	q, _ := newTestQueries(t)
	ctx := context.Background()

	const oauthState = "state-update-code-1"
	_, err := q.InsertCLISession(ctx, InsertCLISessionParams{
		OauthState: oauthState,
		CliPort:    9002,
		CliState:   "cli-state-update-1",
	})
	if err != nil {
		t.Fatalf("InsertCLISession: %v", err)
	}

	exchangeCode := pgtype.Text{String: "xchg-code-abc123", Valid: true}
	userID := pgtype.UUID{Bytes: [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}, Valid: true}
	username := pgtype.Text{String: "testuser", Valid: true}

	err = q.UpdateCLISessionWithCode(ctx, UpdateCLISessionWithCodeParams{
		OauthState:   oauthState,
		ExchangeCode: exchangeCode,
		UserID:       userID,
		Username:     username,
	})
	if err != nil {
		t.Fatalf("UpdateCLISessionWithCode: %v", err)
	}

	session, err := q.GetCLISessionByState(ctx, oauthState)
	if err != nil {
		t.Fatalf("GetCLISessionByState after update: %v", err)
	}

	if !session.ExchangeCode.Valid || session.ExchangeCode.String != exchangeCode.String {
		t.Errorf("ExchangeCode: got valid=%v %q, want %q", session.ExchangeCode.Valid, session.ExchangeCode.String, exchangeCode.String)
	}
	if !session.UserID.Valid || session.UserID.Bytes != userID.Bytes {
		t.Errorf("UserID: got %v, want %v", session.UserID, userID)
	}
	if !session.Username.Valid || session.Username.String != username.String {
		t.Errorf("Username: got valid=%v %q, want %q", session.Username.Valid, session.Username.String, username.String)
	}
	// exchanged_at must still be null — code was set but not yet exchanged.
	if session.ExchangedAt.Valid {
		t.Errorf("ExchangedAt should still be null before exchange")
	}
}

// --------------------------------------------------------------------------
// Test 4 — ExchangeCLISession_Success
// --------------------------------------------------------------------------

func TestExchangeCLISession_Success(t *testing.T) {
	q, _ := newTestQueries(t)
	ctx := context.Background()

	const oauthState = "state-exchange-ok"
	const cliState = "cli-state-exchange-ok"
	const code = "xchg-success-code"

	if _, err := q.InsertCLISession(ctx, InsertCLISessionParams{
		OauthState: oauthState,
		CliPort:    9003,
		CliState:   cliState,
	}); err != nil {
		t.Fatalf("InsertCLISession: %v", err)
	}

	if err := q.UpdateCLISessionWithCode(ctx, UpdateCLISessionWithCodeParams{
		OauthState:   oauthState,
		ExchangeCode: pgtype.Text{String: code, Valid: true},
		UserID:       pgtype.UUID{Bytes: [16]byte{16, 15, 14}, Valid: true},
		Username:     pgtype.Text{String: "alice", Valid: true},
	}); err != nil {
		t.Fatalf("UpdateCLISessionWithCode: %v", err)
	}

	session, err := q.ExchangeCLISession(ctx, ExchangeCLISessionParams{
		ExchangeCode: pgtype.Text{String: code, Valid: true},
		CliState:     cliState,
	})
	if err != nil {
		t.Fatalf("ExchangeCLISession: %v", err)
	}

	if !session.ExchangedAt.Valid {
		t.Error("ExchangedAt should be set after a successful exchange")
	}
	requireValidUUID(t, session.ID)
	if session.OauthState != oauthState {
		t.Errorf("OauthState: got %q, want %q", session.OauthState, oauthState)
	}
}

// --------------------------------------------------------------------------
// Test 5 — ExchangeCLISession_AlreadyExchanged (one-time-use invariant)
// --------------------------------------------------------------------------

func TestExchangeCLISession_AlreadyExchanged(t *testing.T) {
	q, _ := newTestQueries(t)
	ctx := context.Background()

	const oauthState = "state-already-exchanged"
	const cliState = "cli-state-already-exchanged"
	const code = "xchg-one-time-code"

	if _, err := q.InsertCLISession(ctx, InsertCLISessionParams{
		OauthState: oauthState,
		CliPort:    9004,
		CliState:   cliState,
	}); err != nil {
		t.Fatalf("InsertCLISession: %v", err)
	}
	if err := q.UpdateCLISessionWithCode(ctx, UpdateCLISessionWithCodeParams{
		OauthState:   oauthState,
		ExchangeCode: pgtype.Text{String: code, Valid: true},
		UserID:       pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
		Username:     pgtype.Text{String: "bob", Valid: true},
	}); err != nil {
		t.Fatalf("UpdateCLISessionWithCode: %v", err)
	}

	params := ExchangeCLISessionParams{
		ExchangeCode: pgtype.Text{String: code, Valid: true},
		CliState:     cliState,
	}

	// First exchange — must succeed.
	if _, err := q.ExchangeCLISession(ctx, params); err != nil {
		t.Fatalf("first ExchangeCLISession: %v", err)
	}

	// Second exchange with the same code — must fail because exchanged_at IS NULL
	// is no longer satisfied.
	_, err := q.ExchangeCLISession(ctx, params)
	if err == nil {
		t.Fatal("second ExchangeCLISession should have returned an error (no rows), but succeeded")
	}
	if err != pgx.ErrNoRows {
		t.Errorf("expected pgx.ErrNoRows on second exchange, got: %v", err)
	}
}

// --------------------------------------------------------------------------
// Test 6 — ExchangeCLISession_Expired (TTL invariant: 5-minute window)
// --------------------------------------------------------------------------

func TestExchangeCLISession_Expired(t *testing.T) {
	q, tx := newTestQueries(t)
	ctx := context.Background()

	const oauthState = "state-expired"
	const cliState = "cli-state-expired"
	const code = "xchg-expired-code"

	if _, err := q.InsertCLISession(ctx, InsertCLISessionParams{
		OauthState: oauthState,
		CliPort:    9005,
		CliState:   cliState,
	}); err != nil {
		t.Fatalf("InsertCLISession: %v", err)
	}
	if err := q.UpdateCLISessionWithCode(ctx, UpdateCLISessionWithCodeParams{
		OauthState:   oauthState,
		ExchangeCode: pgtype.Text{String: code, Valid: true},
		UserID:       pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
		Username:     pgtype.Text{String: "charlie", Valid: true},
	}); err != nil {
		t.Fatalf("UpdateCLISessionWithCode: %v", err)
	}

	// Backdate created_at by 6 minutes so the 5-minute TTL window has elapsed.
	backdated := time.Now().UTC().Add(-6 * time.Minute)
	if _, err := tx.Exec(ctx,
		"UPDATE cli_auth_sessions SET created_at = $1 WHERE oauth_state = $2",
		backdated, oauthState,
	); err != nil {
		t.Fatalf("backdating created_at: %v", err)
	}

	_, err := q.ExchangeCLISession(ctx, ExchangeCLISessionParams{
		ExchangeCode: pgtype.Text{String: code, Valid: true},
		CliState:     cliState,
	})
	if err == nil {
		t.Fatal("ExchangeCLISession on an expired session should have failed, but succeeded")
	}
	if err != pgx.ErrNoRows {
		t.Errorf("expected pgx.ErrNoRows for expired session, got: %v", err)
	}
}

// --------------------------------------------------------------------------
// Test 7 — ExchangeCLISession_WrongState (cli_state mismatch)
// --------------------------------------------------------------------------

func TestExchangeCLISession_WrongState(t *testing.T) {
	q, _ := newTestQueries(t)
	ctx := context.Background()

	const oauthState = "state-wrong-cli-state"
	const cliState = "correct-cli-state"
	const code = "xchg-wrong-state-code"

	if _, err := q.InsertCLISession(ctx, InsertCLISessionParams{
		OauthState: oauthState,
		CliPort:    9006,
		CliState:   cliState,
	}); err != nil {
		t.Fatalf("InsertCLISession: %v", err)
	}
	if err := q.UpdateCLISessionWithCode(ctx, UpdateCLISessionWithCodeParams{
		OauthState:   oauthState,
		ExchangeCode: pgtype.Text{String: code, Valid: true},
		UserID:       pgtype.UUID{Bytes: [16]byte{3}, Valid: true},
		Username:     pgtype.Text{String: "dave", Valid: true},
	}); err != nil {
		t.Fatalf("UpdateCLISessionWithCode: %v", err)
	}

	_, err := q.ExchangeCLISession(ctx, ExchangeCLISessionParams{
		ExchangeCode: pgtype.Text{String: code, Valid: true},
		CliState:     "WRONG-cli-state", // intentionally wrong
	})
	if err == nil {
		t.Fatal("ExchangeCLISession with wrong cli_state should fail, but succeeded")
	}
	if err != pgx.ErrNoRows {
		t.Errorf("expected pgx.ErrNoRows for wrong cli_state, got: %v", err)
	}
}
