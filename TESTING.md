# Testing Patterns - Village Backend

Code examples and strategies for testing the village backend (Go HTTP API over
PostgreSQL + S3/MinIO). See [`AGENTS.md`](AGENTS.md) for the test package map,
the run-commands cheat sheet, the backend layout, and the cross-repo contract
table - this file holds the **patterns, strategy, and worked examples** that
those tables point at, and does not repeat them.

All Go commands run from `backend/` (the repo is a `go.work` workspace →
`use ./backend`, Go 1.25.5). `-race` is mandatory everywhere: CI runs every
suite under the race detector.

## Test documentation map

| Layer | Build tag | Needs Postgres? | Runs in CI as | Where it's covered |
|-------|-----------|-----------------|---------------|--------------------|
| Unit - handlers, auth, scanner, storage, fixtures | none | no | `go test -race ./...` | [Unit tests](#unit-tests-mockquerier--httptest) below |
| Integration - migrations, sqlc, real-Postgres handler flows | `//go:build integration` | yes (`TEST_DATABASE_URL`) | `go test -tags=integration -race ./...` (after a migrate step) | [Integration tests](#integration-tests-real-postgres) below |
| Pull skip-gate (batch currency probe) | `//go:build integration` | yes | `-tags=integration -race` | [New families](#pull-skip-gate-publish-idempotency-and-startup-backfill-families) below |
| Source-keyed publish idempotency | `//go:build integration` | yes | `-tags=integration -race` | [New families](#pull-skip-gate-publish-idempotency-and-startup-backfill-families) below |
| Explicit content-identity backfill | `//go:build integration` | yes | `-tags=integration -race` | Run with `server -backfill-content-identity`; startup does not backfill |
| Migration + sqlc conventions | mixed | the `_integration_test.go` variants do | both of the above | [Migration & sqlc](#migration--sqlc-test-conventions) below |
| Authoritative publication ordering | `//go:build integration` for persistence/order | PostgreSQL + S3-compatible storage | integration `-race` | Publish/PATCH/share locking, audited private-before-replacement, complete association receipts, and fingerprint currency |
| Cross-repo contract (village ↔ schema module ↔ peasant) | none | no | unit | [Cross-repo contract](#cross-repo-contract-tests--gate-faithful-expectations) below |
| Governance rules (fail-closed fixtures, append-only teardown, drift guards, convergence) | mixed | mostly yes | both | [Governance testing](#governance-testing-migration-026) below |
| Test performance (measured levers, template cache) | - | - | - | [Test performance](#test-performance-measured) below |

Database/data-model invariants the suites rely on (triggers, GUCs, audit table,
migration rules) live in [`docs/database-invariants.md`](docs/database-invariants.md).
The cross-system encryption threat model and lifecycle invariants are canonical
in [`docs/transcript-storage-security.md`](docs/transcript-storage-security.md).
The production Railway PostgreSQL and Cloudflare R2 procedure is separate from
local test infrastructure and lives in
[`docs/railway-cloudflare-r2-activation.md`](docs/railway-cloudflare-r2-activation.md).

For the real local aggregate, run `make backend-encrypted-test` from the root.
It creates a uniquely named disposable Compose project, initializes MinIO, and
runs the source-discovered integration packages through the same structured
event checker that rejects top-level and nested test skips. The deterministic
test KEK is scoped to that command and is not production custody.
The CI gate (`.github/workflows/backend-tests.yml`) is **build → unit `-race` → migrate →
integration `-tags=integration -race`** against a Postgres service container. The
unit suite is the only thing that runs with no infrastructure - the integration
suite `t.Skipf`s gracefully when Postgres is unreachable (see below), so a plain
`go test -race ./...` always works on a laptop with no DB.

CI also provisions MinIO and exports the `TEST_S3_*` variables. Authoritative
publication integration tests mount the real handlers over migrated PostgreSQL
and that real S3-compatible service. They fail closed when `CI` is set but the
storage endpoint is absent; an explicit local skip remains available only when
the developer has not requested the infrastructure-backed integration gate.

## Unit tests (`mockQuerier` + `httptest`)

Unit tests run the **real handler** against a **mocked dependency**. The handler
under test is production code; only its collaborators (the sqlc `Querier` and the
S3 uploader) are mocked. This is the core rule - *mock the dependency, not the
subject*.

### The `mockQuerier` DI mock

`Handler` depends on a narrow `Querier` interface (`internal/handler/querier.go`,
a hand-curated subset of the generated `sqlc.Queries` - the methods handlers
actually call), with a compile-time guard that the real implementation satisfies it:

```go
// querier.go: the production sqlc.Queries must satisfy the interface
var _ Querier = (*sqlc.Queries)(nil)
```

The test mock (`internal/handler/auth_test.go`) is a struct of optional function
pointers, one per stubbed method, plus a matching compile-time guard:

```go
// auth_test.go
type mockQuerier struct {
    insertCLISession     func(ctx context.Context, arg sqlc.InsertCLISessionParams) (pgtype.UUID, error)
    getCLISessionByState func(ctx context.Context, oauthState string) (sqlc.CliAuthSession, error)
    // ... one field per method the handlers exercise
}

var _ Querier = (*mockQuerier)(nil)

func (m *mockQuerier) InsertCLISession(ctx context.Context, arg sqlc.InsertCLISessionParams) (pgtype.UUID, error) {
    if m.insertCLISession != nil {
        return m.insertCLISession(ctx, arg)
    }
    panic("InsertCLISession: not stubbed") // fails loudly on an unexpected call
}
```

Two stub styles coexist, deliberately:

- **Panic-on-nil** for the methods a test *expects* to be called - if the handler
  reaches a method the test didn't wire, the test fails loudly instead of
  silently passing on a zero value.
- **Fixed zero-value returns** for methods that exist on `Querier` but are
  irrelevant to the flow under test (e.g. an auth-only test doesn't care about
  transcript queries).

Don't assume *every* method panics; only the function-pointer fields do.

### Building a handler for a test

Helpers in `auth_test.go` assemble a production `Handler` with the mock(s)
injected (the `pool` field stays nil in unit tests - no DB is opened):

```go
func minimalConfig() *config.Config {
    return &config.Config{
        BaseURL:     "https://example.com",
        FrontendURL: "https://app.example.com",
        JWTSecret:   "test-jwt-secret-unused-in-these-tests",
    }
}

func newTestHandler(q Querier, blobs storage.TranscriptBlobStore) *Handler {
    return &Handler{cfg: minimalConfig(), queries: q, blobs: blobs}
}
```

Tests inject a `storage.TranscriptBlobStore`, normally
`mockTranscriptBlobStore`; storage integration tests compose the real S3 object
store, key custodian, and `EncryptedTranscriptStore`.

### `httptest` with a real handler - worked example

Tests call handler methods directly (no router), recording the response with
`httptest.NewRecorder`. Chi route params and the authenticated-user context are
injected with small helpers - `withChiURLParam` (`annotations_test.go`),
`withTestUser` / `withUserID` (`transcripts_test.go`, `pull_test.go`):

```go
func TestCLIExchange_Success(t *testing.T) {
    // Mock ONLY the DB; the Handler itself is the real production code.
    q := &mockQuerier{
        exchangeCLISession: func(ctx context.Context, arg sqlc.ExchangeCLISessionParams) (sqlc.CliAuthSession, error) {
            return session, nil
        },
        createAPIKey: func(ctx context.Context, arg sqlc.CreateAPIKeyParams) (sqlc.ApiKey, error) {
            if arg.Label.String != "peasant-cli" {
                t.Fatalf("unexpected label %q", arg.Label.String)
            }
            return createdKey, nil
        },
    }

    h := newTestHandler(q, nil)             // real Handler, mock DB, no S3 needed
    w := httptest.NewRecorder()
    r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/cli/exchange", body)

    h.CLIExchange(w, r)                      // REAL handler method runs

    if w.Code != http.StatusOK {
        t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
    }
}
```

Error-body decoding uses the shared `decodeError` helper, which expects the
`{"error": "..."}` shape the handlers emit:

```go
func decodeError(t *testing.T, body []byte) string {
    var resp map[string]string
    if err := json.Unmarshal(body, &resp); err != nil {
        t.Fatalf("failed to decode error body %q: %v", body, err)
    }
    return resp["error"]
}
```

PgType conversion helpers (`pgUUIDFrom`, `pgText`) and fixture constructors
(`publicTranscript()`, `privateTranscript()`, `pullTestTranscript(...)`) live
alongside the mocks in the same package - reuse them rather than re-deriving
`pgtype` values inline.

## Integration tests (real Postgres)

Integration tests carry `//go:build integration` and hit a real PostgreSQL. They
read the database URL from `TEST_DATABASE_URL`, falling back to a hardcoded local
DSN per file when the env var is empty:

```go
func pullTestDatabaseURL(t *testing.T) string {
    t.Helper()
    if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
        return url
    }
    return "postgres://test:test@localhost:55432/village_test?sslmode=disable"
}
```

The fallback DSN differs by file - newer handler tests
(`pull_integration_test.go`, `annotations_push_integration_test.go`,
`cmd/village-setup-demo/setup_integration_test.go`) default to
`postgres://test:test@localhost:55432/village_test`; the database and
migration-era tests (`commit_atomicity_integration_test.go`,
`migration_023_backfill_test.go`, `internal/database/sqlc/cli_sessions_test.go`)
default to `postgres://peasant:peasant@localhost:5432/peasant_test`. **CI sets
`TEST_DATABASE_URL` explicitly to
`postgres://test:test@localhost:5432/village_test?sslmode=disable`**, so set it
yourself to match CI rather than relying on any file's fallback.

### Share attempts: fixtures write attempts, never the derived share row

`transcript_shares` is DERIVED. A database trigger maintains it from
`transcript_share_attempts`, and a second trigger refuses any other write, so a
fixture that inserts a share row directly now fails outright. Write the attempt
and let the derivation produce the row:

```go
pool.Exec(ctx, `
    INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status)
    VALUES ($1, $2, 1, $3)`, transcript, group, status)
```

That is also what the dev seed scripts do, so seeded data exercises the real
path. The lattice itself lives in
`internal/handler/share_attempts_integration_test.go` +
`testdata/share-attempts.yaml`, driven through the real HTTP handlers: it has to
be a real database, because a Go test computing its own expected values would
pass with no trigger installed at all, and because decisions and withdrawals are
UPDATEs, a trigger narrowed to INSERT would keep a mock-backed assertion green
while propagating nothing. The migration-level proofs - the backfill of
pre-attempt shares, and the writer fence refusing INSERT, UPDATE and DELETE
separately - are in
`internal/database/migration_036_share_attempts_integration_test.go`.

The projection must always be reconstructible from the ledger, so
`TestShareProjectionRebuildsFromTheLedger` corrupts `transcript_shares` in each
of the four ways it can diverge, proves `check_transcript_shares_drift()` goes
RED for each and classifies it correctly, and proves
`rebuild_transcript_shares()` restores exactly what the derivation produced.
Corrupting the projection needs `app.share_state_derivation` set, which is how a
test stands in for a corrupting bug. Every lifecycle case additionally asserts
the WHOLE projection still equals a latest-event fold, so a transition that
damaged some other pair cannot pass unseen.

A third guard needs no database at all: `query_write_fence_test.go` parses every
statement in `queries/*.sql`, works out what each one writes, and checks it
against the closed inventory in
`queries/testdata/transcript-shares-statements.yaml`. Adding any statement that
touches `transcript_shares` - a JOIN read included - fails until it is declared
there, and it can only be declared as a read. `ListShareAttempts` itself is
outside that inventory: it selects only from `transcript_share_attempts` (the
ledger) and never mentions `transcript_shares` (the derived projection), so the
guard's word-boundary match on the table name does not fire for it and it needs
no entry there.

**`transcripts.project_hash` is NOT NULL**, so every fixture that inserts a
transcript with raw SQL must name one. Fixtures for the same project share a
hash; unrelated fixtures must not collide.

### Share-event history: the owner-facing read over the same ledger

`internal/handler/share_event_history_integration_test.go` +
`testdata/share-event-history.yaml` cover
`GET /api/v1/users/me/collectives/{groupId}/transcripts/{transcriptId}/events`,
the owner-only endpoint that reads `ListShareAttempts` back out as a numbered
history. It reuses `shareWorld` from `share_attempts_integration_test.go` to
drive the same submit/decide/unshare/remove steps and build a genuine attempt
ledger, then calls `ListShareEventHistory` directly as three viewers: the
transcript's owner, a different signed-in member of the same collective, and no
authenticated caller at all. The fixture asserts a mixed five-state history
returns in `event_num` ascending order, that a still-pending event carries no
`decided_at` and no actor, that `decided_by_actor` renders only the closed class
`owner | collective | moderator` and never a raw user id, and that both
non-owner shapes come back `404` rather than `403` so the response never
confirms the transcript exists. Like the share-attempt lattice above, this has
to run against a real database: the actor class and the ordering both come from
data the derivation and the ledger's own `ORDER BY` produce, not from anything
the Go test computes itself. It carries `//go:build integration` and runs
under `make backend-encrypted-test` alongside the rest of this file's real-Postgres
suites.

### Pull, skip-gate, publish-idempotency, and explicit-backfill families

Newer real-Postgres families worth knowing when touching those paths:

- **Skip-gate** (`pull_skip_gate_integration_test.go`), the batch currency probe:
  `TestPullSkipGate_CurrencyMatrix` (fixture-driven `contentCurrent` /
  `annotationsCurrent` pos+neg), `TestPullSkipGate_PullScopingOmission` (a
  non-pullable other-owner id is absent from the response `Results`; the
  byte-level shape non-leak is the schema module's own test),
  `TestPullSkipGate_OwnerScopedAnnotations` (another owner's annotations for the
  same session never move the answer), and `TestPullSkipGate_CrossOwnerNoOracle`
  (a caller's answer is byte-identical whether or not another owner published
  identical content).
- **Source-keyed publish idempotency**
  (`publish_idempotency_regression_integration_test.go`): publish identity is the
  SOURCE (owner + `local_id`, enforced by `UNIQUE(owner_id, local_id)`);
  `content_hash` is a value-only column and never an identity key.
  `TestPublishSourceKeyedIdempotency` (same `local_id` re-push reuses the row; a
  distinct `local_id` with identical content is a separate fork) and
  `TestPublishForksSurvive_NoContentKeyedIdempotency` (two byte-identical
  publishes under distinct `local_id`s both persist as separate rows carrying the
  same non-null `content_hash`, proving the ABSENCE of content-keyed dedup). These
  are a regression lock on source-keyed identity, not a constraint test.
- **Explicit content identity repair** (`internal/backfill`): the listener-free
  `-backfill-content-identity` mode composes the real encrypted blob store and
  processes pending rows. Normal startup never launches this job.
- **Explicit title repair core** (`internal/backfill`): strict `dry-run` and
  `apply` modes use bounded UUID keyset pages. Dry-run executes the complete
  sanitation and legacy-content decision path without issuing an update. Apply
  changes both title fields atomically only when both prior values and
  `updated_at` still match; a concurrent owner edit wins. The integration suite
  uses real PostgreSQL and the production migrator and title pipeline while
  controlling only encrypted object-storage reads. Row failures continue and
  produce a final nonzero result without logging title, object, path, remote, or
  identity content.

  Operators run the listener-free maintenance path explicitly:

  ```bash
  cd backend
  go run ./cmd/server -backfill-titles=dry-run
  go run ./cmd/server -backfill-titles=apply
  ```

  Run dry-run first and inspect the seven safe summary counters: `scanned`,
  `unchanged`, `would_update`, `updated`, `derived`, `sanitized`, and `failed`.
  Apply has no count acknowledgement or manifest. A failed row remains unchanged,
  later rows still run, and the process exits nonzero only after the full scan.
  Correct the stage named by the safe row log and rerun. Neither mode rewrites S3;
  both require the normal PostgreSQL, encrypted-blob, and KEK authority needed to
  authenticate historical content.

- **Session-origin classification and scope** (`internal/sessionorigin`,
  `internal/backfill`, `internal/handler`): one classifier decides who drove a
  session, and every surface derives from it.
  - `internal/sessionorigin/testdata/classification/cases.yaml` is the rule
    itself: a real user prompt; a command invocation on the system role, on the
    user role, and carrying attributes; the teammate-driven worker shape; a
    system-only session; a command-only session; an empty payload; unreadable
    content; a whitespace-only user turn; a prompt found late in the turn order;
    tool work with no assistant turns; a command wrapper quoted mid-turn; and an
    unlisted wrapper whose name merely starts with a recognized one. The loader
    pins the row count and requires every named arm. The two long production
    shapes differ only in the one turn that says who started the session, so the
    row that classifies `user` cannot pass for another reason.
  - `internal/handler/testdata/session_origin_publish/cases.yaml` pins what
    publish stores, including the fail-safe value for content that cannot be
    decoded, plus a re-publish that replaces the class when the content changes.
    These run against the real handler with a mocked querier, so the assertion
    is the exact value passed to `CreateTranscript` / `UpdateTranscriptByOwnerAndLocalID`.
  - `internal/handler/testdata/session_origin_discovery/cases.yaml` pins the
    discovery scope over real PostgreSQL: the default list hides agent-driven
    sessions for anonymous and owner callers, an explicit `origin` returns
    exactly that class, unclassified rows list like user rows, `agent_total`
    follows the same filters, and an unsupported value is a 400. A companion
    test opens a hidden agent transcript by direct link and requires 200 — the
    scope is discovery, never access control.
  - `internal/backfill/testdata/origin_backfill/cases.yaml` runs
    `-backfill-origins` end to end: dry-run writes nothing, apply installs the
    decision, a rerun is a no-op, a read failure leaves the row untouched, and a
    concurrent write wins the compare-and-set.

  ```bash
  cd backend
  go run ./cmd/server -backfill-origins=dry-run
  go run ./cmd/server -backfill-origins=apply
  ```

  Inspect the five safe summary counters: `scanned`, `unchanged`,
  `would_update`, `updated`, and `failed`. A failed row remains unchanged and
  stays fully visible, later rows still run, and the process exits nonzero only
  after the full scan.

### Migrate first, then run

Each integration test calls `database.RunMigrations(pool)` itself and migrations
are idempotent, but some tests assume an already-migrated DB. So migrate once up
front, exactly as CI does:

```bash
export TEST_DATABASE_URL="postgres://test:test@localhost:5432/village_test?sslmode=disable"

cd backend && DATABASE_URL="$TEST_DATABASE_URL" go run ./cmd/server -migrate-only

cd backend && go test -tags=integration -race ./...
```

`-migrate-only` applies every registered migration and exits without starting the
HTTP listener (`cmd/server/main.go`). Migrate-only requires database authority,
not JWT, S3, or KEK authority.

### Skip-when-unreachable

Some individual integration tests skip when Postgres is absent, which keeps
focused laptop runs possible. The aggregate `make backend-encrypted-test` rejects
every skip event, so unavailable PostgreSQL or MinIO cannot appear green.
The pool-based tests skip on both pool creation and ping failure:

```go
pool, err := pgxpool.New(ctx, pullTestDatabaseURL(t))
if err != nil {
    t.Skipf("cannot create pool (%v); set TEST_DATABASE_URL", err)
}
defer pool.Close()
if err := pool.Ping(ctx); err != nil {
    t.Skipf("cannot reach test database (%v); set TEST_DATABASE_URL", err)
}
```

(`internal/database/sqlc/cli_sessions_test.go` opens a single connection with
`pgx.Connect` instead of a pool, so it skips only on the connect failure - there
is no separate ping there.) Tests that gate on `-short` also `t.Skip` in short
mode.

### Hermetic cleanup via FK cascade

Tests insert real rows with hardcoded identities (e.g. fixed GitHub IDs like
`980001`, `690001`, `924001`) and clean up by **deleting the owning user** -
`ON DELETE CASCADE` then removes every dependent row (transcripts → shares,
commits; groups → members; api_keys; annotations). The cascade chain is rooted at
`users(id)`:

```
users
 ├─ transcripts (owner_id … ON DELETE CASCADE)
 │   ├─ transcript_shares            (cascade only; the writer fence lets it through)
 │   ├─ transcript_share_attempts
 │   └─ transcript_commits
 ├─ groups (created_by …)
 │   ├─ group_members
 │   ├─ transcript_shares (group_id …)
 │   └─ transcript_share_attempts (group_id …)
 ├─ api_keys
 └─ annotations
```

```go
defer func() {
    _, _ = pool.Exec(ctx, `DELETE FROM users WHERE github_id = $1`, testGitHubID)
    // cascades clean shares / commits / groups / members / keys / annotations
}()
```

Two cleanup styles are in use, both hermetic:

- **Delete-owning-user-in-defer** - e.g. `annotations_push`,
  `setup_integration` (owners with no transcripts). Suites whose fixtures own
  transcripts use the `cleanupOwners` variant instead (`pull`,
  `commit_atomicity`, `groups_counts`, `license_roundtrip`,
  `list_license_parity`, the governance suites) - see the migration-026
  cleanup requirements below.
- **Transaction rollback** - `migration_024_content_hash_integration_test.go`
  and `internal/database/sqlc/cli_sessions_test.go` wrap each test in a
  transaction and roll back on `t.Cleanup`, so nothing ever commits:

```go
func newTestQueries(t *testing.T) (*Queries, pgx.Tx) {
    // ... conn, _ := pgx.Connect(ctx, testDatabaseURL(t)) ...
    tx, err := conn.Begin(ctx)
    // ...
    t.Cleanup(func() {
        _ = tx.Rollback(ctx)
        conn.Close(ctx)
    })
    return New(tx), tx
}
```

These suites are **not** idempotent across runs against an un-cleaned DB: the
hardcoded identities collide on `UNIQUE` constraints (e.g. `github_id`). The
cleanup is what keeps reruns green - don't remove it, and don't disable it.

**Migration-026 cleanup requirement - deleting only the owning user is no longer enough** for
tests that own transcripts, for two reasons: the `BEFORE DELETE` retract trigger
is **fail-closed** (a bare `DELETE FROM users` whose cascade touches transcripts
aborts without `app.actor_id`), and the audit table has **no FK** (cascades never
clean it) while being **append-only** (a bare `DELETE` on it is blocked). The
canonical order lives in `internal/handler/integration_helpers_test.go`:

```go
defer cleanupOwners(t, ctx, pool, owner)
// = collect the owners' transcript ids
//   → delete transcripts AS THE SYSTEM ACTOR (execAsSystem: one txn, SET LOCAL app.actor_id)
//   → delete users (no transcripts remain ⇒ the cascade fires no trigger)
//   → purgeAuditRows LAST (the deletes above just re-appended 'retracted' rows;
//     single txn with SET LOCAL app.audit_maintenance='on'; errors are t.Errorf-loud)
```

A test that deletes all its transcripts in-body must still purge the audit rows
for the ids it captured (`defer purgeAuditRows(t, ctx, pool, ids)`) - the
cleanup helper finds nothing left to collect and orphan audit rows accumulate.

## Migration & sqlc test conventions

### Migrations

Migrations are embedded SQL (`//go:embed migrations/*.sql`) in
`internal/database/migrate.go`, registered in an ordered slice of
`{version, file}` and applied by `RunMigrations(pool *pgxpool.Pool) error`. Each
migration acquires the Village advisory transaction lock, checks its exact
version, executes its body, and inserts its registry row in one bounded pgx
transaction. A commit error is treated as ambiguous and a retry rechecks the
exact version under the same lock; migration files never provide their own
transaction control. A
`schema_migrations(version INTEGER PRIMARY KEY, applied_at TIMESTAMPTZ)` table
tracks what has run; each migration is checked before exec, so `RunMigrations`
is idempotent. Files are paired `NNN_name.up.sql` / `NNN_name.down.sql`. The
latest registered version is **032** (`032_authoritative_publication_receipts`);
the next new migration is **033**. Versions 19 and 25 are intentionally absent
from the registry and must not be reused. See `docs/database-invariants.md` §1.

**Registry-wide invariants live in ONE central test** -
`migrations_registry_test.go` pins strictly-increasing versions and
`highest == wantLatestMigration`; bump that const in the same commit as a new
migration. This is deliberate: making each newest migration's test assert "I am
the highest" forced editing the PRIOR migration's test on
every new migration, violating the no-retrofit rule and dropping an assertion
in transit once.

A new migration `NNN` gets its **own** `migration_NNN_*_test.go`. Do not retrofit
prior migration tests. Migration 024 illustrates the two-file pattern:

- `migration_024_content_hash_test.go` - **no build tag**, runs in the unit
  suite. It reads the embedded `.up.sql` / `.down.sql` from `migrationsFS` and
  asserts structure with string checks (`ADD COLUMN content_hash`,
  `DROP COLUMN content_hash`), and asserts its own registration (version 024
  present - the registry-wide strictly-increasing/highest checks live in the
  central `migrations_registry_test.go`, per above). These need no DB.
- `migration_024_content_hash_integration_test.go` - `//go:build integration`.
  It calls `RunMigrations(pool)`, opens a transaction, and asserts real schema
  state (column present, `is_nullable = YES`, insert-NULL default, update/read
  round-trip).

The structure-test/integration-test split lets the cheap registration + SQL-shape
checks run on every `go test` while the live-DB assertions stay behind the tag.
Migration 023's integration test additionally proves a full up/down round-trip,
idempotence, and value backfill (`claude` → `claude-code`, `gemini` → `gemini-cli`).

### sqlc

Queries are authored in `internal/database/queries/*.sql` (with
`-- name: Fn :one|:many|:exec` markers) and generated into
`internal/database/sqlc/` per `backend/sqlc.yaml` (`package sqlc`, `pgx/v5`,
`emit_json_tags`, `emit_db_tags`, `emit_empty_slices`). Regenerate with `sqlc generate` from
`backend/` (where `sqlc.yaml` resolves) after editing a query or a migration the
queries read. **Never hand-edit the generated files** (`*.sql.go`, `models.go`,
`db.go` - each carries `// Code generated by sqlc. DO NOT EDIT.`).

The generated query layer is exercised through `internal/database/sqlc`'s own
`//go:build integration` tests (e.g. `cli_sessions_test.go`), which use the
transaction-rollback helper above and assert nullable columns via the `pgtype`
`.Valid` field.

## Governance testing (migration 026)

Migration 026 made the audit **trigger-written, fail-closed, and append-only**.
That changes how tests must be written; the rules below are load-bearing.

### Fixture writes need an actor

Every `INSERT` into `transcripts` fires the publish trigger (it has **no WHEN
clause**), and every `DELETE` fires retract - both fail-closed on
`app.actor_id`. Fixture helpers declare the SYSTEM actor:

- `internal/database`: `insertTranscript` issues
  `SELECT set_config('app.actor_id', SystemActorID, true)` **on the caller's
  transaction** - never a helper-owned Begin/Commit, because migration tests
  run one rolled-back outer tx for isolation (a self-contained txn would leak
  rows and FK-fail against the uncommitted owner).
- `internal/handler`: `govStore` (publish path via `inTxAs`),
  `pullInsertTranscript` / `countsInsertTranscript` (own txn + system actor),
  `execAsSystem` (teardown).

`UPDATE`s only need an actor when they MOVE a governance axis
(license/visibility): title-only and content_hash-only updates are WHEN-false
and fire nothing.

### Must-fail statements go in savepoints

An expected error aborts the whole Postgres transaction, so wrap it:
`sp, _ := tx.Begin(ctx); _, err = sp.Exec(ctx, …); sp.Rollback(ctx)` - then
assert on `err`. And a fail-closed **UPDATE** test must actually change the
axis: new transcripts default to `visibility='private'`, so a
`SET visibility='private'` is WHEN-false and would pass for the wrong reason.

### Append-only means teardown uses the escape

`purgeAuditRows` is the ONLY sanctioned audit cleaner: one explicit transaction
carrying `SET LOCAL app.audit_maintenance = 'on'` + the `DELETE` - `SET LOCAL`
outside a transaction is a no-op, and pgxpool routes separate `Exec`s to
arbitrary connections, so the GUC and the statement MUST share a tx. It doubles
as the escape's positive test; the negative test asserts a bare `DELETE` fails
with the append-only error.

### Drift guards: DB seed + the module's single byte-source

Post-swap the license menu is enforced on TWO surfaces, not three: the **DB seed**
(village-owned; pinned by the migration-026 integration test) and the **contract
module's single byte-source** - served via `schema.VillageAPISpecJSON()` and enforced
via `schema.ValidatePublishRequest`, which read the SAME in-module bytes, so served
vs enforced cannot drift and no vendored-bytes guard is needed. Obligation columns
still get full-row pinning with a length check against `schema.AllLicenses`, so a new
license FORCES a new expected row. The module surface is watched by
`internal/handler/openapi_license_guard_test.go`
(`TestValidatePublish_BadLicense_ErrorBodyPinsMenu` - the verbatim cross-repo 422
menu body) and `openapi_test.go`'s `TestServeOpenAPI_ServesModuleSpec` (served bytes
== `VillageAPISpecJSON()`, menu == `AllLicenses`) +
`TestPinnedContractVersion_MatchesExpected` (the consumer-side contract-version pin).

### The convergence test is the compensating control

Guarded DDL (`IF NOT EXISTS`) silently skips existing objects, so
`TestMigration026_ConvergesAllEnvClasses` diffs three environment classes
(fresh / gen-1 025 / gen-2 025, byte-frozen fixtures in
`internal/database/testdata/`) at **catalog granularity** - columns+defaults,
named constraints, indexes, triggers, function bodies. Weaker checks miss real
bugs: the schema-wide index-name collision was invisible to
`information_schema`-only diffs. Requires `CREATEDB` (skips with guidance
otherwise; CI's service-container superuser has it).

## Test performance (measured)

Village's integration suite is fast (~4s wall for the whole `-race` run against
a local container); the one structurally expensive test is the convergence
guard, which was profiled and optimized:

| Stage | `-race` time | Lever |
|-------|--------------|-------|
| baseline | 2.85s | serial: 3 scratch DBs, each replaying migrations from scratch |
| template clone + parallel classes | 2.83s | `CREATE DATABASE … TEMPLATE` clones a shared 001–024 base; per-class work in parallel goroutines (error-returning helpers - `t.*` stays on the test goroutine); creation stays serial (concurrent CREATE from one template errors). No win yet: the base rebuild dominated |
| fresh-from-template | ~2.8s cold | the fresh class clones the base too (same embedded prefix SQL either way) |
| **content-addressed base cache** | **1.90s warm** (0.88s test-only) | `village_conv_base` persists across runs, keyed by a sha256 of the embedded 001–024 SQL (`conv_base_meta`); a prefix change rebuilds. The ONE sanctioned scratch-DB survivor |

Rules this bought us:

1. **Measure before optimizing** - the first two levers moved nothing because
   the base rebuild, not the class work, dominated.
2. **Parallel test goroutines must be error-returning** - collect results under
   a mutex, `wg.Wait()`, then assert on the test goroutine.
3. **Caches must be content-addressed and self-invalidating** - never trust a
   leftover DB without verifying what built it. The test tampers with the
   stamped hash and requires a rebuild.
4. **The zero-orphan audit invariant is a committed post-suite gate.** A
   `TestMain` in the `internal/handler` integration build runs
   `assertNoOrphanAuditRows()` after `m.Run()`, so an orphan left by a test of
   ANY ordering fails a SINGLE integration run (exit 1) even though every test
   passed - it no longer depends on running the suite twice back-to-back. The
   gate is fail-closed (only an unreachable DB returns clean; a migrate/query
   error or an actual orphan forces exit 1). `backend-tests.yml` stays at one
   integration run. Other leftover-state failures may still surface only on a
   second run, so a re-run remains a useful spot-check.

## Publish-validation tests - three distinct rejection paths

`POST /api/v1/transcripts/publish` (`internal/handler/transcripts.go`,
`PublishTranscript`) rejects in three ways that callers and cross-repo verdicts
must keep straight. **All three checks run before anything is persisted** - the
secret scan in particular is a hard gate, not best-effort (the transcript and
blob are written only after it passes; the *content-hash write* later in the
handler is the best-effort step, not the scan).

| Path | Status | Trigger | Body asserts on |
|------|--------|---------|-----------------|
| Handler field check | **400** | malformed multipart, invalid metadata JSON, non-object JSON, missing `sessionId`, missing `model` | `"Invalid multipart form"`, `"Missing metadata field"`, `"Invalid metadata JSON"`, `"sessionId is required"`, `"model is required"` |
| Schema validation | **422** | `ValidatePublish` delegates to `schema.ValidatePublishRequest` (the contract module's single byte-source schema) - type/enum violations (e.g. unknown `model.harness` not in the `BestiaryHarness` enum; `source.format` not in `{jsonl, json}`; off-menu `license`) | `"metadata failed schema validation: …"` (for an enum miss, also `"value must be one of …"`) |
| Secret scan | **422** | `scanner.ScanForSecrets` finds a planted secret in the transcript body | `scanner.FormatScanErrors(...)` → starts `"Redaction check failed. Potential secrets detected:"` |

The two **422s are different bodies for the same status**. A test (or a peasant
verdict) that asserts only `422` cannot tell a schema rejection from a secret-scan
rejection - a regression that returns the *wrong* 422 would still pass. **Assert
the body, not just the status.**

Harness keys are normalized *before* schema validation:
`normalizeMetadataHarnessKey` (`internal/handler/migrator.go`) folds legacy
`model.modelHarness` / `model.provider` and pre-rename entry harness values
(`claude` → `claude-code`, `gemini` → `gemini-cli`) into canonical
`bestiary` values. So a *legacy-but-known* harness is accepted via normalization,
while *garbage* still trips the schema enum. An omitted `model.harness` is also
rejected by the schema module's required-field rule; the handler does not carry
a separate required-field guard.

### Worked example - assert the body

```go
// openapi_test.go - an unknown harness must reject as a SCHEMA enum 422,
// distinguishable from a secret-scan 422 by its body.
func TestPublishTranscript_UnknownHarnessRejectsAsSchemaEnum422(t *testing.T) {
    // Fail-closed: the embedded OpenAPI validator IS the enforcement here, so a
    // nil validator fails loudly (t.Fatal), never t.Skip - see lesson 2 below.
    if payloadValidator() == nil {
        t.Fatal("OpenAPI validator unavailable - harness enum 422 cannot be exercised")
    }

    mq := &mockQuerier{
        getTranscriptByOwnerAndLocalID: func(context.Context, sqlc.GetTranscriptByOwnerAndLocalIDParams) (sqlc.Transcript, error) {
            return sqlc.Transcript{}, errFakeNotFound
        },
        createTranscript: func(context.Context, sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
            return sqlc.Transcript{}, nil
        },
    }
    h := newTestHandler(mq, &mockTranscriptBlobStore{})

    metadata := map[string]any{
        "identity":  map[string]any{"sessionId": "550e8400-e29b-41d4-a716-446655440000", "schemaVersion": 2},
        "model":     map[string]any{"harness": "totally-made-up", "model": "claude-opus-4-5"}, // not in BestiaryHarness
        "timestamp": map[string]any{"start": 1700000000000, "end": 1700000060000},
        "source":    map[string]any{"filePath": "/p/t.jsonl", "format": "jsonl"},
        "project":   map[string]any{"hash": testProjectHash, "name": "repo"},
    }
    metaJSON, _ := json.Marshal(metadata)
    body, boundary := multipartBody(t, map[string]string{"metadata": string(metaJSON)},
        `{"contractVersion":"0.1.0","kind":"session_detail","sessionDetail":{"id":"s","harness":"claude-code","turns":[]}}`)

    r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
    r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
    r = r.WithContext(withTestUser(r.Context()))
    w := httptest.NewRecorder()

    h.PublishTranscript(w, r)

    if w.Code != http.StatusUnprocessableEntity {
        t.Fatalf("status = %d, want 422; body = %q", w.Code, w.Body.String())
    }
    // BOTH substrings: proves it's the SCHEMA path (not the scanner 422),
    // and proves the enum rule specifically fired.
    if b := w.Body.String(); !strings.Contains(b, "metadata failed schema validation") ||
        !strings.Contains(b, "value must be one of") {
        t.Fatalf("want schema-enum rejection body, got %q", b)
    }
}
```

The publish-validation behavior is fuzz-covered too: `FuzzPublishTranscriptMetadata`
(`transcripts_fuzz_test.go`) feeds valid, malformed, and adversarial metadata and
asserts the handler never panics (a 500 is tolerated; a panic is not).

### Test fixtures for publish payloads

Shared, declarative fixtures live in `internal/handler/testfixtures/`. YAML
fixtures (`testdata/fixtures/{sessions,timestamps,models,stats,quality,adversarial}.yaml`)
load via typed loaders (`LoadAdversarialFixtures`, `LoadValidPayloads`,
`LoadInvalidPayloads`, …), and `combinatorial.go` crosses fixture domains
(session × timestamp × provider × model × …) via `CartesianProduct*` /
`GenerateCombinatorialPayloads` to exhaust the cross-product test space. Generated
valid payloads are validated against `testfixtures/testdata/schema.json` (a
draft-07 schema used *only by the fixtures* - distinct from the production schema
`schema.ValidatePublishRequest` compiles from the contract module, which is JSON
Schema 2020-12); invalid payloads assert both schema rejection and an
`expectedError` substring.

Separately, `internal/handler/testdata/contract/` holds **byte-level contract
corpora** for wire back-compat - `current`, `legacy-provider-keyed`,
`legacy-raw-jsonl`, `legacy-metadata-field` - each with `{valid,invalid}/`
`{metadata,content,annotations}.json`. These prove the migrator accepts old
peasant wire shapes (key folding, value canonicalization) while still rejecting
genuinely-invalid ones.

Prefer these fixtures over inline literals: a single contract change should update
one fixture, not N tests.

### Enriched transcript preservation gate

`internal/handler/testdata/observed_model_preservation/` is the strict corpus for
the optional `TurnDetail.observedModel` evidence introduced by the released
Schema module. The loader uses known-field decoding, one-document enforcement,
an exact case count, an independent required-name inventory, and uniqueness
checks. The enriched case contains repeated A, changed B, and an omission; the
legacy case contains no observations and must never gain one.

The production proof runs `NewContentMigrator` over the stored envelope, encodes
through the same typed canonical rewrite boundary used by `GetTranscriptContent`,
then migrates and compares the re-emitted turns. `GET /api/v1/schema/version`
advertises the schema-owned flat `observed_model_v1` capability token only when that proof
passes. Publish remains backward compatible for content without observations.
Content carrying `observedModel` fails closed before scan/storage if the proof is
unavailable, with a response explaining that nothing was written and that the
client must retry against a Village advertising the capability.

The negative is executed, not descriptive metadata: the test substitutes an
encoder at the real typed rewrite boundary that deletes `observedModel`. It must
make the preservation proof fail, produce an empty capability set, and make the
enriched publish precondition refuse. The same failing evaluator must still let
the no-observation legacy fixture through. Keep this production-point negative
when the canonical rewrite code moves; a synthetic standalone marshal test does
not replace it.

## Cross-repo contract tests & gate-faithful expectations

The publish/pull wire format is a contract owned jointly by the
`github.com/peasant-labs/schema` module (source of truth), the peasant client
(producer), and village (the enforcer that validates via
`schema.ValidatePublishRequest` and serves `schema.VillageAPISpecJSON()` - one
module byte-source, no vendored copy). See
[`AGENTS.md`](AGENTS.md#shared-wire-contract-and-licensing) for
the ownership rules. Tests that cross this boundary must match the **system's real
policy** and **couple the two repos so they can't drift**. Lessons distilled from
these systems:

- **Pin the error body so verdicts and behavior can't drift.** Two rejections can
  share a status (the secret-scan 422 vs a schema/enum 422 are both `422`). A
  peasant verdict that asserts only `http_status: 422` cannot tell them apart, so
  a regression returning the *wrong* 422 still passes. Pin the verdict's
  `error_contains` to the **exact** body string village returns (e.g.
  `"value must be one of"` for an enum miss, or the
  `"Redaction check failed. Potential secrets detected:"` prefix for the scanner)
  - that one string is what keeps the producer test and the server behavior coupled.

- **Don't `t.Skip` the safety net - fail closed.** A test that `t.Skip`s when its
  validator/dependency is missing **self-disables exactly when the protection is
  gone**, and the skip reads as green. The publish validator is backed by the
  contract module's schema, always compiled into the test binary, so if it ever
  fails the test should `t.Fatal` (fail-closed), not skip. (Skipping is
  correct only for genuinely-external dependencies that are *expected* to be
  absent - e.g. the integration suite's `t.Skipf` when Postgres is unreachable.
  An always-present in-binary validator is not that case.)

- **Pin expectations to the real policy - never fake data to hit a number.** If a
  test needs an N, derive N from the system's actual gate, not from doctored
  inputs. (Cross-repo example: fixtures that legitimately lack a `model` are
  *held* by the client's no-model gate; giving them a placeholder model to force a
  higher count would *circumvent* the very gate the test should prove.) Encode the
  expected value as a **named constant** and assert the *reason* a row was
  accepted or rejected, not merely that a count changed.

- **Validation belongs in the schema, not an ad-hoc handler guard.** Strengthening
  a field rule means change it in the schema module → regen → tag → **re-pin
  `go.mod`** here → let `ValidatePublish` (`schema.ValidatePublishRequest`) enforce
  it. A handler guard that rejects something the schema doesn't declare makes the
  server enforce an **undocumented** rule with a dishonest message (a real example: a
  guard returned `"metadata failed schema validation: model.harness is required"` while
  the schema declared no `required` fields). If an interim guard is truly
  unavoidable, give it an honest message and a tracking issue.

- **Prove the test goes RED on the pre-fix tree.** An assertion can guard a
  *different* invariant than the bug you think it covers. Revert the fix locally
  and confirm the new test actually fails before trusting it.

- **Reuse the fixture structure.** Add new expected values as named constants /
  fixture entries (`internal/handler/testfixtures/`, the contract corpora); the
  loaders and generic tests pick them up without new assertion helpers. Don't
  inline literals across test files.
