# Peasant Village - Backend

Go API server for the Peasant Village.

## Prerequisites

The backend's wire contract comes from the **public** Go module
`github.com/peasant-labs/schema` (pinned in `backend/go.mod`). It is fetched over the
standard module proxy, so local development and Docker builds need **no** GitHub
token, `GOPRIVATE`, or git `insteadOf` remap. A contract change is a `go.mod`
re-pin to a new schema module tag - never a re-vendor.

Railway supplies `RAILWAY_GIT_COMMIT_SHA` automatically to GitHub-triggered builds.
Local production builds pass the same value explicitly with
`--build-arg RAILWAY_GIT_COMMIT_SHA=$(git rev-parse HEAD)`. The Dockerfile requires
a full 40-character lowercase revision, prints it during the build, records it in
the standard `org.opencontainers.image.revision` label, and exposes it as the
non-secret runtime value `VILLAGE_BUILD_REVISION`. From the repository root,
`scripts/verify-production-artifacts.sh` builds both production images and checks
their labels, runtime revisions, and real image commands.

## Development

From the repo root, start the persistent, worktree-isolated encrypted backend for
interactive development with:

```sh
make backend-dev
make backend-dev-seed  # optional sample data
```

This starts PostgreSQL, MinIO, bucket initialization, and the Air-reloading API,
generating and reusing local-only keys so encrypted rows survive restarts. It
needs Docker Compose and common Git/curl/OpenSSL shell tooling, not Nix. Use
`make backend-dev-down` to preserve data or `make backend-dev-reset CONFIRM=1`
to remove only this worktree's namespace, volumes, and generated keys.

Run the separate disposable integration proof with:

```sh
make backend-encrypted-test
```

The proof uses Nix, a unique-per-invocation disposable Compose namespace, and real PostgreSQL/MinIO,
initializes the bucket, applies every registered migration, rejects integration skips, and
cleans up only resources it created after proving the namespace had no running
or stopped containers, volumes, or networks. Caller overrides are also checked
and never reused. See the root README for ports, retention, recovery, and
test-only key custody. The canonical security design is
[`../docs/transcript-storage-security.md`](../docs/transcript-storage-security.md).

To run the full backend and frontend together:

```sh
make dev
```

To run only the backend (requires Postgres and MinIO running):

```sh
go run ./cmd/server
```

The server starts at `http://localhost:8080`. Database migrations are applied automatically on startup.

## Configuration

The server reads `.env` from the project root (`../.env` relative to `backend/`). See `.env.example` for all variables, and [`../docs/environment.md`](../docs/environment.md) for the required/optional breakdown and how to obtain each value.

Key environment variables:

| Variable | Description |
| --- | --- |
| `DATABASE_URL` | Postgres connection string |
| `POOL_MAX_CONNS` | Max DB connection-pool size (optional). Default `max(10, 2×NumCPU)`. PostgreSQL advisory locks belong to a physical session, so a locked publish keeps one connection across its source probe, preflight, S3 upload, and short persistence transactions; no database transaction spans S3. A pool of one safely serializes publishes, while larger pools absorb concurrent transcript/annotation publishes from a client running `--concurrency N`. Precedence: `POOL_MAX_CONNS` > a `pool_max_conns` set directly in `DATABASE_URL` > the built-in default. |
| `S3_ENDPOINT` | MinIO/S3 endpoint |
| `S3_BUCKET` | Bucket for transcript files |
| `S3_ACCESS_KEY` | S3 access key |
| `S3_SECRET_KEY` | S3 secret key |
| `TRANSCRIPT_KEK_ACTIVE_VERSION` | Positive active environment KEK version for encrypted body operations |
| `TRANSCRIPT_KEK_KEYRING` | JSON map of positive versions to base64-encoded 32-byte KEKs; no fallback exists |
| `GITHUB_CLIENT_ID` | GitHub OAuth app client ID |
| `GITHUB_CLIENT_SECRET` | GitHub OAuth app client secret |
| `GITHUB_APP_ID` | GitHub App ID for collective repo linking (optional; feature disabled when empty) |
| `GITHUB_APP_PRIVATE_KEY` | GitHub App PEM private key (optional; needs Contents + Metadata read) |
| `JWT_SECRET` | Secret for signing JWTs |
| `PORT` | Server port (default 8080) |
| `FRONTEND_URL` | Frontend origin for CORS |

