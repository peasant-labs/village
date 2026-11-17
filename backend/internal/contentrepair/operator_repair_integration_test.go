//go:build integration

package contentrepair

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/operator_repair.yaml
var operatorRepairFixtureYAML []byte

type operatorCase struct {
	Name              string   `yaml:"name"`
	Actions           []string `yaml:"actions"`
	Actors            []string `yaml:"actors"`
	AuthorizationRefs []struct {
		Value  string `yaml:"value"`
		Repeat int    `yaml:"repeat"`
	} `yaml:"authorization_refs"`
	ExpectedRevision int64    `yaml:"expected_revision"`
	FirstRuns        []string `yaml:"first_runs"`
	CommitFails      []string `yaml:"commit_fails"`
	CommitLands      []string `yaml:"commit_lands"`
	Outcomes         []string `yaml:"outcomes"`
	OutcomeFields    []string `yaml:"outcome_fields"`
}

const (
	// rowGone is the snapshot of a transcript that no longer exists.
	rowGone          = "no transcript row"
	restoreContent   = "synthetic preserved transcript"
	ledgerCommitText = "query transcript_content_repairs for this repair ID"
)

// operatorHarness is the one scratch database every case shares. A case that
// installs a trigger or renames the ledger restores it before it returns.
type operatorHarness struct {
	ctx  context.Context
	pool *pgxpool.Pool
	ring *config.TranscriptKeyring
}

