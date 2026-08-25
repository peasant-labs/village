//go:build integration

package database

import (
	"bytes"
	"context"
	_ "embed"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"
)

// These cases run against a real PostgreSQL because every one of them is a
// property of the database, not of Go: a share that existed before the attempt
// model must survive as its first attempt, and the writer fence must refuse
// each of the three verbs it claims to cover. A Go test computing its own
// expected values would pass with no trigger installed at all.

// TestShareAttemptBackfillPreservesExistingShares proves a share published
// before the attempt model becomes attempt 1 carrying its own status, so
// counters are correct for pre-existing data from the first deploy.
func TestShareAttemptBackfillPreservesExistingShares(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	migrateTestDatabaseThrough(t, pool, migrationBoundary035)

	owner := insertShareFixtureOwner(t, ctx, pool)
	group := insertShareFixtureGroup(t, ctx, pool, owner)

	// One share per status the pre-attempt table could hold.
	existing := map[string]string{}
	for _, status := range []string{"pending", "approved", "rejected"} {
		transcript := insertShareFixtureTranscript(t, ctx, pool, owner)
		existing[status] = transcript
		if _, err := pool.Exec(ctx,
			`INSERT INTO transcript_shares (transcript_id, group_id, status) VALUES ($1,$2,$3)`,
			transcript, group, status); err != nil {
			t.Fatalf("seed a pre-attempt share in state %q: %v", status, err)
		}
	}

	if err := runMigration(pool, requireMigrationVersion(t, 36)); err != nil {
		t.Fatalf("apply the share-attempt migration over the boundary before it: %v", err)
	}

	for status, transcript := range existing {
		var attemptNo int
		var backfilled string
		if err := pool.QueryRow(ctx,
			`SELECT event_num, status FROM transcript_share_attempts WHERE transcript_id=$1 AND group_id=$2`,
			transcript, group).Scan(&attemptNo, &backfilled); err != nil {
			t.Fatalf("read back the backfilled attempt for a %q share: %v", status, err)
		}
		if attemptNo != 1 || backfilled != status {
			t.Fatalf("a pre-existing %q share became attempt %d in state %q, want attempt 1 in state %q; "+
				"pre-attempt history would otherwise be lost or misreported", status, attemptNo, backfilled, status)
		}
		// The current-state row must be untouched by the backfill: it was
		// already correct, and the derivation has nothing to change.
		var derived string
		if err := pool.QueryRow(ctx,
			`SELECT status FROM transcript_shares WHERE transcript_id=$1 AND group_id=$2`,
			transcript, group).Scan(&derived); err != nil {
			t.Fatalf("read back the current-state row for a %q share: %v", status, err)
		}
		if derived != status {
			t.Fatalf("the backfill changed a %q share's current-state row to %q", status, derived)
		}
	}
}

