//go:build integration

package database

import (
	"context"
	_ "embed"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed testdata/migration-049-checks.yaml
var migration049ChecksYAML []byte

type migration049Check struct {
	Name      string `yaml:"name"`
	Statement string `yaml:"statement"`
	Expect    string `yaml:"expect"`
	CheckSQL  string `yaml:"check_sql"`
	Want      string `yaml:"want"`
}

var requiredMigration049Checks = []string{
	"checked_with_turns_ok",
	"checked_without_turns_ok",
	"unchecked_refuses_turns",
	"unknown_verdict_refused",
	"bad_version_refused",
	"inherited_flag_cascades_with_transcript",
	"tombstone_survives_transcript_delete",
	"tombstone_goes_with_the_account",
	"attempt_unknown_error_refused",
	"attempt_read_failed_ok",
}

func TestMigration049_Checks(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)
	cases := decodeStrictYAML[migration049Check](t, migration049ChecksYAML)
	names := make([]string, 0, len(cases))
	for _, c := range cases {
		names = append(names, c.Name)
	}
	requireNames(t, names, requiredMigration049Checks)

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			id, owner := seedMigration048Transcript(t, pool)
			sub := strings.NewReplacer("{transcript_id}", id, "{owner_id}", owner, "{hash}", hashNamed("fixture"))
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id',$1,true), set_config('app.transcript_writer_version','1',true)", SystemActorID); err != nil {
				t.Fatal(err)
			}
			for _, stmt := range strings.Split(sub.Replace(c.Statement), ";\n") {
				_, execErr := tx.Exec(ctx, stmt)
				if execErr == nil {
					continue
				}
				if c.Expect == "ok" || !strings.Contains(execErr.Error(), c.Expect) {
					t.Fatalf("statement error = %v, want %q", execErr, c.Expect)
				}
				return
			}
			if c.Expect != "ok" {
				t.Fatalf("statement succeeded, want an error containing %q", c.Expect)
			}
			if c.CheckSQL != "" {
				var got string
				if err := tx.QueryRow(ctx, sub.Replace(c.CheckSQL)).Scan(&got); err != nil {
					t.Fatal(err)
				}
				if got != c.Want {
					t.Fatalf("check returned %q, want %q", got, c.Want)
				}
			}
		})
	}
}

// catalog049 renders the columns, constraints and indexes of the four tables
// migration 049 creates.
func catalog049(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	const tables = `('transcript_integrity_results','transcript_integrity_inherited_flags','integrity_flag_tombstones','transcript_integrity_attempts')`
	var out []string
	collect := func(query string) {
		rows, err := pool.Query(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			out = append(out, line)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	}
	collect(`SELECT 'column ' || table_name || ' ' || column_name || ' ' || data_type || ' ' || is_nullable || ' ' || coalesce(column_default, '')
		FROM information_schema.columns WHERE table_name IN ` + tables + ` AND table_schema = current_schema() ORDER BY table_name, column_name`)
	collect(`SELECT 'constraint ' || conrelid::regclass || ' ' || conname || ' ' || pg_get_constraintdef(oid) FROM pg_constraint
		WHERE conrelid::regclass::text IN ` + tables + ` ORDER BY conrelid::regclass::text, conname`)
	collect(`SELECT 'index ' || indexdef FROM pg_indexes WHERE tablename IN ` + tables + ` AND schemaname = current_schema() ORDER BY indexname`)
	collect(`SELECT 'trigger ' || tgname FROM pg_trigger WHERE tgrelid::regclass::text IN ` + tables + ` AND NOT tgisinternal ORDER BY tgname`)
	return strings.Join(out, "\n")
}

func TestMigration049_RoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	for _, m := range migrations {
		if m.version >= 49 {
			continue
		}
		if err := runMigration(pool, m); err != nil {
			t.Fatalf("run migration %d: %v", m.version, err)
		}
	}
	// The down migration must restore exactly the state migration 048 left.
	before048 := catalog048(t, pool)
	before := catalog049(t, pool)
	if before != "" {
		t.Fatalf("the integrity tables exist before migration 049:\n%s", before)
	}

	upSQL, err := migrationsFS.ReadFile("migrations/049_transcript_integrity.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	downSQL, err := migrationsFS.ReadFile("migrations/049_transcript_integrity.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("up: %v", err)
	}
	afterUp := catalog049(t, pool)
	if afterUp == "" {
		t.Fatal("the up migration changed nothing in the catalog")
	}
	if got := catalog048(t, pool); got != before048 {
		t.Fatalf("the up migration changed the transcripts catalog\nbefore:\n%s\n\nafter:\n%s", before048, got)
	}
	if _, err := pool.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("down: %v", err)
	}
	if got := catalog049(t, pool); got != before {
		t.Fatalf("down left integrity objects behind:\n%s", got)
	}
	if got := catalog048(t, pool); got != before048 {
		t.Fatalf("down did not restore the 048 catalog\nbefore:\n%s\n\nafter down:\n%s", before048, got)
	}
	if _, err := pool.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("second up: %v", err)
	}
	if got := catalog049(t, pool); got != afterUp {
		t.Fatalf("second up reached a different catalog\nfirst:\n%s\n\nsecond:\n%s", afterUp, got)
	}
}
