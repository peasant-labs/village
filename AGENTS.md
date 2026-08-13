# Agent Instructions - Village

Village is a commons for redacted AI agent transcripts: a registry,
access-control layer, and discovery surface on top of S3-compatible blob
storage. It stores **published copies** of transcripts (it does not replace the
peasant client's local store) and indexes them for search and sharing.

- **Backend** (`backend/`): Go HTTP API - sqlc-generated queries (pgx/v5), JWT
  sessions + API keys + GitHub OAuth, server-side secret scan.
- **Frontend** (`frontend/`): Next.js (App Router).
- **Data**: PostgreSQL (metadata) + MinIO / S3-compatible (transcript blobs).
- **Edge**: Caddy (local reverse proxy + TLS).

This file primarily documents the **backend**. The repo is a Go workspace (`go.work` →
`use ./backend`, Go 1.25.5); run Go commands from `backend/` (or via the
workspace). Frontend specifics live in `frontend/README.md`.

## Testing

### How to run tests

**All backend tests** (`-race` is mandatory - CI runs every suite with it):
```bash
cd backend && go test -race ./...
```

`make test` (from the repo root) is the convenience wrapper (`cd backend && go test ./...`); add `-race` when running it directly.

**Single package / single test:**
```bash
cd backend && go test -race ./internal/handler/ -v
cd backend && go test -race ./internal/handler/ -run TestPublishTranscript_UnknownHarnessRejectsAsSchemaEnum422 -v
```

**Integration suite** (build-tagged `integration`, needs a real Postgres):
```bash
# Point the tests at a Postgres; each integration test calls RunMigrations
# itself (idempotent), but some assume an already-migrated DB - so migrate once
# up front, exactly as CI does.
export TEST_DATABASE_URL="postgres://test:test@localhost:5432/village_test?sslmode=disable"   # CI-canonical
cd backend && DATABASE_URL="$TEST_DATABASE_URL" JWT_SECRET=throwaway-only-for-config-validation go run ./cmd/server -migrate-only
cd backend && go test -tags=integration -race ./...
```
Integration tests `t.Skipf(...)` with guidance when `TEST_DATABASE_URL` is
unreachable, so the untagged unit suite stays runnable without infrastructure.
CI sets `TEST_DATABASE_URL=postgres://test:test@localhost:5432/village_test?sslmode=disable`
(use that). Per-file fallback defaults vary where the env is unset - some use
`test:test@localhost:55432/village_test`, others `peasant:peasant@localhost:5432/peasant_test`
- so always set `TEST_DATABASE_URL` explicitly to match your DB. Integration
tests are **hermetic**: they clean up by deleting the owning user, and
`ON DELETE CASCADE` removes the transcript + its commits.

**Build:**
```bash
cd backend && go build ./...
```

**Quality gates** (run before every commit):
```bash
cd backend && go build ./...          # compiles
cd backend && go test -race ./...     # unit
cd backend && go test -tags=integration -race ./...   # integration (needs Postgres)
cd backend && gofmt -l . && go vet ./...
```
The local real-service aggregate is `make backend-encrypted-test`. It starts an
isolated disposable PostgreSQL/MinIO project, initializes its bucket, runs every
integration-tagged package under race, rejects all skip events, and cleans up.
For interactive encrypted backend development, `make backend-dev` starts this
worktree's persistent PostgreSQL, MinIO, bucket initializer, and hot-reloading
API; optionally run `make backend-dev-seed`, and stop it with
`make backend-dev-down` without removing local encrypted data or keys.
CI (`.github/workflows/backend-tests.yml`)
is the gate: **build → unit `-race` → migrate → integration `-tags=integration -race`**
against a Postgres service container.

**Database / sqlc / migrate:**
```bash
make migrate    # docker compose exec backend go run ./cmd/server -migrate-only
make sqlc       # runs bare `sqlc generate`; the config is backend/sqlc.yaml, so run it
                # as `cd backend && sqlc generate` (there is no root sqlc.yaml)
make seed       # docker exec -i <postgres> psql -U peasant -d peasant < scripts/seed.sql (needs a running compose stack)
make up / make dev / make down    # full stack via docker compose
```

### Test package map

| Package | Type | What it covers |
|---------|------|----------------|
| `internal/handler` | Unit + Integration | The HTTP surface: publish, pull, annotations (push/manifest/retraction), auth, collective repos, commit batching. Unit tests use a **`mockQuerier`** (DI mock of the sqlc `Querier`) + `httptest`; integration tests (`//go:build integration`) hit a real Postgres via `TEST_DATABASE_URL`. Publish validation: schema-enum **422** (`ValidatePublish`) vs secret-scan **422** (`scanner.FormatScanErrors`) vs malformed **400**. |
| `internal/database` | Integration | Migrations + pool. Per-migration tests `migration_NNN_*_test.go` (and `_integration_test.go`) verify the schema changes that migration introduces. |
| `internal/database/sqlc` | Unit + Integration | Generated query layer (pgx/v5). Some `_test.go` here assume a migrated DB (`-tags=integration`). **Generated - never hand-edit; regenerate via `sqlc`.** |
| `internal/auth` | *(no test files yet)* | `apikey.go`, `jwt.go`, `oauth.go` - API-key mint/verify, JWT issue/verify, GitHub OAuth exchange. |
| `internal/scanner` | *(no test files yet)* | `redaction.go` - server-side secret scan; `FormatScanErrors` is the publish **422** body when a planted secret is found. |
| `internal/storage` | Unit + Integration | Encrypted transcript store, strict object absence, envelope integrity, and real MinIO ciphertext lifecycle. |
| `cmd/village-setup-demo` | Integration | Dev-only credential seeder (real `auth.GenerateAPIKey`); refuses non-localhost. Drives the peasant full-stack e2e. |

### Test fixtures

Shared, declarative fixtures live in `internal/handler/testfixtures/`
(`loader.go` loads YAML `FixtureItem`/`TimestampRange`/… ; `combinatorial.go`
expands mutation/permutation variations). Contract bytes live in
`internal/handler/testdata/contract/`. Prefer these over inline literals - a
single contract change should update one fixture, not N tests.

### Test writing rules

- **Do not mock the system under test.** Handler tests run the **real** handler;
  the `mockQuerier` mocks the *dependency* (the DB), not the handler. Integration
  tests use the real handler + real Postgres.
- **Favour integration tests for anything touching SQL, transactions, or S3.**
  Unit tests with `mockQuerier` are for routing/validation/error-mapping logic;
  anything asserting actual persistence, FK cascade, or atomic commit batches
  must be `-tags=integration` against real Postgres.
- **Assert observable outcomes** - HTTP status + body, rows persisted, blob
  written - not internal call counts.
- **`-race` always.** Every suite runs under the race detector in CI.
- **Migration tests:** a new migration `NNN` gets its own `migration_NNN_*_test.go`;
  do not retrofit prior migration tests. Integration variants carry
  `//go:build integration`.
- **Couple cross-repo contract assertions on the error body, not just status** -
  see [Cross-repo API contract](#cross-repo-api-contract-village--schema-module--peasant)
  and [`TESTING.md`](TESTING.md).

## Architecture

### Backend layout (`backend/internal/`)

| Package | Responsibility |
|---------|----------------|
| `router` | Route table; wires middleware → handlers. |
| `middleware` | CORS + structured request logging only (`cors.go`, `logging.go`). **Not** auth. |
| `handler` | HTTP handlers: `transcripts.go` (publish), `pull*.go`, `annotations*.go` (push / manifest / retraction), `auth*.go`, `collective_repos*.go`, commit batching. `openapi.go` serves + enforces the publish contract from the `github.com/peasant-labs/schema` module (`VillageAPISpecJSON()` / `ValidatePublishRequest`). **Auth middleware lives here** - `auth_middleware.go` (`h.AuthRequired` / `h.AuthOptional` / `h.authenticate`: JWT cookie + API-key Bearer). |
| `auth` | API keys, JWT, GitHub OAuth. |
| `scanner` | Server-side secret scan (`FormatScanErrors`). |
| `storage` | S3/MinIO blob storage. |
| `database` | `migrations/` (numbered SQL), `queries/` (sqlc input), `sqlc/` (generated output), pool. |
| `config` | Env-driven config + validation (`JWT_SECRET`, `DATABASE_URL`, S3 creds, OAuth). |
| `github` | GitHub API client (org affiliation, repo metadata). |
| `backfill` | One-off data backfills tied to migrations. |
| `cmd/server` | The API server; `-migrate-only` applies migrations and exits. |
| `cmd/village-setup-demo` | Dev-only credential seeder for the e2e harness. |

### Database & migrations

- **Migrations** live in `internal/database/migrations/` as numbered pairs
  `NNN_name.up.sql` / `NNN_name.down.sql` (latest **032** `authoritative_publication_receipts`;
  next new migration = **033** - bump `wantLatestMigration` in
  `migrations_registry_test.go` in the same commit. **025 is deliberately
  unregistered**: its number is burned by two never-merged in-branch generations;
  the guarded, convergent 026 supersedes both). Applied by `cmd/server -migrate-only` (CI + `make migrate`)
  or `RunMigrations` (idempotent; integration tests call it themselves).
- **sqlc**: queries authored in `internal/database/queries/*.sql` are generated
  into `internal/database/sqlc/` (`package sqlc`, `pgx/v5`, `emit_json_tags`,
  `emit_db_tags`, `emit_empty_slices`) per `backend/sqlc.yaml`. Regenerate with `sqlc generate`
  (from `backend/`) after editing a query or a migration the queries depend on.
  **Never hand-edit generated files in `internal/database/sqlc/`.**

### Schema versions

| Schema | Location | When to bump |
|--------|----------|--------------|
| **Database** | `internal/database/migrations/` | Adding tables/columns (latest `032`; next = `033`, skipping nothing further - the 025 gap is historical). Add a paired `.up`/`.down` + a migration-specific test, and bump `wantLatestMigration` (`migrations_registry_test.go`) in the same commit. |
| **Publish contract** | delivered by the pinned `github.com/peasant-labs/schema` module - enforced via `schema.ValidatePublishRequest` (`openapi.go`) | Only when the contract changes: **re-pin `go.mod`** to a new schema module tag (never re-vendored). `$id urn:peasant:publish-request:0.4.0`. |
| **OpenAPI doc** | served from the same module via `schema.VillageAPISpecJSON()` (`openapi.go` → `GET /api/v1/openapi.json`) | Moves in lockstep with the pin - served doc and enforced schema are one module byte-source, so they cannot drift. The served/enforced contract **version** is pinned by `wantVillageAPIVersion` (asserted by `TestPinnedContractVersion_MatchesExpected`) and must equal the module's `schema.VillageAPIVersion`. |

### Publish validation (two distinct 422s + 400)

`POST /api/v1/transcripts/publish` (`handler/transcripts.go`) rejects in three ways - keep them straight:
- **400** - malformed / missing handler-level required fields (bad multipart, invalid metadata JSON, missing `sessionId`, missing `model`).
- **422 (schema)** - `ValidatePublish` (`openapi.go`) delegates to
  `schema.ValidatePublishRequest` (the contract module's single byte-source
  JSON-Schema, santhosh-tekuri/jsonschema): type/enum/required violations →
  `"metadata failed schema validation: …"` (e.g. an unknown `model.harness` →
  `"value must be one of …"` via the `BestiaryHarness` enum).
- **422 (secret scan)** - `scanner.FormatScanErrors` when the transcript body
  trips the server-side secret scan.

The two 422s are **different bodies**; tests and cross-repo verdicts must assert
the body, not just the status (see [`TESTING.md`](TESTING.md)).

### Cross-repo API contract (village ↔ schema module ↔ peasant)

The publish/pull wire format is a **contract with a single source of truth (the
`github.com/peasant-labs/schema` module) that both backends pin**. A consumer
change without a matching module tag is **drift**.

| Repo | Role |
|------|------|
| **`github.com/peasant-labs/schema`** | **Source of truth** - Go structs + the generated OpenAPI / publish-request specs, committed inside the module (one byte-source: `VillageAPISpecJSON()` serves them, `ValidatePublishRequest` enforces them). |
| **peasant** (client) | Producer - builds the publish/pull request (pins the same module). |
| **village** (this repo) | Enforcer - validates inbound payloads via `schema.ValidatePublishRequest` and serves `schema.VillageAPISpecJSON()` at `GET /api/v1/openapi.json`. Owns **no** contract bytes: only the `go.mod` pin + the DB license seed. |

**Rules:**
1. **Validation belongs in the schema, not an ad-hoc handler guard.** Adding or
   strengthening a field rule means: change it in the schema module → regen → tag
   the module → **re-pin `go.mod`** here → let `ValidatePublish`
   (`schema.ValidatePublishRequest`) enforce it. A handler guard that rejects
   something the schema doesn't declare makes the server enforce an **undocumented**
   rule. If an interim guard is truly unavoidable, give it an **honest** message and
   a tracking issue.
2. **Re-pin, don't fork.** The publish-request schema and the Village API spec are
   delivered by the module - never hand-edit or re-vendor a local copy to diverge.
   A contract change lands in the schema repo first (its own PR + tag), then a
   consumer `go.mod` re-pin.
3. **Coordinate version negotiation.** The contract `$id` (`…:0.4.0`) is the
   negotiated window with the peasant client; a breaking change moves in lockstep
   with peasant + a module tag bump.
4. **Test both sides, coupled** - pin the error body so peasant and village can't
   drift apart silently (see [`TESTING.md`](TESTING.md#cross-repo-contract-tests--gate-faithful-expectations)).

### Adding a license (e.g. `CC-BY-NC-4.0`) - checklist

| # | Step | Repo | Caught by a failing check? |
|---|------|------|----------------------------|
| 1 | `License` const + `IsValid()` case + `AllLicenses` | schema module | schema compile/tests |
| 2 | `schema-gen` regen (`publish-request`, `village-api` specs) | schema module | schema |
| 3 | SQLite CHECK widen migration | peasant | peasant |
| 4 | Tag the schema module vN | schema module | - |
| 5 | Re-pin `go.mod` to the new schema module tag | village | ✅ seed guard + obligation length check + roundtrip cases fire |
| 6 | New migration: `INSERT INTO licenses (id, name, url, 3 obligations)` | village | ✅ seed guard (set-equality with `AllLicenses`) |
| 7 | Expected-obligations row in the pinning test | village | ✅ length check (migration_026 integration test) |
| 8 | Bump `wantLatestMigration` | village | ✅ registry test |

The publish-request enum + the served `SchemaLicense` menu are **no longer**
village-side re-vendor steps: they ship inside the schema module and move with the
`go.mod` pin (served == enforced by construction). If the re-pin bumps
`schema.VillageAPIVersion` (minting a new frozen spec version - a menu widening
does), update `wantVillageAPIVersion` + `TestPinnedContractVersion_MatchesExpected`
**in the same commit**: that consumer-side test is the version-drift detection now
that the schema repo's go-apidiff gate exempts the version marker.

**Ceremony rule:** a menu/contract widening is `schema` `types.go` + regen + a
**schema-repo tag (its own PR first)** → THEN re-pin the consumers. Non-CC licenses
(`proprietary`, `unlicensed`, `*-ND`) additionally need new obligation axes (ALTER +
consent-screen extension) - the boolean model is CC-only by design (see the licenses
table comment in migration 026).

### Adding a visibility tier (e.g. `unlisted`) - checklist

The visibility menu is CHECK-constrained in TWO immutable migrations (001
`transcripts`, 026 audit table) and mirrored by the Go `dbVisibility*`
constants (`pull.go`) and the PATCH gate. A new tier is a NEW migration that
ALTERs **both** CHECKs, plus the Go constants + PATCH switch - if only the
transcripts side is widened, the migration-026 audit triggers reject the new
value and **block the mutation**.

### Auth model

Three credential paths converge in `handler/auth_middleware.go`
(`AuthRequired` / `AuthOptional` / `authenticate`) + the `auth` package: **JWT**
browser sessions (GitHub OAuth → `oauth.go` → `jwt.go`), **API keys**
(`apikey.go`, used by the peasant CLI and the e2e seeder), and **CLI auth
sessions** (the device-style login in migrations `003`). Access control layers on top:
tiered access, collective/GitHub-org affiliation, per-repository shares.

### Directory layout (repo root)

```
backend/            Go API (this doc)
  cmd/{server,village-setup-demo}
  internal/{router,middleware,handler,auth,scanner,storage,database,config,github,backfill}
  sqlc.yaml
frontend/           Next.js (see frontend/README.md)
docs/               database-invariants.md, deletion-data-lifecycle-model.md,
                    oauth-app-registration.md, collective-repository-connections.md
scripts/            seed.sql, ops helpers
docker-compose.yml  postgres + minio + minio-init + caddy + backend + frontend
Caddyfile, flake.nix, go.work
```

### Design documents

| Document | Purpose |
|----------|---------|
| `README.md` / `backend/README.md` | Stack overview, local run. The wire contract is the **public** `github.com/peasant-labs/schema` module - no private-module auth needed. |
| `TESTING.md` | **The comprehensive testing strategy**: unit/integration suites, mockQuerier + httptest patterns, migration/sqlc conventions, governance-era rules (fail-closed fixtures, append-only teardown, savepoints, drift guards, convergence control), measured performance levers. |
| `docs/database-invariants.md` | **The database reference**: migration-system rules, licensing/visibility/audit data model, the migration-026 triggers, the `app.*` GUC registry (`app.actor_id`, `app.audit_maintenance`), fail-closed actor model, tamper resistance, test-suite invariants. |
| `docs/transcript-storage-security.md` | Canonical encrypted-storage architecture and invariants: threat boundaries, envelope, lifecycle, migration, deletion, rotation, cutover, and evidence. |
| `docs/deletion-data-lifecycle-model.md` | Deletion semantics (hard-delete + durable audit - RESOLVED), soft-delete design space, §7 GDPR lawful basis. |
| `docs/oauth-app-registration.md` | GitHub OAuth App setup. |
| `docs/collective-repository-connections.md` | Collective ↔ repository linking model. |
| `frontend/README.md` | Frontend specifics. |

## Review Criteria

All plans and code changes are reviewed against three axes:

### 1. Correctness (spirit and technicality)
- Does it faithfully serve the originating request and the agreed design?
- Are the technical decisions consistent with the proposal's rationale?
- Any gap where the design says one thing and the code does another, or a
  requirement silently dropped?

### 2. Test quality
- **Favour integration / end-to-end tests** over brittle unit tests; anything
  involving SQL, transactions, S3, or multi-component interaction is
  integration-tested against real Postgres.
- **The system under test must NOT be mocked out** - mock the `Querier`/S3
  *dependency*, not the handler.
- **Use shared fixtures** (`internal/handler/testfixtures/`), not repeated inline
  literals.
- **Test observable behaviour** (HTTP status + body, rows, blobs), not internal
  calls. For cross-repo contract behavior, pin the **error body** so it can't
  drift from the peasant client's expectation.

### 3. Elegance and complexity matching
- **Design the API you know you will need**, without over-engineering for
  hypotheticals.
- **Complexity proportional to the problem.** Validation lives in the contract
  module's schema (one source of truth), not duplicated handler guards. A genuine
  multi-step flow (publish → scan → blob put → atomic commit batch) deserves
  clear stages; a one-field check does not deserve a framework.

## Visual / screenshot UI harness (frontend)

`frontend/scripts/visual/` - the design-system fidelity capture harness. The shared
model + the 7 core primitives (boot/mock/shoot/stitch/diff/probe/gate) are documented
once in the **poly-repo root `AGENTS.md` → "Visual / screenshot UI harness"**; this is
the village-specific map:

- **manage** (`manage-{collectives,detail,settings}` → `/groups`, `/groups/:id`,
  `/groups/:id/settings`) - **authenticated**; use `manage-shoot.mjs` +
  `manage-boot-village.mjs` (drives the login the plain boot lacks).
- **explore** (`cex-explore` → `/`) - `explore-shoot.mjs` + `boot-explore.mjs`;
  signed-out is a real state (the full nav only renders when authed).
- general: `village-shoot.mjs` / `boot-village.mjs`.

Mock backend: `mock-rest.mjs` (+ `mock-rest-explore.mjs`, which adds `/auth/me` +
`/auth/orgs`). SxS: `manage-stitch-sxs.mjs` / `stitch-sxs.mjs`. Computed-style probes:
`probe-village.mjs` / `probe-explore.mjs`. Gate: `surface-gate.mjs`. Regression
baselines live in `frontend/scripts/visual/baseline/` (tracked); working captures
go to a caller-chosen output dir, default `/tmp` (untracked). The frontend resolves
fairtrade from the registry (the pinned range in `frontend/package.json`); after bumping it, re-run `pnpm install` and **confirm
`frontend/node_modules/@peasant-labs/fairtrade` carries the expected version before
trusting a shot** (build-provenance rule). Prod-server note: this app is Next
`output: standalone` - boot `node .next/standalone/<repo-path>/frontend/server.js`
(with `.next/static` + `public` copied in), not `next start`. Harness
consolidation across all three repos is a tracked followup (beads IDs in the
polyrepo-root `.agents.local/`).