// TestTranscriptSharesWriterFenceRefusesEveryVerb proves the fail-closed guard
// against a direct INSERT, a direct UPDATE and a direct DELETE separately. A
// single-verb proof would pass a trigger accidentally scoped to that one verb.
func TestTranscriptSharesWriterFenceRefusesEveryVerb(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)

	owner := insertShareFixtureOwner(t, ctx, pool)
	group := insertShareFixtureGroup(t, ctx, pool, owner)
	transcript := insertShareFixtureTranscript(t, ctx, pool, owner)

	// A row to attack with UPDATE and DELETE, produced the only sanctioned
	// way: by opening an attempt and letting the derivation write it.
	if _, err := pool.Exec(ctx,
		`INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status) VALUES ($1,$2,1,'approved')`,
		transcript, group); err != nil {
		t.Fatalf("open the attempt whose derivation produces the current-state row: %v", err)
	}

	other := insertShareFixtureTranscript(t, ctx, pool, owner)
	fencePairs := map[string]sharePair{
		"derived_pair": {transcript: transcript, group: group},
		"unused_pair":  {transcript: other, group: group},
	}
	// One verb per subtest, so a failure names the verb structurally as well as
	// in its message. Each probe is executed and asserted on its own: a fence
	// scoped to one verb must fail the other two, not be masked by them.
	for _, probe := range loadShareWriterFenceProbes(t, fencePairs) {
		t.Run(probe.Name, func(t *testing.T) {
			pair := fencePairs[probe.Target]
			_, err := pool.Exec(ctx, probe.Statement, pair.transcript, pair.group)
			if err == nil {
				t.Fatalf("a direct %s on transcript_shares SUCCEEDED (%s). The writer fence must refuse all three verbs "+
					"independently; a guard scoped to only some of them leaves the derived row writable behind the ledger.",
					probe.Verb, probe.Why)
			}
			if !strings.Contains(err.Error(), "transcript_shares is derived, not written") {
				t.Fatalf("a direct %s was refused by %v, which is not the writer fence; the refusal must name what to write "+
					"instead", probe.Verb, err)
			}
		})
	}

	// The one non-derivation write that must still succeed: a foreign-key
	// cascade. Deleting the transcript removes its share rows with no
	// application code in the loop, and blocking that would block account
	// deletion.
	if _, err := pool.Exec(ctx, `SELECT set_config('app.actor_id',$1,true)`, SystemActorID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.actor_id',$1,true), set_config('app.transcript_writer_version','1',true)`, SystemActorID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM transcripts WHERE id=$1`, transcript); err != nil {
		t.Fatalf("deleting a transcript must still cascade its share rows away, but it was refused: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the cascade proof: %v", err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcript_shares WHERE transcript_id=$1`, transcript).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("the cascade left %d share rows behind", remaining)
	}
}

func insertShareFixtureOwner(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	handle := "share-fixture-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (github_id, github_username, provider_user_id) VALUES ($1,$2,$3) RETURNING id::text`,
		int64(uuid.New().ID()), handle, handle).Scan(&id); err != nil {
		t.Fatalf("insert share fixture owner: %v", err)
	}
	return id
}

