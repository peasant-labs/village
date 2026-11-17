//go:build integration

package database

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/contentstate"
)

func TestMigration053AbsenteeState(t *testing.T) {
	ctx := context.Background()
	fixture := loadAbsenteeStateFixtures(t)
	pool := newMigrationScratchDatabase(t)
	migrateTestDatabaseThrough(t, pool, 52)
	pre, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner := insertFenceOwner(t, ctx, pre)
	mustAbsenteeExec(t, ctx, pre, `SELECT set_config('app.actor_id',$1,true), set_config('app.transcript_writer_version','1',true)`, owner)
	historicalID, err := insertFenceTranscript(ctx, pre, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := pre.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var before uint32
	if err := pool.QueryRow(ctx, `SELECT relfilenode FROM pg_class WHERE oid='transcripts'::regclass`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := runMigration(pool, requireMigrationVersion(t, 53)); err != nil {
		t.Fatal(err)
	}

	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			switch c.Name {
			case "absentee-registers-053":
				var ok bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=53)`).Scan(&ok); err != nil || !ok {
					t.Fatalf("migration 53 must be recorded: present=%t err=%v", ok, err)
				}
			case "content-state-defaults-present":
				var state string
				var after uint32
				var fastDefault bool
				err := tx.QueryRow(ctx, `SELECT t.content_state, c.relfilenode, a.atthasmissing FROM transcripts t
					JOIN pg_class c ON c.oid='transcripts'::regclass
					JOIN pg_attribute a ON a.attrelid=c.oid AND a.attname='content_state' WHERE t.id=$1`, historicalID).Scan(&state, &after, &fastDefault)
				if err != nil || state != contentstate.Present.String() || after != before || !fastDefault {
					t.Fatalf("existing row or fast default: state=%q before=%d after=%d fast=%t err=%v", state, before, after, fastDefault, err)
				}
			case "content-state-closed-set":
				var def string
				if err := tx.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='transcripts_content_state_closed'`).Scan(&def); err != nil {
					t.Fatal(err)
				}
				var members []string
				for _, state := range fixture.States {
					members = append(members, "'"+strings.ReplaceAll(state, "'", "''")+"'::text")
				}
				wantDef := "CHECK ((content_state = ANY (ARRAY[" + strings.Join(members, ", ") + "])))"
				if def != wantDef {
					t.Fatalf("content-state closed set changed: %s", def)
				}
				for _, state := range fixture.States {
					mustAbsenteeExec(t, ctx, tx, `UPDATE transcripts SET content_state=$1 WHERE id=$2`, state, historicalID)
				}
				for _, state := range fixture.InvalidStates {
					assertAbsenteeRefused(t, ctx, tx, `UPDATE transcripts SET content_state=$1 WHERE id=$2`, state, historicalID)
				}
			case "content-state-change-writes-no-governance-event":
				var before, after int
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM transcript_governance_events_audit WHERE transcript_id=$1`, historicalID).Scan(&before); err != nil {
					t.Fatal(err)
				}
				mustAbsenteeExec(t, ctx, tx, `UPDATE transcripts SET content_state='unavailable' WHERE id=$1`, historicalID)
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM transcript_governance_events_audit WHERE transcript_id=$1`, historicalID).Scan(&after); err != nil || before != after {
					t.Fatalf("state-only change must not write governance events: %d -> %d, %v", before, after, err)
				}
			default:
				runAbsenteeRetractionCase(t, ctx, tx, c.Name, fixture.ShareStates)
			}
		})
	}
}

