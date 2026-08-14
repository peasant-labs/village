# Village

A commons for redacted AI agent transcripts. Publish your sessions from a local
transcript store, share them with collectives, and discover what others have
been working on.

The village itself is a registry, access-control layer, and discovery surface
on top of S3-compatible blob storage. It does not replace your local
transcript database - it stores published copies and indexes them for search
and sharing.

## Stack

- **Backend:** Go (HTTP API, sqlc-generated queries, JWT sessions)
- **Frontend:** Next.js (App Router)
- **Data:** PostgreSQL for metadata, MinIO (S3-compatible) for transcript blobs
- **Edge:** Caddy as a local reverse proxy with TLS

## Run locally

`make backend-dev` needs Docker with Compose v2 plus common Git, Bash, curl, and
OpenSSL tooling; it does not require Nix. The disposable
`make backend-encrypted-test` proof additionally requires Nix. Full-stack
frontend work also needs Node and pnpm (see `packageManager` in `package.json`).

To exercise the encrypted backend from a clean disposable environment:

```sh
make backend-encrypted-test
```

This single command starts only an isolated PostgreSQL 16 and MinIO project on
`127.0.0.1:55460`, `:59060`, and console `:59061`; initializes the
`village-encrypted-test` bucket; applies every registered migration; and runs every
integration-tagged package under `-race` with skip events rejected. That includes
the writer fence and mounted encrypted publish/read/pull/ETag/delete lifecycle.
The default project name is unique per invocation and printed before startup.
Before creating anything, the script refuses any selected-project running or
stopped container, volume, or network under Docker Compose or Podman Compose.
It removes only resources created under that clean project namespace on success or
failure. The deterministic KEK exists only in the child validation process and
is not production custody.

If a run is intentionally retained with `KEEP_ENCRYPTED_TEST_STACK=1`, remove
only its printed namespace with
`make backend-encrypted-down VILLAGE_ENCRYPTED_PROJECT=<printed-name>`. This
explicit target is intentionally destructive only within the selected project.
Override occupied ports
with `VILLAGE_TEST_POSTGRES_PORT`, `VILLAGE_TEST_MINIO_PORT`, and
`VILLAGE_TEST_MINIO_CONSOLE_PORT`. Override `VILLAGE_ENCRYPTED_PROJECT` to run
beside another checkout. An override never authorizes reuse: any stale container,
volume, or network causes refusal and prints an exact recovery command instead
of deleting it.

For encrypted backend development, prefer `make backend-dev`: it generates and
reuses a worktree-local KEK and JWT secret, starts PostgreSQL, MinIO, bucket
initialization, and the hot-reloading backend, and waits for the API. Use
`make backend-dev-seed`, `make backend-dev-down`, and the explicitly destructive
`make backend-dev-reset CONFIRM=1` for its lifecycle.

For the full application, copy the environment template:

1. Copy the env template and fill in the secrets (GitHub OAuth App credentials,
   `JWT_SECRET`, and a real KEK replacing the non-runnable placeholder). Generate
   a manual local KEK with `openssl rand -base64 32` and place it in the keyring JSON:

   ```sh
   cp .env.example .env
   ```

   The GitHub OAuth callback URL is `https://localhost:8443/api/v1/auth/github/callback`.

2. Bring the full stack up in Docker:

   ```sh
   make up        # build + start everything, detached
   # or
   make dev       # same, foreground with build logs
   ```

   On first start, the backend auto-applies migrations and MinIO provisions
   the `peasant-transcripts` bucket.

3. Hit the app:

   - Frontend (via Caddy): https://localhost:8443
   - API direct (via Caddy): https://localhost:8445
   - MinIO console: http://localhost:9001 (`minioadmin` / `minioadmin`)

Stop everything with `make down`.

### Useful Make targets

| Command       | What it does                                                    |
| ------------- | --------------------------------------------------------------- |
| `make up`     | Build and start all containers in the background                |
| `make dev`    | Same as `up` but foreground                                     |
| `make down`   | Stop all containers                                             |
| `make seed`   | Load sample users, transcripts, and collectives into Postgres   |
| `make migrate`| Run backend migrations against the running container            |
| `make sqlc`   | Regenerate Go types from `backend/internal/database/queries/`   |
| `make test`   | Run backend tests (`go test ./...` in `backend/`)               |
| `make build`  | Build the backend binary and the frontend production bundle    |
| `make backend-encrypted-test` | Run the isolated real PostgreSQL/MinIO encrypted backend proof |
| `make backend-encrypted-down` | Remove only the selected disposable proof namespace and volumes |
| `make backend-dev` | Start this worktree's persistent encrypted backend and print loopback URLs |
| `make backend-dev-seed` | Run all four encrypted seed and relationship stages |
| `make backend-dev-down` | Stop the backend stack while preserving data and local keys |
| `make backend-dev-reset CONFIRM=1` | Remove only this worktree's backend data and generated keys |