Pending encrypted content identities are repaired explicitly with
`server -backfill-content-identity`. This mode processes all pending rows and exits without a listener.
Its authority and identity invariants are canonical in
[`../docs/transcript-storage-security.md`](../docs/transcript-storage-security.md),
with operator procedure in
[`../docs/transcript-encryption-operations.md`](../docs/transcript-encryption-operations.md).
For production provisioning and secret mapping, use the
[`Railway PostgreSQL and Cloudflare R2 activation runbook`](../docs/railway-cloudflare-r2-activation.md).

Historical transcript titles are repaired explicitly with
`server -backfill-titles=dry-run` or `server -backfill-titles=apply`. Both
process every stored transcript row and exit without a listener; only `apply`
writes. To run a title backfill after a `github.com/peasant-labs/redact`
version bump:

1. Deploy the server build that carries the new `redact` module version.
2. Run `server -backfill-titles=dry-run`. Read the `title_backfill_complete`
   log line's `scanned`, `unchanged`, `would_update`, `derived`, `sanitized`,
   and `failed` keys. No row changes.
3. If the counts look right, run `server -backfill-titles=apply` with the same
   deployed build. Read the same `title_backfill_complete` log line's
   `updated` and `failed` keys; a failed row is left unchanged and safe to
   retry after the underlying dependency or content is repaired (see the
   row-level warn/error log for the failing stage; it never logs raw
   transcript content).

Historical transcript session origins are reclassified explicitly with
`server -backfill-origins=dry-run` or `server -backfill-origins=apply`. Both
process every stored transcript row and exit without a listener; only `apply`
writes. Rows published before the origin column existed carry the fail-safe
`unknown` value, which is listed exactly like a user session, so running this
is what moves agent-driven sessions into the collapsed discovery group:

1. Deploy the server build that carries the session-origin column.
2. Run `server -backfill-origins=dry-run`. Read the `origin_backfill_complete`
   log line's `scanned`, `unchanged`, `would_update`, and `failed` keys. No row
   changes.
3. If the counts look right, run `server -backfill-origins=apply` with the same
   deployed build. Read the same `origin_backfill_complete` log line's
   `updated` and `failed` keys. A failed row is left unchanged, stays fully
   visible, and is safe to retry after the underlying dependency or content is
   repaired (see the row-level error log for the failing stage; it never logs
   raw transcript content).

Rerunning `apply` is idempotent: a row whose stored origin already equals the
freshly classified one is counted as `unchanged` and not written.

## Key Directories

```
├── cmd/server/            # Entrypoint (main.go)
├── internal/
│   ├── config/            # Env config loading (godotenv)
│   ├── database/
│   │   ├── migrations/    # SQL migrations (embedded, auto-applied)
│   │   ├── queries/       # SQLC query definitions
│   │   └── sqlc/          # Generated code (do not edit)
│   ├── handler/           # HTTP handlers (transcripts, groups, auth, tags)
│   ├── scanner/           # Secret scanning safety net
│   └── storage/           # S3 client
└── sqlc.yaml              # SQLC config
```

## Database

> **Invariants reference:** [`../docs/database-invariants.md`](../docs/database-invariants.md)
> - migrations rules, the governance/licensing data model, the audit triggers,
> and the `app.*` GUCs (`app.actor_id` fail-closed actor attribution,
> `app.audit_maintenance` append-only escape). Read it before touching
> migrations, triggers, or anything writing to `transcripts`.
> Storage encryption and cross-system invariants are canonical in
> [`../docs/transcript-storage-security.md`](../docs/transcript-storage-security.md).

Migrations live in `internal/database/migrations/` and are applied automatically when the server starts. To add a new migration:

1. Create `internal/database/migrations/NNN_description.up.sql`
2. Add an entry to the `migrations` slice in `internal/database/migrate.go`
3. Update queries in `internal/database/queries/`
4. Run `make sqlc` to regenerate Go types

## API

All endpoints are under `/api/v1`. Authentication uses session cookies (web) or API keys (CLI).

Key endpoints:

- `POST /api/v1/transcripts/publish` - publish a transcript (multipart: `metadata` JSON + `transcript_file`)
- `GET /api/v1/transcripts` - list/search transcripts (params: `q`, `tags`, `provider`, `owner`, `project`, `repo`, `sort`, `page`, `limit`)
- `GET /api/v1/transcripts/:id` - transcript detail
- `GET /api/v1/auth/github` - initiate GitHub OAuth
- `POST /api/v1/auth/api-keys` - create an API key

## Testing

Use `make backend-encrypted-test` from the repository root for the meaningful
real-service flow. For infrastructure-free unit tests, run
`nix develop -c env GOWORK=off GOFLAGS=-mod=readonly go test -race ./...` here.
