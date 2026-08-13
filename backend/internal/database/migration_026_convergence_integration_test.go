//go:build integration

package database

// TestMigration026_ConvergesAllEnvClasses — the compensating control for 026's
// guarded DDL. Guards (IF NOT EXISTS / DROP IF EXISTS) silently skip existing
// objects, so nothing in the migration itself proves that the three environment
// classes that exist in the wild end up with the same schema:
//
//   fresh  — never saw any 025 (CI, prod)
//   gen1   — applied the FIRST in-branch 025 (transcript_governance_events,
//            CASCADE FKs, no trigger) and recorded schema_migrations version=25
//   gen2   — applied the SECOND in-branch 025 (audit table + retract trigger +
//            permissiveness_rank) and recorded version=25
//
// The gen fixtures are byte-frozen history (testdata/migration_025_gen{1,2}.up.sql)
// — immutable by definition. Each class gets its own scratch DATABASE (per-conn
// search_path tricks don't survive a multi-conn pool), is driven to its historical
// state, then RunMigrations applies 026, and the resulting catalogs are diffed at
// CATALOG GRANULARITY — columns+defaults, named constraints, INDEXES, triggers,
// functions — against the fresh class. The index clause is the reason this test
// exists: an earlier draft of 026 created the audit index before dropping gen1's
// table, and the schema-wide index namespace made the guarded CREATE INDEX
// silently skip on exactly the environments 026 repairs.
//
// COST CONTROLS (Impl-UAT FIX-NOW): the 001–024 prefix is applied ONCE to a base
// database and the gen classes are CLONED from it (CREATE DATABASE … TEMPLATE —
// a file-level copy that also carries the schema_migrations rows), and the
// per-class work (fixture + migrate + fixpoint + publish-check + snapshot) runs
// in PARALLEL goroutines (error-returning helpers; t.* stays on the test
// goroutine). Database creation stays serial: concurrent CREATE DATABASE from
// one template fails with "source database is being accessed".
//
// Requires CREATEDB on the test role (CI's service-container superuser has it);
// otherwise the test skips with guidance.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationPrefixHash content-addresses the 001..max embedded migration SQL.
func migrationPrefixHash(t *testing.T, max int) string {
	t.Helper()
	h := sha256.New()
	for _, m := range migrations {
		if m.version > max {
			continue
		}
		b, err := migrationsFS.ReadFile(m.file)
		if err != nil {
			t.Fatalf("hash migration %d: %v", m.version, err)
		}
		fmt.Fprintf(h, "%d:", m.version)
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// baseCacheValid reports whether village_conv_base exists and was built from
// exactly this migration prefix.
func baseCacheValid(ctx context.Context, connectTo func(string) (*pgxpool.Pool, error), wantHash string) bool {
	pool, err := connectTo("village_conv_base")
	if err != nil {
		return false
	}
	defer pool.Close()
	var got string
	if err := pool.QueryRow(ctx, "SELECT prefix_hash FROM conv_base_meta LIMIT 1").Scan(&got); err != nil {
		return false
	}
	return got == wantHash
}

func TestMigration026_ConvergesAllEnvClasses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()

	adminURL := migration023TestDatabaseURL(t)
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Skipf("no test database available: %v", err)
	}
	// Close via t.Cleanup, NOT defer: the DROP DATABASE cleanups are registered
	// with t.Cleanup below, and cleanups run LIFO AFTER the function returns — a
	// defer here would close the pool before they fire, silently leaking the
	// scratch databases.
	t.Cleanup(admin.Close)
	if err := admin.Ping(ctx); err != nil {
		t.Skipf("test database not reachable: %v", err)
	}
	var canCreateDB bool
	if err := admin.QueryRow(ctx,
		"SELECT rolcreatedb OR rolsuper FROM pg_roles WHERE rolname = current_user").Scan(&canCreateDB); err != nil {
		t.Fatalf("check CREATEDB privilege: %v", err)
	}
	if !canCreateDB {
		t.Skipf("test role lacks CREATEDB: the convergence guard cannot run — grant CREATEDB to the test role (CI service containers are superuser)")
	}

	freshDB := func(name string) {
		t.Helper()
		if _, err := admin.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", name)); err != nil {
			t.Fatalf("drop scratch db %s: %v", name, err)
		}
		t.Cleanup(func() {
			_, _ = admin.Exec(context.Background(), fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", name))
		})
	}
	connectTo := func(dbName string) (*pgxpool.Pool, error) {
		cfg, err := pgxpool.ParseConfig(adminURL)
		if err != nil {
			return nil, fmt.Errorf("parse test database url: %w", err)
		}
		cfg.ConnConfig.Database = dbName
		return pgxpool.NewWithConfig(ctx, cfg)
	}

	// Base: 001–024 applied ONCE and CACHED across runs (content-addressed: a
	// marker table stores the sha256 of the embedded 001–024 SQL; a prefix change
	// rebuilds). The base is the one scratch DB deliberately NOT dropped at test
	// end — it is a bounded, hash-validated template cache (like a build cache),
	// which is what keeps the repeat-run cost at clone-speed instead of a full
	// prefix replay. The three per-class clones ARE dropped every run.
	prefixHash := migrationPrefixHash(t, 24)
	if !baseCacheValid(ctx, connectTo, prefixHash) {
		// plain drop (no cleanup registration): the base persists as the cache
		if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS village_conv_base WITH (FORCE)"); err != nil {
			t.Fatalf("drop stale base: %v", err)
		}
		if _, err := admin.Exec(ctx, "CREATE DATABASE village_conv_base"); err != nil {
			t.Fatalf("create base scratch db: %v", err)
		}
		basePool, err := connectTo("village_conv_base")
		if err != nil {
			t.Fatalf("connect base: %v", err)
		}
		applyRegisteredMigrationsUpTo(t, ctx, basePool, 24, "base")
		if _, err := basePool.Exec(ctx, "CREATE TABLE conv_base_meta (prefix_hash TEXT NOT NULL)"); err != nil {
			basePool.Close()
			t.Fatalf("create base cache meta table: %v", err)
		}
		if _, err := basePool.Exec(ctx, "INSERT INTO conv_base_meta VALUES ($1)", prefixHash); err != nil {
			basePool.Close()
			t.Fatalf("stamp base cache hash: %v", err)
		}
		basePool.Close() // a template must have no live connections
	}

	// Serial creation (template locking), parallel everything-after.
	classes := []struct {
		name    string
		fixture string // "" = fresh (no 025 generation; RunMigrations applies 026)
	}{
		{"fresh", ""},
		{"gen1", "testdata/migration_025_gen1.up.sql"},
		{"gen2", "testdata/migration_025_gen2.up.sql"},
	}
	for _, class := range classes {
		dbName := "village_conv_" + class.name
		freshDB(dbName)
		// Every class clones the 001–024 base (same embedded SQL a fresh replay
		// would run; RunMigrations-from-empty is exercised by every other
		// integration test) — the class differences START at the 025 generations.
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbName+" TEMPLATE village_conv_base"); err != nil {
			t.Fatalf("[%s] create scratch db: %v", class.name, err)
		}
	}

	snapshots := make(map[string][]string, len(classes))
	errs := make(map[string]error, len(classes))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, class := range classes {
		wg.Add(1)
		go func(name, fixture string) {
			defer wg.Done()
			snap, err := runConvergenceClass(ctx, connectTo, name, fixture)
			mu.Lock()
			snapshots[name], errs[name] = snap, err
			mu.Unlock()
		}(class.name, class.fixture)
	}
	wg.Wait()
	for _, class := range classes {
		if err := errs[class.name]; err != nil {
			t.Fatalf("[%s] %v", class.name, err)
		}
	}

	for _, class := range []string{"gen1", "gen2"} {
		if diff := diffSnapshots(snapshots["fresh"], snapshots[class]); diff != "" {
			t.Fatalf("schema divergence fresh vs %s — 026 is not a convergent fixpoint:\n%s", class, diff)
		}
	}
}

