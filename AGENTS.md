# Contributor Guide for Automated Tools

Village is a commons for redacted AI-agent transcripts. It stores published
copies, enforces access controls, and provides discovery over PostgreSQL and
S3-compatible object storage.

This file is the repository-local source of instructions for coding agents and
other automated contributors. Do not rely on files outside this repository.

## Repository map

- `backend/`: Go HTTP API, PostgreSQL migrations and sqlc queries, authentication,
  secret scanning, and encrypted object storage.
- `frontend/`: Next.js App Router application. See `frontend/README.md`.
- `docs/`: database, storage-security, deployment, and lifecycle references.
- `TESTING.md`: detailed test patterns and integration-test safety rules.

The Go workspace uses `backend/`. Run Go commands there unless a Make target is
explicitly documented as a root command.

## Required checks

```sh
cd backend && go build ./...
cd backend && go test -race ./...
cd backend && gofmt -l . && go vet ./...
```

Integration tests require PostgreSQL and use the `integration` build tag:

```sh
export TEST_DATABASE_URL="postgres://test:test@localhost:5432/village_test?sslmode=disable"
cd backend
DATABASE_URL="$TEST_DATABASE_URL" go run ./cmd/server -migrate-only
go test -tags=integration -race ./...
```

Run `make backend-encrypted-test` from the repository root for the disposable,
real PostgreSQL and MinIO aggregate. It rejects skipped integration tests.

## Test rules

- Always use the race detector for Go tests.
- Test production handlers and mock only their dependencies. SQL, transaction,
  trigger, and object-storage behavior requires integration coverage against
  real services.
- Assert observable behavior: HTTP status and body, persisted rows, and stored
  objects. The two publish `422` paths (contract validation and secret scanning)
  have different bodies, so tests must not assert status alone.
- Put table-driven and combinatorial cases in the existing YAML fixtures rather
  than inline test tables.
- A new migration receives its own structure and integration tests. Do not edit
  tests for older migrations merely to establish which migration is newest.
- Never hand-edit `backend/internal/database/sqlc/`; regenerate it from
  `backend/` with `sqlc generate`.

See `TESTING.md` for fixtures, database setup, teardown, and worked examples.

## Database and migration safety

Read `docs/database-invariants.md` before changing migrations, SQL, transcript
mutation transactions, licensing, visibility, deletion, or governance audit
behavior. Update that document in the same commit whenever an invariant changes.

- Shipped migrations are immutable. Add a numbered `.up.sql`/`.down.sql` pair
  and bump `wantLatestMigration` in the same commit.
- Versions 19 and 25 are intentionally absent. Do not reuse either number.
- Transcript inserts, governance-axis updates, and deletes are audited by
  fail-closed database triggers and require transaction-local `app.actor_id`.
- The governance audit is trigger-written and append-only. Maintenance deletion
  is allowed only in one explicit transaction with
  `app.audit_maintenance='on'`.
- Encrypted transcript storage mutations require transaction-local
  `app.transcript_writer_version=1`.
- Integration teardown must delete transcripts as the system actor, delete users,
  and purge audit rows last using the sanctioned maintenance helper.
- A visibility-menu change must update both database checks, the Go constants,
  and the PATCH validation path.

## Shared wire contract and licensing

The public `github.com/peasant-labs/schema` module is the source of truth for
wire types, the Village OpenAPI document, publish validation, and the license ID
menu. Village serves and validates the module's same embedded bytes.

Contract changes land and receive a module tag before Village updates its
`go.mod` pin. Do not fork or vendor a local contract copy, and do not add an
undocumented handler-only validation rule.

When adding a license:

1. Add the license and regenerate specifications in the schema module, then tag it.
2. Update the schema module pin here.
3. Add a Village migration that seeds the license and its three existing CC
   obligation fields.
4. Update obligation expectations, the pinned API version when it changes, and
   `wantLatestMigration`.

The current obligation booleans model Creative Commons licenses only. Other
license families may require new data-model axes; do not reinterpret the
existing fields. Licenses form a partial order and are not assigned a scalar
permissiveness rank. A granted license cannot be cleared through the application.

## Change discipline

- Validate external input at system boundaries and return actionable errors.
- Keep dependencies injectable and avoid test-only production paths.
- Do not log transcript content, credentials, object keys, paths, remotes, or
  identity data.
- Preserve real user-facing routes and behavior unless the change explicitly
  removes them.
- Keep documentation links repository-relative or public.

For general contribution and disclosure guidance, see `CONTRIBUTING.md` and
`SECURITY.md`.
