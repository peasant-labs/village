//go:build integration

package database

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// errPlanCaptured stops the generated query once its plan has been read.
var errPlanCaptured = errors.New("plan captured")

// explainDBTX runs every query it is handed as EXPLAIN and keeps the plan, so a
// test can plan a generated sqlc statement exactly as the application sends it,
// with its own parameters, instead of a hand-written copy that could drift.
type explainDBTX struct {
	tx   pgx.Tx
	plan strings.Builder
}

func (e *explainDBTX) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("explainDBTX plans queries only")
}

func (e *explainDBTX) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	rows, err := e.tx.Query(ctx, "EXPLAIN "+sql, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			rows.Close()
			return nil, err
		}
		e.plan.WriteString(line)
		e.plan.WriteByte('\n')
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nil, errPlanCaptured
}

func (e *explainDBTX) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	panic("explainDBTX plans Query statements only")
}

// pageRootsReadInIndexOrder reports whether the plan's page_roots step cuts
// root_limit roots straight off the named index: its Limit sits directly on a
// scan of that index, with no Sort between them. An index used only for its
// owner_id range would put a Sort there, so naming the index is not enough.
func pageRootsReadInIndexOrder(plan, indexName string) bool {
	var nodes []string
	inPageRoots := false
	for _, line := range strings.Split(plan, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "CTE ") {
			inPageRoots = trimmed == "CTE page_roots"
			continue
		}
		if inPageRoots && strings.HasPrefix(trimmed, "->") {
			nodes = append(nodes, strings.TrimSpace(strings.TrimPrefix(trimmed, "->")))
			if len(nodes) == 2 {
				break
			}
		}
	}
	return len(nodes) == 2 &&
		strings.HasPrefix(nodes[0], "Limit") &&
		(strings.HasPrefix(nodes[1], "Index Scan using "+indexName+" ") ||
			strings.HasPrefix(nodes[1], "Index Scan Backward using "+indexName+" ") ||
			strings.HasPrefix(nodes[1], "Index Only Scan using "+indexName+" "))
}

// TestMigration052ContributionActivityIndexLifecycle proves the index exists
// after the migrations with its key in order, that the generated paged
// contribution query reads its roots in index order on the first page and on a
// cursor page (so the key matches the query's sort expression), that
// it goes away with the down migration, and that re-running the up migration
// creates it again.
func TestMigration052ContributionActivityIndexLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)

	const indexName = "idx_transcripts_owner_last_activity"
	indexCount := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE tablename = 'transcripts' AND indexname = $1`, indexName).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", indexName, err)
		}
		return n
	}

	var definition string
	if err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes WHERE tablename = 'transcripts' AND indexname = $1`, indexName).Scan(&definition); err != nil {
		t.Fatalf("the index must exist after the migrations: %v", err)
	}
	// The key in its order: owner, last activity newest first, then id. The
	// epoch literal is printed in the session time zone, so only its type is
	// pinned here; the structure test pins the statement itself.
	const keyHead = "USING btree (owner_id, COALESCE(session_end, session_start, published_at, '"
	const keyTail = "'::timestamp with time zone) DESC, id)"
	if !strings.Contains(definition, keyHead) || !strings.HasSuffix(definition, keyTail) {
		t.Fatalf("index definition = %q, want a btree on (owner_id, last activity DESC, id)", definition)
	}

	up, err := migrationsFS.ReadFile("migrations/052_contribution_activity_index.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/052_contribution_activity_index.down.sql")
	if err != nil {
		t.Fatal(err)
	}

	owner := pgtype.UUID{Bytes: [16]byte{0x05, 0x02}, Valid: true}
	group := pgtype.UUID{Bytes: [16]byte{0x05, 0x02, 0x01}, Valid: true}
	firstPage := sqlc.ListContributionPageParams{GroupID: group, OwnerID: owner, MatchingProjects: []string{}, RootLimit: 500}
	cursorPage := firstPage
	cursorPage.AfterID = pgtype.UUID{Bytes: [16]byte{0x05, 0x02, 0x02}, Valid: true}
	cursorPage.AfterTime = pgtype.Timestamptz{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}

	// The planner question: with sequential scans and explicit sorts priced
	// out, the root order of the generated page query must come from the index.
	// An empty scratch table would otherwise make any plan look as cheap.
	plan := func(params sqlc.ListContributionPageParams) string {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin explain: %v", err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
			t.Fatalf("disable sequential scans for the explain: %v", err)
		}
		if _, err := tx.Exec(ctx, "SET LOCAL enable_sort = off"); err != nil {
			t.Fatalf("disable explicit sorts for the explain: %v", err)
		}
		db := &explainDBTX{tx: tx}
		if _, err := sqlc.New(db).ListContributionPage(ctx, params); !errors.Is(err, errPlanCaptured) {
			t.Fatalf("plan the paged contribution query: %v", err)
		}
		return db.plan.String()
	}
	for name, params := range map[string]sqlc.ListContributionPageParams{"first page": firstPage, "cursor page": cursorPage} {
		if p := plan(params); !pageRootsReadInIndexOrder(p, indexName) {
			t.Fatalf("the %s of the paged contribution query does not cut its roots straight off the index; plan=%s", name, p)
		}
	}

	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("reverse migration 052: %v", err)
	}
	if n := indexCount(); n != 0 {
		t.Fatalf("after the down migration the index count = %d, want 0", n)
	}
	if p := plan(firstPage); pageRootsReadInIndexOrder(p, indexName) {
		t.Fatalf("after the down migration the page roots still read the index; plan=%s", p)
	}

	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("re-run migration 052 up after down: %v", err)
	}
	if n := indexCount(); n != 1 {
		t.Fatalf("after re-running the up migration the index count = %d, want 1", n)
	}
	if p := plan(firstPage); !pageRootsReadInIndexOrder(p, indexName) {
		t.Fatalf("after re-running the up migration the page roots do not read the index in order; plan=%s", p)
	}
}