// runConvergenceClass drives one environment class to head and returns its
// catalog snapshot. Error-returning (no *testing.T): it runs off the test
// goroutine.
func runConvergenceClass(ctx context.Context, connectTo func(string) (*pgxpool.Pool, error), name, fixture string) ([]string, error) {
	pool, err := connectTo("village_conv_" + name)
	if err != nil {
		return nil, fmt.Errorf("connect scratch db: %w", err)
	}
	defer pool.Close()

	if fixture != "" {
		// The clone already carries 001–024 + their schema_migrations rows; add
		// the frozen 025 generation and record version=25 — exactly what a
		// branch-based environment ran.
		fixtureSQL, err := os.ReadFile(fixture)
		if err != nil {
			return nil, fmt.Errorf("read frozen fixture: %w", err)
		}
		if _, err := pool.Exec(ctx, string(fixtureSQL)); err != nil {
			return nil, fmt.Errorf("apply frozen 025 fixture: %w", err)
		}
		if _, err := pool.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES (25)"); err != nil {
			return nil, fmt.Errorf("record historical version 25: %w", err)
		}
	}

	if err := migrateDatabaseThrough(ctx, pool, migrationBoundary026); err != nil {
		return nil, fmt.Errorf("migrate through governance boundary: %w", err)
	}
	// Fixpoint: re-applying is a no-op, not an error.
	if err := migrateDatabaseThrough(ctx, pool, migrationBoundary026); err != nil {
		return nil, fmt.Errorf("governance boundary must be a fixpoint (re-apply failed): %w", err)
	}

	if err := publishWorks(ctx, pool, name); err != nil {
		return nil, err
	}
	return catalogSnapshot(ctx, pool)
}