func TestOperatorRepairRealPostgres(t *testing.T) {
	var fixture struct {
		Required []string       `yaml:"required"`
		Cases    []operatorCase `yaml:"cases"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(operatorRepairFixtureYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	h := &operatorHarness{ctx: context.Background(), pool: scratchDatabase(t)}
	scenarios := map[string]func(*testing.T, *operatorHarness, operatorCase){
		"operator-repair-refuses-reserved-actor":                     scenarioReservedActor,
		"operator-repair-refuses-unauthorized-run":                   scenarioUnauthorizedRun,
		"operator-repair-actor-matches-guc":                          scenarioActorMatchesGUC,
		"operator-repair-expect-revision-mismatch-refused":           scenarioExpectRevisionMismatch,
		"operator-repair-refuses-replayed-terminal":                  scenarioReplayedTerminal,
		"operator-repair-ambiguous-commit-resolved-by-ledger-read":   scenarioAmbiguousCommit,
		"operator-repair-attempt-without-terminal-is-unresolved":     scenarioAttemptWithoutTerminal,
		"operator-repair-reconciliation-cites-attempt":               scenarioReconciliationCitesAttempt,
		"operator-repair-reconciliation-terminal-clears-alert":       scenarioReconciliationTerminalClears,
		"operator-repair-reconciliation-attempt-does-not-clear":      scenarioReconciliationAttemptDoesNotClear,
		"operator-repair-reconciles-only-listed-attempt":             scenarioReconcilesOnlyListed,
		"operator-repair-writes-no-lineage":                          scenarioWritesNoLineage,
		"operator-repair-writes-no-governance-event":                 scenarioWritesNoGovernanceEvent,
		"operator-repair-fails-closed-without-audit-destination":     scenarioFailsClosedWithoutAudit,
		"operator-repair-log-omits-content-and-identity":             scenarioLogOmitsContentAndIdentity,
		"operator-repair-takes-publish-lock":                         scenarioTakesPublishLock,
		"operator-repair-delete-waits-for-publish-lock":              scenarioDeleteWaitsForPublishLock,
		"operator-repair-restore-transcript-deleted-before-terminal": scenarioRestoreDeletedBeforeTerminal,
		"operator-repair-account-cascade-during-restore":             scenarioAccountCascadeDuringRestore,
	}
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if seen[c.Name] {
			t.Fatalf("duplicate operator repair case %q", c.Name)
		}
		seen[c.Name] = true
		if scenarios[c.Name] == nil {
			t.Fatalf("operator repair case %q has no scenario", c.Name)
		}
	}
	for _, name := range fixture.Required {
		if !seen[name] {
			t.Fatalf("missing operator repair fixture case %q", name)
		}
	}
	for name := range scenarios {
		if !seen[name] {
			t.Fatalf("operator repair scenario %q has no fixture case", name)
		}
	}

	if err := database.RunMigrations(h.pool); err != nil {
		t.Fatal(err)
	}
	ring, err := config.ParseTranscriptKeyring("1", `{"1":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}`)
	if err != nil {
		t.Fatal(err)
	}
	h.ring = ring
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) { scenarios[c.Name](t, h, c) })
	}
}

// ---- the world one case runs against ---------------------------------------

type hookSlot struct{ fn func() }

// fire runs the hook once. The executor calls it while it holds its locks.
func (s *hookSlot) fire() {
	if s.fn != nil {
		fn := s.fn
		s.fn = nil
		fn()
	}
}

// repairWorld is one seeded transcript that the named action would repair.
type repairWorld struct {
	h       *operatorHarness
	t       *testing.T
	action  Action
	id      uuid.UUID
	owner   uuid.UUID
	localID string
	store   storage.TranscriptBlobStore
	objects *memoryObjects // restore only
	source  storage.ObjectKey
	hook    *hookSlot
	// secrets are content and identity values no log or error may carry.
	secrets []string
}

func parseAction(t *testing.T, kebab string) Action {
	t.Helper()
	action := Action(strings.ReplaceAll(kebab, "-", "_"))
	if !action.IsValid() {
		t.Fatalf("fixture action %q is unknown", kebab)
	}
	return action
}

// world seeds a transcript on which the action applies when run unmodified.
func (h *operatorHarness) world(t *testing.T, action Action) *repairWorld {
	t.Helper()
	w := &repairWorld{h: h, t: t, action: action, hook: &hookSlot{}}
	canonical := func(s string) string { return schema.ComputeTranscriptHash([]byte(s)) }
	stored, err := storage.NewContentIdentity(canonical("fixture"), 7)
	if err != nil {
		t.Fatal(err)
	}
	scripted := scriptedBlobStore{hook: w.hook.fire}
	switch action {
	case DeclareUnavailable:
		w.id = seedPresentTranscript(t, h.ctx, h.pool, stored, "transcripts/"+uuid.NewString()+".bin")
		scripted.err = storage.ErrObjectLookupUncertain
		w.store = scripted
		w.secrets = []string{string(stored.Hash())}
	case ClearMalformedIdentity:
		malformed := malformedHash
		w.id = seedTranscriptRow(t, h.ctx, h.pool, seedRow{blobKey: "transcripts/" + uuid.NewString() + ".bin", hash: &malformed})
		scripted.identity = stored
		w.store = scripted
		w.secrets = []string{string(stored.Hash())}
	case ReplaceThirdValue:
		third, err := storage.NewContentIdentity(canonical("third value"), 11)
		if err != nil {
			t.Fatal(err)
		}
		w.id = seedPresentTranscript(t, h.ctx, h.pool, stored, "transcripts/"+uuid.NewString()+".bin")
		scripted.identity = third
		w.store = scripted
		w.secrets = []string{string(stored.Hash()), string(third.Hash())}
	case RestoreAttestedGeneration:
		w.objects = &memoryObjects{objects: map[storage.ObjectKey]memoryObject{}}
		encrypted, err := storage.NewEncryptedTranscriptStore(w.objects, h.ring)
		if err != nil {
			t.Fatal(err)
		}
		id := uuid.New()
		descriptor, attested, err := encrypted.Write(h.ctx, id, []byte(restoreContent))
		if err != nil {
			t.Fatal(err)
		}
		preserved := w.objects.objects[descriptor.ObjectKey()]
		w.source = storage.ObjectKey("transcripts/" + uuid.NewString() + ".bin")
		w.objects.objects[w.source] = memoryObject{body: append([]byte(nil), preserved.body...), contentType: preserved.contentType}
		w.objects.objects[descriptor.ObjectKey()] = memoryObject{body: []byte("synthetic divergent ciphertext"), contentType: preserved.contentType}
		hash, size := string(attested.Hash()), attested.PlaintextSize()
		w.id = seedTranscriptRow(t, h.ctx, h.pool, seedRow{id: id, blobKey: string(descriptor.ObjectKey()), wrappedKey: descriptor.WrappedDEK(),
			keyVersion: int(descriptor.KeyVersion()), hash: &hash, size: &size})
		if _, err := h.pool.Exec(h.ctx, `UPDATE transcripts SET content_state='unavailable' WHERE id=$1`, w.id); err != nil {
			t.Fatal(err)
		}
		w.objects.puts = 0
		w.objects.beforePut = w.hook.fire
		w.store = restoreStore{EncryptedTranscriptStore: encrypted}
		w.secrets = []string{hash, restoreContent, string(w.source)}
	default:
		t.Fatalf("no world for action %q", action)
	}
	if err := h.pool.QueryRow(h.ctx, `SELECT owner_id, local_id FROM transcripts WHERE id=$1`, w.id).Scan(&w.owner, &w.localID); err != nil {
		t.Fatal(err)
	}
	return w
}

// opts is a valid, fresh repair for the world's action.
func (w *repairWorld) opts() Options {
	o := Options{RepairID: uuid.New(), TranscriptID: w.id, Action: w.action, ActorID: uuid.New(),
		AuthorizationRef: "ticket-operator-case", Reason: "operator repair case", ExpectedRevision: 1}
	if w.action == RestoreAttestedGeneration {
		o.SourceKey = w.source
	}
	return o
}

func (w *repairWorld) deps(pool *pgxpool.Pool) Deps {
	return Deps{Pool: pool, Queries: sqlc.New(pool), Blobs: w.store, Now: time.Now}
}

func (w *repairWorld) run(opts Options) (Outcome, error) { return w.runOn(w.h.pool, opts) }

func (w *repairWorld) runOn(pool *pgxpool.Pool, opts Options) (Outcome, error) {
	deps := w.deps(pool)
	switch w.action {
	case DeclareUnavailable:
		return Declare(w.h.ctx, deps, opts)
	case ClearMalformedIdentity:
		return Clear(w.h.ctx, deps, opts)
	case ReplaceThirdValue:
		return ReplaceThird(w.h.ctx, deps, opts)
	default:
		return Restore(w.h.ctx, deps, opts)
	}
}

func (w *repairWorld) mustApply(opts Options) Outcome {
	w.t.Helper()
	outcome, err := w.run(opts)
	if err != nil || outcome.Phase != Applied {
		w.t.Fatalf("%s: want an applied repair, got %+v err=%v", w.action, outcome, err)
	}
	return outcome
}

// puts is the number of object writes a restore made.
func (w *repairWorld) puts() int {
	if w.objects == nil {
		return 0
	}
	return w.objects.puts
}

// snapshot is every transcript column a repair may change, or rowGone.
func (w *repairWorld) snapshot() string {
	w.t.Helper()
	var state string
	var revision int64
	var hash, segHash *string
	var size, segSize *int64
	err := w.h.pool.QueryRow(w.h.ctx, `SELECT content_state, content_revision, content_hash, blob_size_bytes, segmented_identity_hash, segmented_identity_size FROM transcripts WHERE id=$1`, w.id).
		Scan(&state, &revision, &hash, &size, &segHash, &segSize)
	if errors.Is(err, pgx.ErrNoRows) {
		return rowGone
	}
	if err != nil {
		w.t.Fatal(err)
	}
	return fmt.Sprintf("%s|%d|%v|%v|%v|%v", state, revision, deref(hash), deref(size), deref(segHash), deref(segSize))
}

type ledgerRow struct {
	Phase      string
	Class      string
	Reconciles string
	Actor      uuid.UUID
}

func (w *repairWorld) ledger(repair uuid.UUID) []ledgerRow {
	w.t.Helper()
	rows, err := w.h.pool.Query(w.h.ctx, `SELECT phase, COALESCE(failure_class,''), COALESCE(reconciles_repair_id::text,''), operator_actor
		FROM transcript_content_repairs WHERE repair_id=$1 ORDER BY seq`, repair)
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	var out []ledgerRow
	for rows.Next() {
		var r ledgerRow
		if err := rows.Scan(&r.Phase, &r.Class, &r.Reconciles, &r.Actor); err != nil {
			w.t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		w.t.Fatal(err)
	}
	return out
}

// phases is the ledger read the runbook tells an operator to make.
func (w *repairWorld) phases(repair uuid.UUID) []string {
	w.t.Helper()
	rows, err := sqlc.New(w.h.pool).ListTranscriptContentRepairPhases(w.h.ctx, pgtype.UUID{Bytes: repair, Valid: true})
	if err != nil {
		w.t.Fatal(err)
	}
	out := []string{}
	for _, row := range rows {
		out = append(out, row.Phase)
	}
	return out
}

// unresolved lists this transcript's attempts the alert query returns.
func (w *repairWorld) unresolved(settle time.Duration) []uuid.UUID {
	w.t.Helper()
	rows, err := sqlc.New(w.h.pool).ListUnresolvedTranscriptContentRepairs(w.h.ctx, pgtype.Interval{Microseconds: settle.Microseconds(), Valid: true})
	if err != nil {
		w.t.Fatal(err)
	}
	var out []uuid.UUID
	for _, row := range rows {
		if uuid.UUID(row.TranscriptID.Bytes) == w.id {
			if row.Action != RestoreAttestedGeneration.String() || row.PriorRevision != 1 {
				w.t.Fatalf("alert row action=%s revision=%d, want a restore at revision 1", row.Action, row.PriorRevision)
			}
			out = append(out, uuid.UUID(row.RepairID.Bytes))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func sortedIDs(ids ...uuid.UUID) []uuid.UUID {
	out := append([]uuid.UUID(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func (w *repairWorld) lockKey() string {
	return database.SessionPublishLockKey(pgtype.UUID{Bytes: w.owner, Valid: true}, w.localID)
}

// lockHeld reports whether another session holds the transcript's publish lock.
func (w *repairWorld) lockHeld() bool {
	w.t.Helper()
	conn, err := w.h.pool.Acquire(w.h.ctx)
	if err != nil {
		w.t.Fatal(err)
	}
	defer conn.Release()
	var got bool
	if err := conn.QueryRow(w.h.ctx, "SELECT pg_try_advisory_lock(hashtextextended($1, 0))", w.lockKey()).Scan(&got); err != nil {
		w.t.Fatal(err)
	}
	if got {
		if _, err := conn.Exec(w.h.ctx, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", w.lockKey()); err != nil {
			w.t.Fatal(err)
		}
	}
	return !got
}

// advisoryWaiters counts sessions in this database waiting on an advisory lock.
func (w *repairWorld) advisoryWaiters() int {
	w.t.Helper()
	var n int
	if err := w.h.pool.QueryRow(w.h.ctx, `SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND NOT granted
		AND database=(SELECT oid FROM pg_database WHERE datname=current_database())`).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

// remove deletes rows the way an unlocked writer does: under the identity
// writer marker, attributed to the owner.
func (w *repairWorld) remove(query string, arg any) error {
	tx, err := w.h.pool.Begin(w.h.ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(w.h.ctx)
	if _, err = tx.Exec(w.h.ctx, "SELECT set_config('app.actor_id',$1,true), set_config('app.transcript_writer_version','1',true)", w.owner.String()); err != nil {
		return err
	}
	if _, err = tx.Exec(w.h.ctx, query, arg); err != nil {
		return err
	}
	return tx.Commit(w.h.ctx)
}

// ---- failure injection ------------------------------------------------------

// installSQL runs setup now and teardown at cleanup, and returns a function that
// runs teardown early. Teardown is idempotent.
func (h *operatorHarness) installSQL(t *testing.T, setup, teardown string) func() {
	t.Helper()
	exec := func(sql string) {
		if _, err := h.pool.Exec(h.ctx, sql, pgx.QueryExecModeSimpleProtocol); err != nil {
			t.Fatal(err)
		}
	}
	exec(teardown)
	exec(setup)
	removed := false
	remove := func() {
		if !removed {
			removed = true
			exec(teardown)
		}
	}
	t.Cleanup(remove)
	return remove
}

// failCommit makes every commit that inserted a ledger row fail at COMMIT.
func (h *operatorHarness) failCommit(t *testing.T) func() {
	return h.installSQL(t, `
CREATE FUNCTION test_fail_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic commit failure'; END $$;
CREATE CONSTRAINT TRIGGER test_fail_commit AFTER INSERT ON transcript_content_repairs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION test_fail_commit();`,
		`DROP TRIGGER IF EXISTS test_fail_commit ON transcript_content_repairs; DROP FUNCTION IF EXISTS test_fail_commit();`)
}

// blockTerminal rejects every terminal ledger row, so a restore that committed
// its attempt cannot record how it ended.
func (h *operatorHarness) blockTerminal(t *testing.T) func() {
	return h.installSQL(t, `
CREATE FUNCTION test_block_terminal() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
  IF NEW.phase <> 'attempted' THEN RAISE EXCEPTION 'synthetic terminal failure'; END IF; RETURN NEW; END $$;
CREATE TRIGGER test_block_terminal BEFORE INSERT ON transcript_content_repairs FOR EACH ROW EXECUTE FUNCTION test_block_terminal();`,
		`DROP TRIGGER IF EXISTS test_block_terminal ON transcript_content_repairs; DROP FUNCTION IF EXISTS test_block_terminal();`)
}

// lostCommitConn forwards a COMMIT, waits for the server to finish it, and then
// drops the response and the connection: the commit landed and the caller never
// hears it.
type lostCommitConn struct {
	net.Conn
	armed  *atomic.Bool
	broken atomic.Bool
}

func (c *lostCommitConn) Write(p []byte) (int, error) {
	if len(p) > 0 && p[0] == 'Q' && bytes.Contains(bytes.ToLower(p), []byte("commit")) && c.armed.CompareAndSwap(true, false) {
		n, err := c.Conn.Write(p)
		if err != nil {
			return n, err
		}
		var seen []byte
		buf := make([]byte, 4096)
		for !bytes.HasSuffix(seen, []byte{'Z', 0, 0, 0, 5, 'I'}) {
			m, err := c.Conn.Read(buf)
			seen = append(seen, buf[:m]...)
			if err != nil {
				return n, err
			}
		}
		c.broken.Store(true)
		_ = c.Conn.Close()
		return n, nil
	}
	return c.Conn.Write(p)
}

func (c *lostCommitConn) Read(b []byte) (int, error) {
	if c.broken.Load() {
		return 0, io.ErrUnexpectedEOF
	}
	return c.Conn.Read(b)
}

func (h *operatorHarness) lostCommitPool(t *testing.T, armed *atomic.Bool) *pgxpool.Pool {
	t.Helper()
	cfg := h.pool.Config().Copy()
	cfg.ConnConfig.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &lostCommitConn{Conn: conn, armed: armed}, nil
	}
	pool, err := pgxpool.NewWithConfig(h.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// ---- scenarios --------------------------------------------------------------

func validRaw(action string) RawOptions {
	raw := RawOptions{RepairID: uuid.NewString(), TranscriptID: uuid.NewString(), Action: action, ActorID: uuid.NewString(),
		AuthorizationRef: "ticket", Reason: "reason", ExpectedRevision: 1}
	if action == "restore-attested-generation" {
		raw.SourceSupplied = true
		raw.SourceKey = "transcripts/" + uuid.NewString() + ".bin"
	}
	return raw
}

// assertRejectedUnchanged runs a repair that must not be accepted and proves it
// changed nothing: no error-free outcome, the same row, no ledger row, no write.
func (w *repairWorld) assertRejectedUnchanged(opts Options, why string) {
	w.t.Helper()
	before, puts := w.snapshot(), w.puts()
	outcome, err := w.run(opts)
	if err == nil {
		w.t.Fatalf("%s %s: the executor accepted it with outcome %+v", w.action, why, outcome)
	}
	if after := w.snapshot(); after != before {
		w.t.Fatalf("%s %s: the transcript changed from %s to %s", w.action, why, before, after)
	}
	if rows := w.ledger(opts.RepairID); len(rows) != 0 {
		w.t.Fatalf("%s %s: ledger rows were written: %+v", w.action, why, rows)
	}
	if w.puts() != puts {
		w.t.Fatalf("%s %s: the object was written", w.action, why)
	}
}

func scenarioReservedActor(t *testing.T, h *operatorHarness, c operatorCase) {
	for _, actor := range c.Actors {
		for _, action := range c.Actions {
			raw := validRaw(action)
			if _, err := ParseOptions(raw); err != nil {
				t.Fatalf("the baseline options for %s were refused: %v", action, err)
			}
			raw.ActorID = actor
			if _, err := ParseOptions(raw); err == nil || !strings.Contains(err.Error(), "-repair-actor") {
				t.Fatalf("reserved actor %s for %s: want a -repair-actor refusal, got %v", actor, action, err)
			}
		}
	}
	// A nil actor that bypasses the parser is refused by the ledger itself.
	for _, action := range c.Actions {
		w := h.world(t, parseAction(t, action))
		opts := w.opts()
		opts.ActorID = uuid.Nil
		w.assertRejectedUnchanged(opts, "with the nil actor")
	}
}

func scenarioUnauthorizedRun(t *testing.T, h *operatorHarness, c operatorCase) {
	for _, ref := range c.AuthorizationRefs {
		value := ref.Value
		if ref.Repeat > 0 {
			value = strings.Repeat(ref.Value, ref.Repeat)
		}
		for _, action := range c.Actions {
			raw := validRaw(action)
			raw.AuthorizationRef = value
			if _, err := ParseOptions(raw); err == nil || !strings.Contains(err.Error(), "-repair-authorization-ref") {
				t.Fatalf("authorization %q (%d runes) for %s: want a -repair-authorization-ref refusal, got %v", ref.Value, len([]rune(value)), action, err)
			}
			w := h.world(t, parseAction(t, action))
			opts := w.opts()
			opts.AuthorizationRef = value
			w.assertRejectedUnchanged(opts, fmt.Sprintf("with a %d-rune authorization", len([]rune(value))))
		}
	}
}

func scenarioActorMatchesGUC(t *testing.T, h *operatorHarness, c operatorCase) {
	var first *repairWorld
	for _, action := range c.Actions {
		w := h.world(t, parseAction(t, action))
		first = w
		opts := w.opts()
		w.mustApply(opts)
		rows := w.ledger(opts.RepairID)
		if len(rows) == 0 {
			t.Fatalf("%s wrote no ledger row", action)
		}
		for _, row := range rows {
			if row.Actor != opts.ActorID || row.Actor == w.owner {
				t.Fatalf("%s ledger actor = %s, want the operator %s and not the owner %s", action, row.Actor, opts.ActorID, w.owner)
			}
		}
	}
	// The actor is derived from the transaction, never asserted by a statement.
	tx, err := h.pool.Begin(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(h.ctx)
	if _, err = tx.Exec(h.ctx, "SELECT set_config('app.actor_id',$1,true)", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(h.ctx, `INSERT INTO transcript_content_repairs (repair_id, phase, action, transcript_id, operator_actor, authorization_ref, reason, prior_revision, resulting_revision, failure_class)
		VALUES ($1,'refused','declare_unavailable',$2,$3,'a','r',1,1,'precondition_mismatch')`, uuid.New(), first.id, uuid.New())
	if err == nil || !strings.Contains(err.Error(), "disagrees with app.actor_id") {
		t.Fatalf("a statement-supplied actor that disagrees with the transaction actor was accepted: %v", err)
	}
}

func scenarioExpectRevisionMismatch(t *testing.T, h *operatorHarness, c operatorCase) {
	for _, action := range c.Actions {
		w := h.world(t, parseAction(t, action))
		before := w.snapshot()
		opts := w.opts()
		opts.ExpectedRevision = c.ExpectedRevision
		outcome, err := w.run(opts)
		if err != nil || outcome.Phase != Refused || outcome.FailureClass != PreconditionMismatch || outcome.ContentRevision != 1 {
			t.Fatalf("%s: want a refused precondition_mismatch at revision 1, got %+v err=%v", action, outcome, err)
		}
		if after := w.snapshot(); after != before {
			t.Fatalf("%s: a refused repair changed the transcript from %s to %s", action, before, after)
		}
		rows := w.ledger(opts.RepairID)
		if len(rows) != 1 || rows[0].Phase != Refused.String() || rows[0].Class != PreconditionMismatch.String() {
			t.Fatalf("%s: want one refused precondition_mismatch ledger row, got %+v", action, rows)
		}
		if w.puts() != 0 {
			t.Fatalf("%s: a refused repair wrote the object", action)
		}
	}
}

func scenarioReplayedTerminal(t *testing.T, h *operatorHarness, c operatorCase) {
	for _, first := range c.FirstRuns {
		for _, action := range c.Actions {
			w := h.world(t, parseAction(t, action))
			opts := w.opts()
			switch Phase(first) {
			case Applied:
			case Refused:
				opts.ExpectedRevision = 2
			default:
				t.Fatalf("fixture first run %q is unknown", first)
			}
			outcome, err := w.run(opts)
			if err != nil || outcome.Phase.String() != first {
				t.Fatalf("%s: first run want %s, got %+v err=%v", action, first, outcome, err)
			}
			before, ledger, puts := w.snapshot(), w.ledger(opts.RepairID), w.puts()
			replay := opts
			replay.ExpectedRevision = 1
			if outcome, err := w.run(replay); err == nil {
				t.Fatalf("%s after a %s run: a replayed repair ID was accepted with %+v", action, first, outcome)
			}
			if after := w.snapshot(); after != before {
				t.Fatalf("%s after a %s run: the replay changed the transcript from %s to %s", action, first, before, after)
			}
			if after := w.ledger(opts.RepairID); fmt.Sprint(after) != fmt.Sprint(ledger) {
				t.Fatalf("%s after a %s run: the replay changed the ledger from %+v to %+v", action, first, ledger, after)
			}
			if w.puts() != puts {
				t.Fatalf("%s after a %s run: the replay wrote the object", action, first)
			}
		}
	}
}

func scenarioAmbiguousCommit(t *testing.T, h *operatorHarness, c operatorCase) {
	// The server rejects the commit: the caller cannot tell, the ledger says no.
	for _, action := range c.CommitFails {
		w := h.world(t, parseAction(t, action))
		remove := h.failCommit(t)
		opts := w.opts()
		before := w.snapshot()
		_, err := w.run(opts)
		if err == nil || !strings.Contains(err.Error(), ledgerCommitText) {
			t.Fatalf("%s: want an error that sends the operator to the ledger, got %v", action, err)
		}
		if got := w.phases(opts.RepairID); len(got) != 0 {
			t.Fatalf("%s: a failed commit left ledger phases %v", action, got)
		}
		if after := w.snapshot(); after != before || w.puts() != 0 {
			t.Fatalf("%s: a failed commit left the transcript %s -> %s or wrote the object", action, before, after)
		}
		remove()
		// The ledger said nothing happened, so the same ID is safe to run again.
		w.mustApply(opts)
		want := []string{Applied.String()}
		if w.action == RestoreAttestedGeneration {
			want = []string{Attempted.String(), Applied.String()}
		}
		if got := w.phases(opts.RepairID); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s: retry ledger phases = %v, want %v", action, got, want)
		}
	}
	// The commit lands and its response is lost: the ledger says it happened.
	for _, action := range c.CommitLands {
		w := h.world(t, parseAction(t, action))
		armed := &atomic.Bool{}
		armed.Store(true)
		opts := w.opts()
		_, err := w.runOn(h.lostCommitPool(t, armed), opts)
		if err == nil || !strings.Contains(err.Error(), ledgerCommitText) {
			t.Fatalf("%s: a lost commit response must send the operator to the ledger, got %v", action, err)
		}
		if armed.Load() {
			t.Fatalf("%s: the commit was never intercepted, so the case proved nothing", action)
		}
		if got := w.phases(opts.RepairID); fmt.Sprint(got) != fmt.Sprint([]string{Applied.String()}) {
			t.Fatalf("%s: ledger phases after a landed commit = %v, want [applied]", action, got)
		}
		if !strings.HasPrefix(w.snapshot(), "unavailable|1|") {
			t.Fatalf("%s: the landed commit left the transcript %s", action, w.snapshot())
		}
		// The ledger decided it: it is applied, so the same ID is not run again.
		ledger, snapshot := w.ledger(opts.RepairID), w.snapshot()
		if _, err := w.run(opts); err == nil {
			t.Fatalf("%s: the same repair ID ran again after its commit landed", action)
		}
		if fmt.Sprint(w.ledger(opts.RepairID)) != fmt.Sprint(ledger) || w.snapshot() != snapshot {
			t.Fatalf("%s: rerunning a landed repair ID changed the ledger or the transcript", action)
		}
	}
}

// unresolvedAttempt runs a restore whose terminal row cannot be recorded, so its
// committed attempt has no outcome. The terminal block must already be installed.
func (w *repairWorld) unresolvedAttempt(cite uuid.UUID) (Options, error) {
	w.t.Helper()
	opts := w.opts()
	opts.Reconciles = cite
	_, err := w.run(opts)
	return opts, err
}

func (w *repairWorld) mustLeaveUnresolved(cite uuid.UUID) uuid.UUID {
	w.t.Helper()
	opts, err := w.unresolvedAttempt(cite)
	if err == nil || !strings.Contains(err.Error(), "unresolved") || !strings.Contains(err.Error(), opts.RepairID.String()) {
		w.t.Fatalf("a restore with no terminal row must report an unresolved attempt naming its repair ID, got %v", err)
	}
	return opts.RepairID
}

func scenarioAttemptWithoutTerminal(t *testing.T, h *operatorHarness, _ operatorCase) {
	w := h.world(t, RestoreAttestedGeneration)
	h.blockTerminal(t)
	before := w.snapshot()
	attempt := w.mustLeaveUnresolved(uuid.Nil)
	if got := w.phases(attempt); fmt.Sprint(got) != fmt.Sprint([]string{Attempted.String()}) {
		t.Fatalf("ledger phases = %v, want only the attempt", got)
	}
	if w.puts() != 1 {
		t.Fatalf("the object was written %d times, want 1: the attempt was committed before the write", w.puts())
	}
	if after := w.snapshot(); after != before {
		t.Fatalf("a restore with no terminal row changed the transcript from %s to %s", before, after)
	}
	if got := w.unresolved(0); fmt.Sprint(got) != fmt.Sprint([]uuid.UUID{attempt}) {
		t.Fatalf("alert query = %v, want the attempt %s", got, attempt)
	}
	// The settle window hides an attempt that may still be finishing.
	if got := w.unresolved(time.Hour); len(got) != 0 {
		t.Fatalf("alert query inside the settle window = %v, want nothing", got)
	}
}

func scenarioReconciliationCitesAttempt(t *testing.T, h *operatorHarness, _ operatorCase) {
	w := h.world(t, RestoreAttestedGeneration)
	remove := h.blockTerminal(t)
	attempt := w.mustLeaveUnresolved(uuid.Nil)
	remove()
	reconcile := w.opts()
	reconcile.Reconciles = attempt
	w.mustApply(reconcile)
	rows := w.ledger(reconcile.RepairID)
	if len(rows) != 2 || rows[0].Phase != Attempted.String() || rows[1].Phase != Applied.String() {
		t.Fatalf("reconciling repair ledger = %+v, want an attempt and an applied row", rows)
	}
	for _, row := range rows {
		if row.Reconciles != attempt.String() {
			t.Fatalf("reconciling %s row cites %q, want the unresolved attempt %s", row.Phase, row.Reconciles, attempt)
		}
	}
	// The cited attempt is never rewritten: a ledger row is append-only.
	if got := w.phases(attempt); fmt.Sprint(got) != fmt.Sprint([]string{Attempted.String()}) {
		t.Fatalf("the cited attempt's ledger phases changed to %v", got)
	}
	if !strings.HasPrefix(w.snapshot(), "present|1|") {
		t.Fatalf("the reconciling restore left the transcript %s", w.snapshot())
	}
}

func scenarioReconciliationTerminalClears(t *testing.T, h *operatorHarness, c operatorCase) {
	for _, outcome := range c.Outcomes {
		w := h.world(t, RestoreAttestedGeneration)
		remove := h.blockTerminal(t)
		attempt := w.mustLeaveUnresolved(uuid.Nil)
		remove()
		if got := w.unresolved(0); fmt.Sprint(got) != fmt.Sprint([]uuid.UUID{attempt}) {
			t.Fatalf("%s: alert query before reconciling = %v, want %s", outcome, got, attempt)
		}
		reconcile := w.opts()
		reconcile.Reconciles = attempt
		switch Phase(outcome) {
		case Applied:
		case Refused:
			reconcile.ExpectedRevision = 2
		default:
			t.Fatalf("fixture outcome %q is unknown", outcome)
		}
		got, err := w.run(reconcile)
		if err != nil || got.Phase.String() != outcome {
			t.Fatalf("%s: reconciling repair ended %+v err=%v", outcome, got, err)
		}
		if got := w.unresolved(0); len(got) != 0 {
			t.Fatalf("%s: a terminal row citing the attempt left the alert at %v", outcome, got)
		}
	}
}

func scenarioReconciliationAttemptDoesNotClear(t *testing.T, h *operatorHarness, _ operatorCase) {
	w := h.world(t, RestoreAttestedGeneration)
	h.blockTerminal(t)
	attempt := w.mustLeaveUnresolved(uuid.Nil)
	// The reconciling repair commits its own attempt but cannot finish.
	reconcile := w.mustLeaveUnresolved(attempt)
	rows := w.ledger(reconcile)
	if len(rows) != 1 || rows[0].Phase != Attempted.String() || rows[0].Reconciles != attempt.String() {
		t.Fatalf("reconciling attempt ledger = %+v, want one attempt citing %s", rows, attempt)
	}
	want := sortedIDs(attempt, reconcile)
	if got := w.unresolved(0); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("alert query = %v, want both unresolved attempts %v: an attempt that cites another resolves nothing", got, want)
	}
}

func scenarioReconcilesOnlyListed(t *testing.T, h *operatorHarness, _ operatorCase) {
	w := h.world(t, RestoreAttestedGeneration)
	remove := h.blockTerminal(t)
	listed := w.mustLeaveUnresolved(uuid.Nil)
	other := w.mustLeaveUnresolved(uuid.Nil)
	if got, want := w.unresolved(0), sortedIDs(listed, other); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("alert query = %v, want both attempts %v", got, want)
	}
	remove()
	reconcile := w.opts()
	reconcile.Reconciles = listed
	w.mustApply(reconcile)
	if got := w.unresolved(0); fmt.Sprint(got) != fmt.Sprint([]uuid.UUID{other}) {
		t.Fatalf("alert query = %v, want only the attempt the reconciling repair did not cite (%s)", got, other)
	}
}

// sideRecords counts the rows another subsystem owns that a repair must not add.
func (w *repairWorld) sideRecords() (rewrites, governance int) {
	w.t.Helper()
	if err := w.h.pool.QueryRow(w.h.ctx, `SELECT count(*) FROM transcript_content_rewrites WHERE transcript_id=$1`, w.id).Scan(&rewrites); err != nil {
		w.t.Fatal(err)
	}
	if err := w.h.pool.QueryRow(w.h.ctx, `SELECT count(*) FROM transcript_governance_events_audit WHERE transcript_id=$1`, w.id).Scan(&governance); err != nil {
		w.t.Fatal(err)
	}
	return rewrites, governance
}

func (w *repairWorld) runOutcome(outcome string) Outcome {
	w.t.Helper()
	opts := w.opts()
	switch Phase(outcome) {
	case Applied:
	case Refused:
		opts.ExpectedRevision = 2
	default:
		w.t.Fatalf("fixture outcome %q is unknown", outcome)
	}
	got, err := w.run(opts)
	if err != nil || got.Phase.String() != outcome {
		w.t.Fatalf("%s: want a %s repair, got %+v err=%v", w.action, outcome, got, err)
	}
	return got
}

func scenarioWritesNoLineage(t *testing.T, h *operatorHarness, c operatorCase) {
	for _, outcome := range c.Outcomes {
		for _, action := range c.Actions {
			w := h.world(t, parseAction(t, action))
			before, _ := w.sideRecords()
			w.runOutcome(outcome)
			if after, _ := w.sideRecords(); after != before {
				t.Fatalf("%s %s: the repair wrote %d rewrite lineage row(s)", action, outcome, after-before)
			}
		}
	}
}

func scenarioWritesNoGovernanceEvent(t *testing.T, h *operatorHarness, c operatorCase) {
	for _, outcome := range c.Outcomes {
		for _, action := range c.Actions {
			w := h.world(t, parseAction(t, action))
			_, before := w.sideRecords()
			w.runOutcome(outcome)
			if _, after := w.sideRecords(); after != before {
				t.Fatalf("%s %s: the repair wrote %d governance event(s)", action, outcome, after-before)
			}
		}
	}
}

func scenarioFailsClosedWithoutAudit(t *testing.T, h *operatorHarness, c operatorCase) {
	// A missing ledger is a precondition failure, never permission to proceed.
	for _, action := range c.Actions {
		w := h.world(t, parseAction(t, action))
		restore := h.installSQL(t, `ALTER TABLE transcript_content_repairs RENAME TO transcript_content_repairs_absent`,
			`DO $$ BEGIN IF to_regclass('transcript_content_repairs_absent') IS NOT NULL THEN
			   ALTER TABLE transcript_content_repairs_absent RENAME TO transcript_content_repairs; END IF; END $$`)
		before := w.snapshot()
		if outcome, err := w.run(w.opts()); err == nil {
			t.Fatalf("%s: a repair with no ledger returned %+v", action, outcome)
		}
		if after := w.snapshot(); after != before || w.puts() != 0 {
			t.Fatalf("%s: a repair with no ledger changed the transcript %s -> %s or wrote the object", action, before, after)
		}
		restore()
	}
}

// captureLogs routes the default logger to a buffer for the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return buf
}

func scenarioLogOmitsContentAndIdentity(t *testing.T, h *operatorHarness, c operatorCase) {
	var fields []string
	for i := 0; i < reflect.TypeOf(Outcome{}).NumField(); i++ {
		fields = append(fields, reflect.TypeOf(Outcome{}).Field(i).Name)
	}
	if fmt.Sprint(fields) != fmt.Sprint(c.OutcomeFields) {
		t.Fatalf("Outcome carries fields %v, the reviewed set is %v: a new field reaches the operator output", fields, c.OutcomeFields)
	}
	logs := captureLogs(t)
	for _, outcome := range c.Outcomes {
		for _, action := range c.Actions {
			w := h.world(t, parseAction(t, action))
			var text strings.Builder
			got := w.runOutcome(outcome)
			fmt.Fprintf(&text, "%+v\n", got)
			// A refusal and a replay are the paths that produce error text.
			replay := w.opts()
			replay.RepairID = got.RepairID
			if _, err := w.run(replay); err != nil {
				text.WriteString(err.Error())
			}
			text.WriteString(logs.String())
			for _, secret := range w.secrets {
				if strings.Contains(text.String(), secret) {
					t.Fatalf("%s %s: output carries %q", action, outcome, secret)
				}
			}
		}
	}
}

func scenarioTakesPublishLock(t *testing.T, h *operatorHarness, c operatorCase) {
	for _, action := range c.Actions {
		w := h.world(t, parseAction(t, action))
		var heldDuring bool
		w.hook.fn = func() { heldDuring = w.lockHeld() }
		w.mustApply(w.opts())
		if !heldDuring {
			t.Fatalf("%s: the publish lock was not held while the repair read or wrote the object", action)
		}
		if w.lockHeld() {
			t.Fatalf("%s: the publish lock leaked past the repair", action)
		}
	}
}

func scenarioDeleteWaitsForPublishLock(t *testing.T, h *operatorHarness, c operatorCase) {
	for _, action := range c.Actions {
		w := h.world(t, parseAction(t, action))
		opts := w.opts()
		deleted := make(chan error, 1)
		var deletedEarly bool
		w.hook.fn = func() {
			// A deletion takes the same lock before it touches the row.
			go func() {
				deleted <- database.WithSessionPublishLock(h.ctx, h.pool, pgtype.UUID{Bytes: w.owner, Valid: true}, w.localID, func(conn *pgxpool.Conn) error {
					tx, err := conn.Begin(h.ctx)
					if err != nil {
						return err
					}
					defer tx.Rollback(h.ctx)
					if _, err = tx.Exec(h.ctx, "SELECT set_config('app.actor_id',$1,true), set_config('app.transcript_writer_version','1',true)", w.owner.String()); err != nil {
						return err
					}
					if _, err = tx.Exec(h.ctx, `DELETE FROM transcripts WHERE id=$1`, w.id); err != nil {
						return err
					}
					return tx.Commit(h.ctx)
				})
			}()
			deadline := time.Now().Add(15 * time.Second)
			for w.advisoryWaiters() == 0 && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if w.advisoryWaiters() == 0 {
				t.Errorf("%s: the deletion never waited on the publish lock", action)
			}
			select {
			case <-deleted:
				deletedEarly = true
			case <-time.After(300 * time.Millisecond):
			}
		}
		// The repair finishes against a transcript that still exists: it is
		// applied, and its terminal row is written, before the deletion runs.
		got, err := w.run(opts)
		if err != nil || got.Phase != Applied {
			t.Fatalf("%s: the repair did not finish while the deletion waited: %+v err=%v", action, got, err)
		}
		if deletedEarly {
			t.Fatalf("%s: the deletion ran while the repair held the publish lock", action)
		}
		select {
		case err := <-deleted:
			if err != nil {
				t.Fatalf("%s: the deletion failed after the repair released the lock: %v", action, err)
			}
		case <-time.After(15 * time.Second):
			t.Fatalf("%s: the deletion never ran after the repair released the lock", action)
		}
		if w.snapshot() != rowGone {
			t.Fatalf("%s: the deletion did not remove the transcript", action)
		}
		if rows := w.ledger(opts.RepairID); len(rows) != 0 {
			t.Fatalf("%s: the transcript's ledger rows survived its deletion: %+v", action, rows)
		}
	}
}

// deletedDuringRestore runs a restore whose transcript is removed by an unlocked
// writer between the object write and the terminal row.
func deletedDuringRestore(t *testing.T, h *operatorHarness, remove func(w *repairWorld) error) {
	w := h.world(t, RestoreAttestedGeneration)
	opts := w.opts()
	w.hook.fn = func() {
		if err := remove(w); err != nil {
			t.Errorf("the concurrent removal failed: %v", err)
		}
	}
	outcome, err := w.run(opts)
	if err == nil || !strings.Contains(err.Error(), "unresolved") || !strings.Contains(err.Error(), opts.RepairID.String()) {
		t.Fatalf("a transcript removed before the terminal row must report an unresolved attempt naming its repair ID, got outcome %+v err=%v", outcome, err)
	}
	if w.puts() != 1 {
		t.Fatalf("the object was written %d times, want 1", w.puts())
	}
	if w.snapshot() != rowGone {
		t.Fatalf("the removed transcript is still present: %s", w.snapshot())
	}
	// The ledger rows went with the transcript, so no alert can be raised for a
	// transcript that no longer exists.
	if rows := w.ledger(opts.RepairID); len(rows) != 0 {
		t.Fatalf("ledger rows survived the transcript: %+v", rows)
	}
	if got := w.unresolved(0); len(got) != 0 {
		t.Fatalf("alert query lists %v for a removed transcript", got)
	}
	if w.lockHeld() {
		t.Fatal("the publish lock leaked after the transcript was removed")
	}
}

func scenarioRestoreDeletedBeforeTerminal(t *testing.T, h *operatorHarness, _ operatorCase) {
	deletedDuringRestore(t, h, func(w *repairWorld) error {
		return w.remove(`DELETE FROM transcripts WHERE id=$1`, w.id)
	})
}

func scenarioAccountCascadeDuringRestore(t *testing.T, h *operatorHarness, _ operatorCase) {
	var owner uuid.UUID
	deletedDuringRestore(t, h, func(w *repairWorld) error {
		owner = w.owner
		return w.remove(`DELETE FROM users WHERE id=$1`, w.owner)
	})
	var n int
	if err := h.pool.QueryRow(h.ctx, `SELECT count(*) FROM users WHERE id=$1`, owner).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the account cascade did not remove the owner: count=%d err=%v", n, err)
	}
}
