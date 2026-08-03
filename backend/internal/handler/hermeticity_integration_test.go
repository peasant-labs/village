//go:build integration

package handler

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/village/backend/internal/database"
)

// TestMain runs the zero-orphan governance-audit invariant as a POST-SUITE gate:
// after every test in this package has run (m.Run()), the append-only audit table
// must carry ZERO rows pointing at a deleted transcript. Because it fires strictly
// AFTER the whole suite, it catches an orphan leaked by a test of ANY ordering in a
// SINGLE run — superseding the old "run twice back-to-back, count orphans on the
// second run" hand-ritual (a mid-suite test only caught leaks ordered before it).
//
// Being in an `integration`-tagged file, this TestMain compiles ONLY into the
// integration test binary; the untagged unit suite is unaffected and uses the
// default test runner.
func TestMain(m *testing.M) {
	code := m.Run()
	if err := assertNoOrphanAuditRows(); err != nil {
		fmt.Fprintf(os.Stderr, "post-suite governance-audit orphan gate FAILED: %v\n", err)
		code = 1
	}
	os.Exit(code)
}

// orphanGateDatabaseURL resolves the test DSN the same way pullTestDatabaseURL
// does, but WITHOUT a *testing.T — the post-suite gate runs outside any test.
func orphanGateDatabaseURL() string {
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		return url
	}
	return "postgres://test:test@localhost:55432/village_test?sslmode=disable"
}

// assertNoOrphanAuditRows is the post-suite hermeticity self-check. It is
// FAIL-CLOSED: it returns nil ONLY when the database is unreachable (the whole
// integration suite skips in that mode too, so there is nothing to check). Every
// other failure — a build/migrate error, a query error, or an actual orphan —
// returns a non-nil error, forcing exit code 1. A gate that cannot run must never
// report clean.
//
// Why the GLOBAL NOT-EXISTS predicate is safe from cross-package transients (a
// future author MUST preserve these two invariants or this gate goes flaky):
//
//	(1) Concurrent audit-writers roll back: the only other integration package that
//	    writes this table (internal/database/migration_026_*) does its
//	    publish→delete→audit work inside one outer tx with defer tx.Rollback, so
//	    under READ COMMITTED its rows are never committed/visible to this pool.
//	(2) The handler governance suites are serial (no t.Parallel) with per-test
//	    deferred cleanupOwners, so no committed orphan window persists into this gate.
//
// If either changes (a test in ANY package COMMITS a publish+retract against the
// shared TEST_DATABASE_URL), scope this predicate to the suite's owners instead.
func assertNoOrphanAuditRows() error {
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, orphanGateDatabaseURL())
	if err != nil {
		return fmt.Errorf("orphan gate: build pool from TEST_DATABASE_URL: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		// DB unreachable — the integration suite skipped, so there is nothing to
		// verify. This is the ONLY nil-return path.
		return nil
	}

	// Bring the schema up first (idempotent, as govTestPool does): a MISSING
	// transcript_governance_events_audit table would otherwise let a broken schema
	// false-pass instead of erroring.
	if err := database.RunMigrations(pool); err != nil {
		return fmt.Errorf("orphan gate: RunMigrations: %w", err)
	}

	var orphans int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM transcript_governance_events_audit a
		WHERE NOT EXISTS (SELECT 1 FROM transcripts t WHERE t.id = a.transcript_id)`).Scan(&orphans); err != nil {
		// Reachable DB but the predicate itself failed (connection/relation/
		// transient): fail closed — never return nil here.
		return fmt.Errorf("orphan gate: count orphan audit rows: %w", err)
	}
	if orphans != 0 {
		return fmt.Errorf("found %d orphan governance-audit row(s) referencing deleted transcripts — a test "+
			"deleted its transcripts in-body without purging their audit rows. Fix the leaking test's "+
			"teardown to defer purgeAuditRows(t, ctx, pool, ids) for the ids it captured (see "+
			"integration_helpers_test.go cleanupOwners/purgeAuditRows).", orphans)
	}
	return nil
}