// publishWorks proves the operational hazard is repaired per class: a
// publish-shaped INSERT with a declared actor succeeds and the trigger appends
// its 'published' audit row (on old-025 DBs at the version-skip bug, this path
// 500'd on a missing relation). Rolled back — the snapshot sees schema only.
func publishWorks(ctx context.Context, pool *pgxpool.Pool, class string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("publish check: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	var ownerID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id)
		VALUES (927001, 'convuser', '927001') RETURNING id::text
	`).Scan(&ownerID); err != nil {
		return fmt.Errorf("publish check: insert user: %w", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true)", SystemActorID); err != nil {
		return fmt.Errorf("publish check: declare system actor: %w", err)
	}
	var tid string
	if err := tx.QueryRow(ctx, `
		INSERT INTO transcripts (owner_id, local_id, title, model_provider, model_name, blob_key, schema_version)
		VALUES ($1, $2, 't', 'claude-code', 'm', $3, '2') RETURNING id::text
	`, ownerID, "conv-"+class, "transcripts/conv/"+class+".json").Scan(&tid); err != nil {
		return fmt.Errorf("publish check: insert transcript: %w", err)
	}
	var events int
	if err := tx.QueryRow(ctx,
		"SELECT count(*) FROM transcript_governance_events_audit WHERE transcript_id = $1::uuid", tid).Scan(&events); err != nil {
		return fmt.Errorf("publish check: count audit: %w", err)
	}
	if events != 1 {
		return fmt.Errorf("publish check: audit rows = %d, want exactly 1", events)
	}
	return nil
}

// applyRegisteredMigrationsUpTo mirrors RunMigrations for the registered
// migrations with version <= max — the mechanism a historical environment used
// to reach its pre-025 state. (Same package: uses the real migrations slice and
// embedded SQL, not copies.)
func applyRegisteredMigrationsUpTo(t *testing.T, ctx context.Context, pool *pgxpool.Pool, max int, class string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMPTZ DEFAULT now()
		)
	`); err != nil {
		t.Fatalf("[%s] create schema_migrations: %v", class, err)
	}
	for _, m := range migrations {
		if m.version > max {
			continue
		}
		sql, err := migrationsFS.ReadFile(m.file)
		if err != nil {
			t.Fatalf("[%s] read migration %d: %v", class, m.version, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("[%s] apply migration %d: %v", class, m.version, err)
		}
		if _, err := pool.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", m.version); err != nil {
			t.Fatalf("[%s] record migration %d: %v", class, m.version, err)
		}
	}
}