func runAbsenteeRetractionCase(t *testing.T, ctx context.Context, tx pgx.Tx, name string, shareStates []string) {
	t.Helper()
	owner := insertFenceOwner(t, ctx, tx)
	groupOwner := insertFenceOwner(t, ctx, tx)
	mustAbsenteeExec(t, ctx, tx, `SELECT set_config('app.actor_id',$1,true), set_config('app.transcript_writer_version','1',true)`, owner)
	id, err := insertFenceTranscript(ctx, tx, owner)
	if err != nil {
		t.Fatal(err)
	}
	visibility := "shared"
	if name == "retraction-audience-only-for-shared-visibility" {
		visibility = "public"
	}
	mustAbsenteeExec(t, ctx, tx, `UPDATE transcripts SET visibility=$1, license_id='CC0-1.0' WHERE id=$2`, visibility, id)
	groups := make(map[string]string)
	for _, status := range shareStates {
		var group string
		if err := tx.QueryRow(ctx, `INSERT INTO groups (name, created_by) VALUES ($1,$2) RETURNING id::text`, uuid.NewString(), groupOwner).Scan(&group); err != nil {
			t.Fatal(err)
		}
		groups[status] = group
		mustAbsenteeExec(t, ctx, tx, `INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status) VALUES ($1,$2,1,$3)`, id, group, status)
	}

	if name == "retraction-audit-event-unchanged" {
		mustAbsenteeExec(t, ctx, tx, `SELECT set_config('app.actor_id','',true)`)
		assertAbsenteeRefused(t, ctx, tx, `DELETE FROM transcripts WHERE id=$1`, id)
		mustAbsenteeExec(t, ctx, tx, `SELECT set_config('app.actor_id',$1,true)`, owner)
	}
	if name == "retraction-audience-captured-on-account-cascade" {
		mustAbsenteeExec(t, ctx, tx, `DELETE FROM users WHERE id=$1`, owner)
	} else {
		mustAbsenteeExec(t, ctx, tx, `DELETE FROM transcripts WHERE id=$1`, id)
	}

	var count int
	var event, eventVisibility, license, actor string
	err = tx.QueryRow(ctx, `SELECT count(*), min(event_type), min(visibility), min(license_id), min(changed_by::text)
		FROM transcript_governance_events_audit WHERE transcript_id=$1 AND event_type='retracted'`, id).
		Scan(&count, &event, &eventVisibility, &license, &actor)
	if err != nil || count != 1 || event != EventRetracted.String() || eventVisibility != visibility || license != string(schema.LicenseCC0) || actor != owner {
		t.Fatalf("retraction audit snapshot: count=%d event=%q visibility=%q license=%q actor=%q err=%v", count, event, eventVisibility, license, actor, err)
	}
	got := absenteeAudience(t, ctx, tx, id)
	want := []string{groups["approved"]}
	if visibility == string(schema.VisibilityPublic) {
		want = []string{}
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retraction audience: got %v want %v", got, want)
	}

	switch name {
	case "absentee-down-refuses":
		down, err := migrationsFS.ReadFile("migrations/053_transcript_absentee_state.down.sql")
		if err != nil {
			t.Fatal(err)
		}
		assertAbsenteeRefused(t, ctx, tx, string(down))
		if got := absenteeAudience(t, ctx, tx, id); !reflect.DeepEqual(got, want) {
			t.Fatalf("down refusal lost audience: got %v want %v", got, want)
		}
	case "retraction-audience-cascades-with-collective":
		mustAbsenteeExec(t, ctx, tx, `DELETE FROM groups WHERE id=$1`, groups["approved"])
		if got := absenteeAudience(t, ctx, tx, id); len(got) != 0 {
			t.Fatalf("group cascade left audience %v", got)
		}
	case "retraction-audience-append-only":
		assertAbsenteeRefused(t, ctx, tx, `UPDATE transcript_retraction_audiences SET group_id=$1 WHERE transcript_id=$2`, groups["pending"], id)
		assertAbsenteeRefused(t, ctx, tx, `DELETE FROM transcript_retraction_audiences WHERE transcript_id=$1`, id)
		if got := absenteeAudience(t, ctx, tx, id); !reflect.DeepEqual(got, want) {
			t.Fatalf("refused mutation changed audience: got %v want %v", got, want)
		}
	case "retraction-audit-event-unchanged", "retraction-audience-captured-on-owner-delete",
		"retraction-audience-captured-on-account-cascade", "retraction-audience-approved-shares-only",
		"retraction-audience-only-for-shared-visibility":
	default:
		t.Fatalf("unknown absentee-state case %q", name)
	}
}

func absenteeAudience(t *testing.T, ctx context.Context, tx pgx.Tx, id string) []string {
	t.Helper()
	rows, err := tx.Query(ctx, `SELECT group_id::text FROM transcript_retraction_audiences WHERE transcript_id=$1 ORDER BY group_id`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := []string{}
	for rows.Next() {
		var group string
		if err := rows.Scan(&group); err != nil {
			t.Fatal(err)
		}
		got = append(got, group)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

func mustAbsenteeExec(t *testing.T, ctx context.Context, tx pgx.Tx, query string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		t.Fatalf("absentee-state fixture statement failed: %v", err)
	}
}

func assertAbsenteeRefused(t *testing.T, ctx context.Context, tx pgx.Tx, query string, args ...any) {
	t.Helper()
	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Rollback(ctx)
	if _, err := sp.Exec(ctx, query, args...); err == nil {
		t.Fatalf("absentee-state fixture statement unexpectedly succeeded: %s", strings.SplitN(query, "\n", 2)[0])
	}
	if err := sp.Rollback(ctx); err != nil {
		t.Fatal(fmt.Errorf("rollback refused statement: %w", err))
	}
}
