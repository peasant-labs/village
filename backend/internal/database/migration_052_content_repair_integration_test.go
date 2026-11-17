//go:build integration

package database

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMigration052ContentRepairRealPostgres(t *testing.T) {
	ctx := context.Background()
	fixture := loadRepairAuditFixtures(t)
	pool := newMigrationScratchDatabase(t)
	migrateTestDatabaseThrough(t, pool, 51)
	if err := runMigration(pool, requireMigrationVersion(t, 52)); err != nil {
		t.Fatal(err)
	}

	setup, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner := insertFenceOwner(t, ctx, setup)
	if _, err := setup.Exec(ctx, "SELECT set_config('app.actor_id',$1,true), set_config('app.transcript_writer_version','1',true)", owner); err != nil {
		t.Fatal(err)
	}
	transcript, err := insertFenceTranscript(ctx, setup, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := setup.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version','1',true)"); err != nil {
				t.Fatal(err)
			}
			switch c.Kind {
			case "registration":
				var registered bool
				if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version=52)").Scan(&registered); err != nil || !registered {
					t.Fatalf("version 52 registration=%t: %v", registered, err)
				}
			case "catalog":
				assertRepairAuditCatalog(t, ctx, tx, fixture)
			case "":
				for i, step := range c.Steps {
					t.Run(fmt.Sprintf("step-%d", i+1), func(t *testing.T) {
						runRepairAuditStep(t, ctx, tx, fixture.Defaults, step, owner, transcript)
					})
				}
			default:
				t.Fatalf("unknown repair audit fixture kind %q", c.Kind)
			}
			if c.VerifyAfterCommit != "" && !t.Failed() {
				if err := tx.Commit(ctx); err != nil {
					t.Fatalf("commit repair audit fixture: %v", err)
				}
				var verified bool
				if err := pool.QueryRow(ctx, strings.ReplaceAll(c.VerifyAfterCommit, "{{transcript}}", transcript)).Scan(&verified); err != nil || !verified {
					t.Fatalf("verify committed repair audit fixture: verified=%t err=%v", verified, err)
				}
			}
		})
	}

	t.Run("down-removes-ledger-and-functions", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		down, err := migrationsFS.ReadFile("migrations/052_transcript_content_repair.down.sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, string(down)); err != nil {
			t.Fatal(err)
		}
		var absent bool
		err = tx.QueryRow(ctx, `SELECT to_regclass('public.transcript_content_repairs') IS NULL
			AND to_regprocedure('public.repair_audit_block_delete()') IS NULL
			AND to_regprocedure('public.repair_audit_block_update()') IS NULL
			AND to_regprocedure('public.repair_audit_assign_actor()') IS NULL`).Scan(&absent)
		if err != nil || !absent {
			t.Fatalf("down migration left a ledger or trigger function: absent=%t err=%v", absent, err)
		}
	})
}

