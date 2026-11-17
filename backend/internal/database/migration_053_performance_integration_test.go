//go:build integration

package database

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type absenteeExplainNode struct {
	NodeType    string                `json:"Node Type"`
	IndexName   string                `json:"Index Name"`
	ActualRows  int                   `json:"Actual Rows"`
	Removed     int                   `json:"Rows Removed by Filter"`
	SharedHits  int                   `json:"Shared Hit Blocks"`
	SharedReads int                   `json:"Shared Read Blocks"`
	Children    []absenteeExplainNode `json:"Plans"`
}

type absenteeExplain struct {
	Plan absenteeExplainNode `json:"Plan"`
}

func (n absenteeExplainNode) findIndex(index string) *absenteeExplainNode {
	if n.IndexName == index {
		return &n
	}
	for _, child := range n.Children {
		if found := child.findIndex(index); found != nil {
			return found
		}
	}
	return nil
}

// This test protects the bounded tombstone and audience lookups: a unit test
// cannot observe PostgreSQL's planner, row visits or buffer accesses. It runs
// the production SQL shapes on the real schema without planner overrides.
// Its cost is bounded by the YAML seed sizes (100k audit rows, 20,200 shares);
// it launches no external process. newMigrationScratchDatabase creates a
// uniquely named disposable database and drops it with FORCE at cleanup;
// concurrent runs do not share it. Statements have a 60-second context budget;
// cleanup runs on panic or timeout, though SIGKILL can leave the uniquely named
// scratch database for manual removal. The integration-tagged command works from a
// clean checkout when TEST_DATABASE_URL supplies a CREATEDB-capable test user.
// EXPLAIN ANALYZE and the captured-row assertion observe real database effects,
// not a copied query implementation. Dropping either leading-key index, or
// replacing the indexed predicates with an unrestricted scan, makes the
// relevant plan assertion fail. Revisit or simplify this evidence if real
// production workload measurements supersede the synthetic scale; schema
// changes must retain a bounded lookup.
func TestMigration053RetractionPlans(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	f := loadAbsenteeStateFixtures(t).Performance
	if f.AuditRows < 100000 || f.Transcripts < 2 || f.Groups < 2 || f.MaxMatchingRows < f.Groups {
		t.Fatal("performance fixture must seed a selective, bounded workload")
	}
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	owner := insertFenceOwner(t, ctx, tx)
	mustAbsenteeExec(t, ctx, tx, `SELECT set_config('app.actor_id',$1,true), set_config('app.transcript_writer_version','1',true)`, owner)
	mustAbsenteeExec(t, ctx, tx, `INSERT INTO transcripts
		(owner_id, local_id, visibility, model_provider, blob_key, schema_version, project_hash, wrapped_data_key, encryption_algorithm, key_version)
		SELECT $1, gen_random_uuid()::text, 'shared', 'claude-code', 'transcripts/'||gen_random_uuid()::text||'.bin',
		       '1', 'c4e19a2f0b73', decode('01','hex'), 'aes-256-gcm-random-nonce-v1', 1
		FROM generate_series(1,$2)`, owner, f.Transcripts)
	var target string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM transcripts WHERE owner_id=$1 ORDER BY id LIMIT 1`, owner).Scan(&target); err != nil {
		t.Fatal(err)
	}
	mustAbsenteeExec(t, ctx, tx, `INSERT INTO groups (name,created_by)
		SELECT gen_random_uuid()::text,$1 FROM generate_series(1,$2)`, owner, f.Groups)
	// Seed the derived relation directly in this synthetic database to measure
	// the indexed read, not the cost of generating share-attempt history.
	mustAbsenteeExec(t, ctx, tx, `SELECT set_config('app.share_state_derivation','on',true)`)
	mustAbsenteeExec(t, ctx, tx, `INSERT INTO transcript_shares (transcript_id,group_id,status)
		SELECT t.id,g.id,'approved' FROM transcripts t CROSS JOIN groups g
		WHERE t.owner_id=$1 AND g.created_by=$1`, owner)
	mustAbsenteeExec(t, ctx, tx, `SELECT set_config('app.share_state_derivation','off',true)`)
	mustAbsenteeExec(t, ctx, tx, `INSERT INTO transcript_governance_events_audit
		(transcript_id,event_type,visibility,changed_by)
		SELECT gen_random_uuid(),'published','private',$1 FROM generate_series(1,$2)`, owner, f.AuditRows)
	mustAbsenteeExec(t, ctx, tx, `INSERT INTO transcript_governance_events_audit
		(transcript_id,event_type,visibility,changed_by) VALUES ($1,'retracted','shared',$2)`, target, owner)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE transcript_governance_events_audit; ANALYZE transcript_shares`); err != nil {
		t.Fatal(err)
	}

	checkPlan := func(label, sql, index string, maxRows int) {
		t.Helper()
		var raw []byte
		if err := pool.QueryRow(ctx, sql, target).Scan(&raw); err != nil {
			t.Fatalf("%s explain analyze: %v", label, err)
		}
		var explain []absenteeExplain
		if err := json.Unmarshal(raw, &explain); err != nil || len(explain) != 1 {
			t.Fatalf("%s explain JSON: %v", label, err)
		}
		node := explain[0].Plan.findIndex(index)
		if node == nil || node.ActualRows+node.Removed > maxRows {
			t.Fatalf("%s must use %s with <=%d index rows: %s", label, index, maxRows, raw)
		}
		t.Logf("%s EXPLAIN (ANALYZE, BUFFERS): %s", label, raw)
	}
	checkPlan("latest event", `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
		SELECT event_type,visibility FROM transcript_governance_events_audit
		WHERE transcript_id=$1 ORDER BY seq DESC LIMIT 1`, "idx_gov_events_transcript", 1)
	checkPlan("latest retracted", `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
		SELECT event_type,visibility FROM transcript_governance_events_audit
		WHERE transcript_id=$1 AND event_type='retracted' ORDER BY seq DESC LIMIT 1`, "idx_gov_events_transcript", 1)
	checkPlan("audience capture", `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
		INSERT INTO transcript_retraction_audiences (transcript_id,group_id)
		SELECT $1,s.group_id FROM transcript_shares s
		WHERE s.transcript_id=$1 AND s.status='approved' ON CONFLICT DO NOTHING`, "transcript_shares_pkey", f.MaxMatchingRows)
	var captured int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcript_retraction_audiences WHERE transcript_id=$1`, target).Scan(&captured); err != nil || captured != f.Groups {
		t.Fatalf("capture inserted %d audiences, want %d: %v", captured, f.Groups, err)
	}
}