// catalogSnapshot captures the governance-relevant schema at catalog granularity.
// Ordinal positions are deliberately excluded (a dropped column leaves attnum
// gaps); identity is (table, column, type, nullability, default), plus
// constraint, index, trigger, and function DEFINITIONS.
func catalogSnapshot(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	var snap []string
	collect := func(kind, query string) error {
		rows, err := pool.Query(ctx, query)
		if err != nil {
			return fmt.Errorf("snapshot %s: %w", kind, err)
		}
		defer rows.Close()
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				return fmt.Errorf("scan %s: %w", kind, err)
			}
			snap = append(snap, kind+": "+line)
		}
		return rows.Err()
	}
	const tables = "('licenses','governance_event_types','transcript_governance_events_audit','transcripts','transcript_governance_events')"
	steps := []struct{ kind, query string }{
		{"column", `
		SELECT table_name || '.' || column_name || ' ' || data_type || ' nullable=' || is_nullable
		       || ' default=' || coalesce(column_default, '<none>')
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name IN ` + tables + `
		ORDER BY 1`},
		{"constraint", `
		SELECT tc.table_name || ' ' || tc.constraint_type || ' ' || tc.constraint_name ||
		       coalesce(' ' || cc.check_clause, '')
		FROM information_schema.table_constraints tc
		LEFT JOIN information_schema.check_constraints cc
		       ON cc.constraint_schema = tc.constraint_schema AND cc.constraint_name = tc.constraint_name
		WHERE tc.table_schema = 'public' AND tc.table_name IN ` + tables + `
		  -- exclude implicit NOT NULL checks BY NAME (<relid>_<attnum>_not_null):
		  -- their auto-names embed OIDs, nondeterministic across databases. Name-
		  -- based (not clause-text) so a real future CHECK whose clause contains
		  -- "IS NOT NULL" still differs.
		  AND (tc.constraint_type <> 'CHECK' OR tc.constraint_name NOT LIKE '%\_not\_null')
		ORDER BY 1`},
		{"index", `
		SELECT indexname || ' ' || indexdef FROM pg_indexes
		WHERE schemaname = 'public' AND tablename IN ` + tables + `
		ORDER BY 1`},
		{"trigger", `
		SELECT pg_get_triggerdef(t.oid) FROM pg_trigger t
		JOIN pg_class c ON c.oid = t.tgrelid
		WHERE NOT t.tgisinternal AND c.relname IN ` + tables + `
		ORDER BY 1`},
		{"function", `
		SELECT p.proname || ' ' || md5(p.prosrc) FROM pg_proc p
		JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'public' AND p.proname IN
		  ('audit_transcript_governance','audit_transcript_retract','governance_audit_block_mutation')
		ORDER BY 1`},
	}
	for _, s := range steps {
		if err := collect(s.kind, s.query); err != nil {
			return nil, err
		}
	}
	return snap, nil
}

func diffSnapshots(fresh, other []string) string {
	freshSet := map[string]bool{}
	for _, l := range fresh {
		freshSet[l] = true
	}
	otherSet := map[string]bool{}
	for _, l := range other {
		otherSet[l] = true
	}
	var b strings.Builder
	for _, l := range fresh {
		if !otherSet[l] {
			b.WriteString("  missing: " + l + "\n")
		}
	}
	for _, l := range other {
		if !freshSet[l] {
			b.WriteString("  extra:   " + l + "\n")
		}
	}
	return b.String()
}