func assertRepairAuditCatalog(t *testing.T, ctx context.Context, tx pgx.Tx, fixture repairAuditFixture) {
	t.Helper()
	rows, err := tx.Query(ctx, `SELECT a.attname, format_type(a.atttypid,a.atttypmod),
		NOT a.attnotnull, a.attidentity::text, COALESCE(pg_get_expr(d.adbin,d.adrelid),'')
		FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
		WHERE a.attrelid='transcript_content_repairs'::regclass AND a.attnum>0 AND NOT a.attisdropped
		ORDER BY a.attnum`)
	if err != nil {
		t.Fatal(err)
	}
	var columns []repairAuditColumn
	for rows.Next() {
		var c repairAuditColumn
		if err := rows.Scan(&c.Name, &c.Type, &c.Nullable, &c.Identity, &c.Default); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if !reflect.DeepEqual(columns, fixture.Catalog.Columns) {
		t.Fatalf("repair ledger columns differ from fixture:\n got %#v\nwant %#v", columns, fixture.Catalog.Columns)
	}
	assertCatalogNames(t, ctx, tx, "checks", `SELECT conname::text FROM pg_constraint
		WHERE conrelid='transcript_content_repairs'::regclass AND contype='c'`, fixture.Catalog.Checks)
	assertCatalogNames(t, ctx, tx, "indexes", `SELECT indexname::text FROM pg_indexes
		WHERE schemaname=current_schema() AND tablename='transcript_content_repairs'`, fixture.Catalog.Indexes)
	assertCatalogNames(t, ctx, tx, "triggers", `SELECT tgname::text FROM pg_trigger
		WHERE tgrelid='transcript_content_repairs'::regclass AND NOT tgisinternal`, fixture.Catalog.Triggers)
	var keysMatch bool
	err = tx.QueryRow(ctx, `SELECT
		(SELECT pg_get_constraintdef(oid) FROM pg_constraint
		 WHERE conrelid='transcript_content_repairs'::regclass AND contype='p') = 'PRIMARY KEY (repair_id, phase)'
		AND (SELECT pg_get_expr(indpred,indrelid) FROM pg_index
		     WHERE indexrelid='transcript_content_repairs_one_terminal'::regclass) = '(phase <> ''attempted''::text)'
		AND (SELECT pg_get_expr(indpred,indrelid) FROM pg_index
		     WHERE indexrelid='transcript_content_repairs_citations'::regclass) = '(phase <> ''attempted''::text)'
		AND (SELECT pg_get_indexdef(indexrelid,1,true) FROM pg_index
		     WHERE indexrelid='transcript_content_repairs_citations'::regclass) = 'reconciles_repair_id'
		AND (SELECT NOT indisunique AND indisvalid AND indisready FROM pg_index
		     WHERE indexrelid='transcript_content_repairs_citations'::regclass)`).Scan(&keysMatch)
	if err != nil || !keysMatch {
		t.Fatalf("repair ledger primary key or partial indexes differ: matches=%t err=%v", keysMatch, err)
	}
}

func assertCatalogNames(t *testing.T, ctx context.Context, tx pgx.Tx, label, query string, expected []string) {
	t.Helper()
	rows, err := tx.Query(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actual []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := append([]string(nil), expected...)
	sort.Strings(actual)
	sort.Strings(want)
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("repair ledger %s: got %v, want %v", label, actual, want)
	}
}

func runRepairAuditStep(t *testing.T, ctx context.Context, tx pgx.Tx, defaults map[string]any, step repairAuditStep, owner, transcript string) {
	t.Helper()
	actor := owner
	switch step.Actor {
	case "":
	case "missing":
		actor = ""
	case "system":
		actor = SystemActorID
	default:
		t.Fatalf("unknown actor fixture %q", step.Actor)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id',$1,true)", actor); err != nil {
		t.Fatal(err)
	}
	savepoint, err := tx.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer savepoint.Rollback(ctx)

	if step.Expect == "true" {
		var got bool
		query := strings.ReplaceAll(step.SQL, "{{transcript}}", transcript)
		if err := savepoint.QueryRow(ctx, query).Scan(&got); err != nil || !got {
			t.Fatalf("fixture query: got %t, err %v", got, err)
		}
		if err := savepoint.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return
	}

	var command pgconn.CommandTag
	repairID := uuid.NewString()
	if step.SQL != "" {
		command, err = savepoint.Exec(ctx, strings.ReplaceAll(step.SQL, "{{transcript}}", transcript))
	} else {
		values := make(map[string]any, len(defaults)+len(step.Fields)+3)
		for key, value := range defaults {
			values[key] = value
		}
		for key, value := range step.Fields {
			values[key] = value
		}
		values["transcript_id"] = transcript
		if value, ok := values["repair_id"]; ok {
			repairID, ok = value.(string)
			if !ok {
				t.Fatal("fixture repair_id must be a UUID string")
			}
		}
		values["repair_id"] = repairID
		command, err = insertRepairFixture(ctx, savepoint, values)
	}
	if step.Expect == "reject" {
		if err == nil {
			t.Fatal("fixture expected PostgreSQL to refuse the statement")
		}
		var pgerr *pgconn.PgError
		if step.Constraint != "" && (!errors.As(err, &pgerr) || pgerr.ConstraintName != step.Constraint) {
			t.Fatalf("fixture expected constraint %q, got %v", step.Constraint, err)
		}
		if step.Message != "" && !strings.Contains(err.Error(), step.Message) {
			t.Fatalf("fixture expected error containing %q, got %v", step.Message, err)
		}
		return
	}
	if step.Expect != "accept" {
		t.Fatalf("unknown fixture expectation %q", step.Expect)
	}
	if err != nil {
		t.Fatalf("fixture expected statement to succeed: %v", err)
	}
	if step.Affected != nil && command.RowsAffected() != *step.Affected {
		t.Fatalf("affected rows = %d, want %d", command.RowsAffected(), *step.Affected)
	}
	if step.CheckActor {
		var got string
		if err := savepoint.QueryRow(ctx, "SELECT operator_actor::text FROM transcript_content_repairs WHERE repair_id=$1::uuid", repairID).Scan(&got); err != nil || got != owner {
			t.Fatalf("derived actor = %q, want %q: %v", got, owner, err)
		}
	}
	if err := savepoint.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func insertRepairFixture(ctx context.Context, tx pgx.Tx, values map[string]any) (pgconn.CommandTag, error) {
	columns := []string{
		"repair_id", "phase", "action", "transcript_id", "operator_actor", "authorization_ref", "reason",
		"prior_revision", "prior_identity_hash", "prior_identity_size", "resulting_revision",
		"resulting_identity_hash", "resulting_identity_size", "failure_class", "reconciles_repair_id",
	}
	args := make([]any, 0, len(columns))
	for _, key := range columns {
		value := values[key]
		if repeat, ok := value.(map[string]any); ok {
			letter, lettersOK := repeat["repeat"].(string)
			count, countOK := repeat["count"].(int)
			if !lettersOK || !countOK || count < 0 || count > 1001 {
				return pgconn.CommandTag{}, fmt.Errorf("invalid bounded repeated fixture value for %s", key)
			}
			value = strings.Repeat(letter, count)
		}
		args = append(args, value)
	}
	return tx.Exec(ctx, `INSERT INTO transcript_content_repairs (
		repair_id, phase, action, transcript_id, operator_actor, authorization_ref, reason,
		prior_revision, prior_identity_hash, prior_identity_size, resulting_revision,
		resulting_identity_hash, resulting_identity_size, failure_class, reconciles_repair_id)
		VALUES ($1::uuid,$2,$3,$4::uuid,$5::uuid,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15::uuid)`, args...)
}