func insertShareFixtureGroup(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO groups (name, created_by) VALUES ($1,$2) RETURNING id::text`,
		"share-fixture-"+uuid.NewString(), owner).Scan(&id); err != nil {
		t.Fatalf("insert share fixture collective: %v", err)
	}
	return id
}

// insertShareFixtureTranscript writes a transcript the way production does:
// inside one transaction carrying the actor and writer-version markers the
// audit and encryption fences require.
func insertShareFixtureTranscript(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner string) string {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.actor_id',$1,true), set_config('app.transcript_writer_version','1',true)`, SystemActorID); err != nil {
		t.Fatal(err)
	}
	local := uuid.NewString()
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO transcripts (owner_id, local_id, visibility, model_provider, blob_key, schema_version,
		                         wrapped_data_key, encryption_algorithm, key_version, project_hash)
		VALUES ($1,$2,'shared','claude-code',$3,'1',decode('01','hex'),'aes-256-gcm-random-nonce-v1',1,$4)
		RETURNING id::text`, owner, local, "transcripts/"+local+".bin", "a1b2c3d4e5f6").Scan(&id); err != nil {
		t.Fatalf("insert share fixture transcript: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestShareProjectionRebuildsFromTheLedger is the test that makes the
// reconstruction guarantee real rather than aspirational. Keeping a derived
// table was accepted on the understanding that it can always be rebuilt from
// the ledger; an untested rebuild would make that a claim rather than a
// property.
//
// It also proves the consistency check can go RED - a checker nobody has seen
// fail is not evidence - by corrupting the projection in each of the three ways
// it can actually diverge, and then proving the rebuild repairs all of them.
func TestShareProjectionRebuildsFromTheLedger(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)

	owner := insertShareFixtureOwner(t, ctx, pool)
	group := insertShareFixtureGroup(t, ctx, pool, owner)

	// A ledger covering every state, including a pair whose latest event is
	// terminal (so the projection must hold NO row for it) and a pair with a
	// multi-event history (so "latest wins" is actually exercised).
	live := map[string]string{}
	for _, status := range []string{"pending", "approved", "rejected"} {
		transcript := insertShareFixtureTranscript(t, ctx, pool, owner)
		live[status] = transcript
		mustExec(t, ctx, pool, `INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status) VALUES ($1,$2,1,$3)`, transcript, group, status)
	}
	withdrawn := insertShareFixtureTranscript(t, ctx, pool, owner)
	mustExec(t, ctx, pool, `INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status) VALUES ($1,$2,1,'approved')`, withdrawn, group)
	mustExec(t, ctx, pool, `INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status) VALUES ($1,$2,2,'retracted')`, withdrawn, group)
	resubmitted := insertShareFixtureTranscript(t, ctx, pool, owner)
	mustExec(t, ctx, pool, `INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status) VALUES ($1,$2,1,'rejected')`, resubmitted, group)
	mustExec(t, ctx, pool, `INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status) VALUES ($1,$2,2,'approved')`, resubmitted, group)

	assertNoShareDrift(t, ctx, pool, "after building the ledger through the derivation")
	before := snapshotProjection(t, ctx, pool)
	// A pair whose latest event is representable holds a current-state row; a
	// pair whose latest event is terminal holds none. Both directions are
	// asserted, because a projection that kept every pair and one that kept none
	// would each satisfy only half of this.
	for state, transcript := range live {
		if _, present := before[transcript+"|"+group]; !present {
			t.Fatalf("the %s pair has no current-state row; a pair whose latest event is representable must have one", state)
		}
	}
	if _, present := before[resubmitted+"|"+group]; !present {
		t.Fatal("the resubmitted pair has no current-state row; its latest event is an acceptance and must be projected")
	}
	if _, present := before[withdrawn+"|"+group]; present {
		t.Fatal("the withdrawn pair has a current-state row; a pair whose latest event is terminal must have none")
	}
	wantProjected := int64(len(before))

	// Each corruption is a way the projection can actually diverge, and each
	// must be both DETECTED and REPAIRED. The cases live in a fixture, not in a
	// table here, so adding a new way to diverge is a fixture row rather than a
	// code change. Writing the projection requires the derivation flag - the GUC
	// is context-passing, not a credential, which is what lets a test stand in
	// for a corrupting bug.
	pairs := map[string]sharePair{
		"approved_pair":  {transcript: live["approved"], group: group},
		"pending_pair":   {transcript: live["pending"], group: group},
		"rejected_pair":  {transcript: live["rejected"], group: group},
		"withdrawn_pair": {transcript: withdrawn, group: group},
	}
	for _, corruption := range loadShareProjectionCorruptions(t, pairs) {
		t.Run(corruption.Name, func(t *testing.T) {
			pair := pairs[corruption.Target]
			corruptProjection(t, ctx, pool, corruption.Statement, pair.transcript, pair.group)

			var drift int64
			if err := pool.QueryRow(ctx, `SELECT check_transcript_shares_drift()`).Scan(&drift); err != nil {
				t.Fatal(err)
			}
			if drift == 0 {
				t.Fatalf("the consistency check reports zero drift after %s (%s); a check that cannot see this corruption "+
					"cannot see a real one either", corruption.Name, corruption.Why)
			}
			var problem string
			if err := pool.QueryRow(ctx, `SELECT problem FROM transcript_share_drift LIMIT 1`).Scan(&problem); err != nil {
				t.Fatal(err)
			}
			if problem != corruption.WantProblem {
				t.Errorf("drift classified as %q, want %q; an operator reading the report needs to know WHICH way it diverged",
					problem, corruption.WantProblem)
			}

			var installed int64
			if err := pool.QueryRow(ctx, `SELECT rebuild_transcript_shares()`).Scan(&installed); err != nil {
				t.Fatalf("rebuilding the projection from the ledger failed: %v", err)
			}
			if installed != wantProjected {
				t.Errorf("rebuild installed %d rows, want %d - the same set the derivation produced", installed, wantProjected)
			}
			assertNoShareDrift(t, ctx, pool, "after rebuilding from the ledger")
			if got := snapshotProjection(t, ctx, pool); !projectionsEqual(got, before) {
				t.Fatalf("the rebuilt projection differs from the one the derivation produced.\n got: %v\nwant: %v", got, before)
			}
		})
	}
}

// sharePair is one (transcript, collective) the fixture can aim a corruption at.
type sharePair struct{ transcript, group string }

// shareProjectionCorruption is one fixture row: a way the projection can
// diverge from the ledger, and the classification the drift view must report.
type shareProjectionCorruption struct {
	Name        string `yaml:"name"`
	Why         string `yaml:"why"`
	Target      string `yaml:"target"`
	Statement   string `yaml:"statement"`
	WantProblem string `yaml:"want_problem"`
}

//go:embed testdata/share_projection/corruptions.yaml
var shareProjectionCorruptionsYAML []byte

// requiredShareProjectionCorruptions names the divergences that must stay
// covered. Each is here because losing it would leave a real way for the
// projection to drift with nothing watching for it.
var requiredShareProjectionCorruptions = []string{
	"status_no_longer_matches_the_latest_event",
	"current_state_row_went_missing",
	"current_state_row_with_nothing_behind_it",
	"shared_at_drifted_from_its_event",
}

// shareDriftClassifications is the closed set transcript_share_drift can
// report. A fixture naming anything else is describing a divergence the view
// does not know how to classify.
var shareDriftClassifications = map[string]bool{
	"missing_from_projection": true,
	"absent_from_ledger":      true,
	"status_mismatch":         true,
	"shared_at_mismatch":      true,
}

func loadShareProjectionCorruptions(t *testing.T, pairs map[string]sharePair) []shareProjectionCorruption {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(shareProjectionCorruptionsYAML))
	decoder.KnownFields(true)
	var cases []shareProjectionCorruption
	if err := decoder.Decode(&cases); err != nil {
		t.Fatalf("decode the projection-corruption fixture: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("the projection-corruption fixture must be exactly one YAML document; found a second: %v", trailing)
	}

	present := map[string]bool{}
	for _, testCase := range cases {
		if present[testCase.Name] {
			t.Fatalf("the projection-corruption fixture repeats case %q", testCase.Name)
		}
		present[testCase.Name] = true
		if testCase.Why == "" {
			t.Fatalf("case %q states no reason it exists; a case nobody can justify cannot be maintained", testCase.Name)
		}
		if _, known := pairs[testCase.Target]; !known {
			t.Fatalf("case %q aims at target %q, which this test does not set up; the corruption would hit nothing", testCase.Name, testCase.Target)
		}
		if !shareDriftClassifications[testCase.WantProblem] {
			t.Fatalf("case %q expects classification %q, which transcript_share_drift cannot report; teach the view that "+
				"divergence before asserting it", testCase.Name, testCase.WantProblem)
		}
		// Both placeholders must be present, or the statement is not actually
		// bound to the pair the case claims to corrupt and could silently
		// corrupt nothing at all.
		if !strings.Contains(testCase.Statement, "$1") || !strings.Contains(testCase.Statement, "$2") {
			t.Fatalf("case %q has a statement that does not bind both $1 (transcript) and $2 (collective), so it is not "+
				"aimed at its target pair: %s", testCase.Name, testCase.Statement)
		}
	}
	for _, required := range requiredShareProjectionCorruptions {
		if !present[required] {
			t.Fatalf("the projection-corruption fixture no longer covers %q. That divergence exists in the real system; "+
				"restore the case rather than removing it from this manifest.", required)
		}
	}
	return cases
}

func mustExec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, statement string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, statement, args...); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}

// corruptProjection writes transcript_shares directly, which only the
// derivation may normally do. It stands in for a bug that corrupts the
// projection, so the check and the rebuild are proven against real damage.
func corruptProjection(t *testing.T, ctx context.Context, pool *pgxpool.Pool, statement string, args ...any) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.share_state_derivation','on',true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, statement, args...); err != nil {
		t.Fatalf("corrupt the projection (%s): %v", statement, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func snapshotProjection(t *testing.T, ctx context.Context, pool *pgxpool.Pool) map[string]string {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT transcript_id::text, group_id::text, status, shared_at::text FROM transcript_shares`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	snapshot := map[string]string{}
	for rows.Next() {
		var transcript, group, status, sharedAt string
		if err := rows.Scan(&transcript, &group, &status, &sharedAt); err != nil {
			t.Fatal(err)
		}
		snapshot[transcript+"|"+group] = status + "@" + sharedAt
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func projectionsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func assertNoShareDrift(t *testing.T, ctx context.Context, pool *pgxpool.Pool, when string) {
	t.Helper()
	var drift int64
	if err := pool.QueryRow(ctx, `SELECT check_transcript_shares_drift()`).Scan(&drift); err != nil {
		t.Fatalf("evaluate the drift check %s: %v", when, err)
	}
	if drift != 0 {
		var problem, stored, expected string
		_ = pool.QueryRow(ctx, `SELECT problem, COALESCE(stored_status,'<none>'), COALESCE(expected_status,'<none>') FROM transcript_share_drift LIMIT 1`).Scan(&problem, &stored, &expected)
		t.Fatalf("the projection disagrees with the ledger in %d row(s) %s; first is %s (stored %s, expected %s)", drift, when, problem, stored, expected)
	}
}

// shareWriterFenceProbe is one fixture row: a single verb aimed at the derived
// projection, which the fail-closed fence must refuse on its own.
type shareWriterFenceProbe struct {
	Name      string `yaml:"name"`
	Verb      string `yaml:"verb"`
	Why       string `yaml:"why"`
	Target    string `yaml:"target"`
	Statement string `yaml:"statement"`
}

//go:embed testdata/share_writer_fence/probes.yaml
var shareWriterFenceProbesYAML []byte

// requiredShareWriterFenceVerbs is exactly what
// BEFORE INSERT OR UPDATE OR DELETE claims to cover. Every verb must be probed
// separately, because a fence narrowed to one of them would otherwise pass.
var requiredShareWriterFenceVerbs = []string{"INSERT", "UPDATE", "DELETE"}

func loadShareWriterFenceProbes(t *testing.T, pairs map[string]sharePair) []shareWriterFenceProbe {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(shareWriterFenceProbesYAML))
	decoder.KnownFields(true)
	var probes []shareWriterFenceProbe
	if err := decoder.Decode(&probes); err != nil {
		t.Fatalf("decode the writer-fence probe fixture: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("the writer-fence probe fixture must be exactly one YAML document; found a second: %v", trailing)
	}

	byVerb := map[string]string{}
	for _, probe := range probes {
		if probe.Why == "" {
			t.Fatalf("probe %q states no reason it exists; a case nobody can justify cannot be maintained", probe.Name)
		}
		if existing, repeated := byVerb[probe.Verb]; repeated {
			t.Fatalf("probes %q and %q both cover verb %s; each verb is proved once, on its own", existing, probe.Name, probe.Verb)
		}
		byVerb[probe.Verb] = probe.Name
		if _, known := pairs[probe.Target]; !known {
			t.Fatalf("probe %q aims at target %q, which this test does not set up; the write would hit nothing", probe.Name, probe.Target)
		}
		// The statement must actually perform the verb it claims, or a probe
		// could report a refusal of some other operation entirely.
		if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(probe.Statement)), probe.Verb) {
			t.Fatalf("probe %q claims verb %s but its statement does not begin with it: %s", probe.Name, probe.Verb, probe.Statement)
		}
		if !strings.Contains(probe.Statement, "$1") || !strings.Contains(probe.Statement, "$2") {
			t.Fatalf("probe %q has a statement that does not bind both $1 (transcript) and $2 (collective), so it is not "+
				"aimed at its target pair: %s", probe.Name, probe.Statement)
		}
	}
	for _, verb := range requiredShareWriterFenceVerbs {
		if byVerb[verb] == "" {
			t.Fatalf("the writer-fence probe fixture no longer covers %s. The fence claims to refuse INSERT, UPDATE and "+
				"DELETE; dropping a verb is exactly how a fence narrowed to the others would pass unnoticed.", verb)
		}
	}
	return probes
}