## Publishing transcripts

Transcripts are pushed into the village by the `peasant` CLI rather than the
web UI. After signing in at https://localhost:8443, visit `/publish` to mint
an API key, then push from your local store. See
[peasant-labs/peasant](https://github.com/peasant-labs/peasant) for the CLI
itself.

## Engineering references

[`docs/transcript-storage-security.md`](docs/transcript-storage-security.md) is
the canonical storage-security architecture and invariant reference, including
threat boundaries, envelope/read/write/migration/deletion/rotation/cutover flows,
and seven ASCII diagrams.
[`TESTING.md`](TESTING.md) - the comprehensive testing strategy: suites, house
rules, governance-era fixture/teardown rules, measured performance levers.
[`docs/database-invariants.md`](docs/database-invariants.md) - migrations,
triggers, the `app.*` GUCs, the audit table, and every other database/data-model
invariant.
[`docs/transcript-encryption-operations.md`](docs/transcript-encryption-operations.md)
- encrypted key maintenance, conservative object reconciliation, deletion
limits, and backup threats.
[`docs/transcript-encryption-cutover.md`](docs/transcript-encryption-cutover.md)
- the checked maintenance-window sequence, stabilization contract, credential
revocation, and permanent old-bucket deletion.
[`docs/railway-cloudflare-r2-activation.md`](docs/railway-cloudflare-r2-activation.md)
- the official-source-grounded Railway PostgreSQL and private Cloudflare R2
provisioning and activation procedure.

## Architecture & auth docs

The transcript **pull surface** (`/api/v1/pull/*`) and the **auth model** shared
between this server and the peasant CLI are documented canonically in the peasant
repo - the village backend points at them rather than duplicating them:

- **[docs/pull.md](https://github.com/peasant-labs/peasant/blob/develop/docs/pull.md)**
  - pull architecture: component map, the staged pull flow, idempotency / 304
  fast-paths, and the on-disk manifest.
- **[docs/auth.md](https://github.com/peasant-labs/peasant/blob/develop/docs/auth.md)**
  - the whole auth model, including the `canViewTranscript` (web) vs
  `canPullTranscript` (pull) allowed/denied matrix and its deliberate divergences
  (the `canPullTranscript` doc comment in `backend/internal/handler/pull.go`
  points here).

## Folder map

| Path                                | What lives here                                                   |
| ----------------------------------- | ----------------------------------------------------------------- |
| `backend/cmd/server/`               | Go HTTP server entrypoint                                         |
| `backend/internal/`                 | Handlers, config, database (migrations + sqlc), S3 client, etc.   |
| `frontend/src/app/`                 | Next.js App Router pages                                          |
| `frontend/src/components/`          | React components                                                  |
| `frontend/src/lib/`                 | API client, types, React Query hooks                              |
| `scripts/`                          | Seed data and dev helpers (see below)                             |
| `Caddyfile`                         | Reverse proxy config (`:8443` web, `:8445` direct API)            |
| `docker-compose.yml`                | Local dev stack (Postgres, MinIO, Caddy, backend, frontend)       |
| `flake.nix`                         | Optional Nix dev shell                                            |

## Demo data

`make seed` requires the running database, bucket, and configured KEK. It runs
four ordered stages: encrypted core records (`-seed-core`), core relationship
SQL (`scripts/seed.sql`), encrypted privacy records (`-seed-privacy`), then
privacy relationship SQL (`scripts/seed-privacy-features.sql`).

To exercise the curated-collective review flow as *yourself* (the dev user
created when you sign in via OAuth), additionally run
[`scripts/dev-pending-demo.sql`](scripts/dev-pending-demo.sql). It lands one
pending `transcript_shares` row into a collective you own so a review row
appears on `/groups/{id}`:

```sh
docker exec -i $(docker compose ps -q postgres) psql -U peasant -d peasant \
  < scripts/dev-pending-demo.sql
```

To exercise the "Pending member requests" UI in the right sidebar of a
collective you own, run
[`scripts/dev-pending-member-demo.sql`](scripts/dev-pending-member-demo.sql).
It inserts a single `group_members` row with `role='pending'` so an
approve/reject card appears for the owner on `/groups/{id}`:

```sh
docker exec -i $(docker compose ps -q postgres) psql -U peasant -d peasant \
  < scripts/dev-pending-member-demo.sql
```
