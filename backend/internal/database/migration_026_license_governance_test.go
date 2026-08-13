package database

// Migration 026 adds the per-transcript LEGAL axis (license) + the fail-closed,
// trigger-written, append-only governance audit — superseding the never-merged 025
// (both in-branch generations; see the 026 up.sql header).
//
// This file runs in the default `go test ./...` (no DB): it asserts 026 is
// registered under the right file and that the embedded up/down SQL has the
// expected GUARDED, ORDERED, FAIL-CLOSED shape. Registry-wide invariants
// (strictly increasing, highest == newest) live in migrations_registry_test.go.
// The behavioral assertions are build-tagged `integration`:
// migration_026_license_governance_integration_test.go and
// migration_026_convergence_integration_test.go.

import (
	"strings"
	"testing"
)

func TestMigration026_Registered(t *testing.T) {
	var found bool
	for _, m := range migrations {
		if m.version == 26 {
			found = true
			if m.file != "migrations/026_license_governance.up.sql" {
				t.Fatalf("migration 26 file = %q, want migrations/026_license_governance.up.sql", m.file)
			}
		}
		if m.version == 25 {
			t.Fatal("migration 25 must NOT be registered: its version number is burned (two in-branch content generations); 026 supersedes it")
		}
	}
	if !found {
		t.Fatal("migration version 26 is not registered in the migrations list")
	}
}

func TestMigration026_SQL(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/026_license_governance.up.sql")
	if err != nil {
		t.Fatalf("read 026 up migration: %v", err)
	}
	upSQL := strings.ToLower(string(up))

	// The closed three-tier license menu must be seeded (identity vs
	// schema.AllLicenses is pinned by the integration drift guard).
	for _, id := range []string{"cc0-1.0", "cc-by-4.0", "cc-by-sa-4.0"} {
		if !strings.Contains(upSQL, id) {
			t.Fatalf("026 up migration must seed license %q", id)
		}
	}
	// transcripts.license_id must be NULLABLE (NULL = legacy/unset).
	if strings.Contains(upSQL, "license_id text not null") {
		t.Fatal("026 transcripts.license_id must be NULLABLE")
	}
	// No scalar permissiveness order: collective resolution is decided+consented,
	// not computed (Plan UAT); gen-2 envs get the column dropped.
	if !strings.Contains(upSQL, "drop column if exists permissiveness_rank") {
		t.Fatal("026 must DROP COLUMN IF EXISTS permissiveness_rank (gen-2 env repair)")
	}
	if strings.Contains(upSQL, "permissiveness_rank integer") {
		t.Fatal("026 must not (re)create permissiveness_rank")
	}

	// Governance events order by a monotonic seq IDENTITY.
	if !strings.Contains(upSQL, "generated always as identity") {
		t.Fatal("026 governance audit must have a monotonic seq IDENTITY column")
	}
	if strings.Contains(upSQL, "unique (transcript_id, effective_at)") {
		t.Fatal("026 must NOT have UNIQUE(transcript_id, effective_at); seq replaces it")
	}

	// FIXPOINT + CONVERGENCE shape: guarded DDL, and the gen-1 table dropped
	// BEFORE the audit index is created — index names are schema-wide, so the
	// old-generation drop must free idx_gov_events_transcript first or the
	// guarded CREATE INDEX silently skips on old-025 envs.
	dropOld := strings.Index(upSQL, "drop table if exists transcript_governance_events;")
	mkIndex := strings.Index(upSQL, "create index if not exists idx_gov_events_transcript")
	if dropOld == -1 {
		t.Fatal("026 must DROP TABLE IF EXISTS transcript_governance_events (gen-1 reconciliation)")
	}
	if mkIndex == -1 {
		t.Fatal("026 must create idx_gov_events_transcript with IF NOT EXISTS")
	}
	if dropOld > mkIndex {
		t.Fatal("026 ordering bug: gen-1 DROP TABLE must precede the audit CREATE INDEX (schema-wide index namespace)")
	}
	for _, want := range []string{
		"create table if not exists licenses",
		"create table if not exists governance_event_types",
		"create table if not exists transcript_governance_events_audit",
		"add column if not exists license_id",
	} {
		if !strings.Contains(upSQL, want) {
			t.Fatalf("026 up migration must contain guarded DDL %q", want)
		}
	}
	// CREATE TRIGGER has no IF NOT EXISTS — every trigger must DROP IF EXISTS
	// first for fixpoint re-application.
	if got := strings.Count(upSQL, "drop trigger if exists"); got != strings.Count(upSQL, "create trigger") {
		t.Fatalf("every CREATE TRIGGER needs a preceding DROP TRIGGER IF EXISTS: %d drops vs %d creates",
			got, strings.Count(upSQL, "create trigger"))
	}

	// FAIL-CLOSED attribution (Plan UAT): no owner fallback anywhere; both
	// mutation-side functions must raise on a missing actor. And all trigger
	// functions pin search_path.
	if strings.Contains(upSQL, "old.owner_id") || strings.Contains(upSQL, "new.owner_id") {
		t.Fatal("026 triggers must be FAIL-CLOSED: no owner_id attribution fallback")
	}
	if got := strings.Count(upSQL, "governance audit requires app.actor_id (fail-closed)"); got < 2 {
		t.Fatalf("both audit trigger functions must RAISE the fail-closed error; found %d", got)
	}
	if got := strings.Count(upSQL, "set search_path = pg_catalog, public"); got != 3 {
		t.Fatalf("all 3 trigger functions must pin search_path: found %d", got)
	}
	// Append-only enforcement with the sanctioned, txn-scoped escape.
	if !strings.Contains(upSQL, "before update or delete on transcript_governance_events_audit") {
		t.Fatal("026 must install the append-only block trigger on the audit table")
	}
	if !strings.Contains(upSQL, "app.audit_maintenance") {
		t.Fatal("026 block trigger must honor the app.audit_maintenance escape GUC")
	}

	down, err := migrationsFS.ReadFile("migrations/026_license_governance.down.sql")
	if err != nil {
		t.Fatalf("read 026 down migration: %v", err)
	}
	downSQL := strings.ToLower(string(down))
	for _, want := range []string{
		"drop trigger if exists trg_governance_audit_immutable",
		"drop trigger if exists trg_audit_transcript_retract",
		"drop trigger if exists trg_audit_transcript_governance",
		"drop trigger if exists trg_audit_transcript_publish",
		"drop table if exists transcript_governance_events_audit",
		"drop column if exists license_id",
		"drop table if exists governance_event_types",
		"drop table if exists licenses",
	} {
		if !strings.Contains(downSQL, want) {
			t.Fatalf("026 down migration must contain %q", want)
		}
	}
}
