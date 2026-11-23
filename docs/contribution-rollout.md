# Rolling out contribution at library scale

This note is for the operator who deploys the paged contribute page, the
selection write and the two indexes that serve them. It states how the index
builds behave, what to do if a build would exceed the migration budget, the
order of the releases, which legacy routes must stay mounted, and how to roll
back. Every duration and size comes from
[`contribution-scale-benchmark.md`](contribution-scale-benchmark.md); nothing
here is an estimate.

## What ships

| Part | Repository | What it adds |
|---|---|---|
| Contract | `peasant-labs/schema` | The `paged` and `snapshot` views of `listContributableTranscripts`, `listContributionProjects`, `batchShareSelection`, and the contribution error codes |
| Backend | `peasant-labs/village` | The handlers for those operations, and migrations 055 and 056 |
| Frontend | `peasant-labs/village` | The paged contribute page, the project picker, the per-project snapshot, and the chunked submit of 250 sessions per request |

The migrations add two indexes on `transcripts` and nothing else: no table, no
column, no trigger. The selection is held in the browser. The server stores no
selection state, so a rollback has no selection data to migrate back.

| Migration | Index | Key |
|---|---|---|
| `055_contribution_family_index` | `idx_transcripts_owner_parent_session` | `(owner_id, parent_session_id)` |
| `056_contribution_activity_index` | `idx_transcripts_owner_last_activity` | `(owner_id, COALESCE(session_end, session_start, published_at, 'epoch') DESC, id)` |

## How the index builds behave

The backend applies pending migrations at startup, before it serves, and the
`-migrate-only` flag applies them and exits. Each migration runs as follows
(`runMigration` in `backend/internal/database/migrate.go`):

1. It opens one transaction, bounded by `migrationTimeout`, which is five
   minutes.
2. It takes the Village migration advisory transaction lock, so only one
   process migrates at a time.
3. It checks the exact version in `schema_migrations`, runs the body, records
   the version, and commits. A failure or a timeout rolls back the body and the
   registry row together.

Migrations 055 and 056 are separate transactions. Each runs a plain
`CREATE INDEX`, not `CONCURRENTLY`, because `CONCURRENTLY` cannot run inside a
transaction. A plain build takes a `SHARE` lock on `transcripts`:

- Reads continue during the build.
- Inserts, updates and deletes on `transcripts` wait until that migration
  commits. Publishing, republishing, visibility changes and contributions all
  write `transcripts`, so they pause.
- The build must first wait for every open write transaction on `transcripts`
  to finish. Writes that arrive while it waits queue behind it. The five-minute
  budget includes that wait.

The writer is blocked for the whole build, so the write pause is the build
duration. The benchmark measured it with a single-row writer running alongside
each build.

| Index | Rows | Build | Index size | Write pause |
|---|---:|---:|---:|---|
| 055 | 12,148 | 38.8 ms | 262,144 bytes | whole build |
| 055 | 1,000,000 | 658.7 ms | 20,242,432 bytes | whole build |
| 056 | 1,000,000 | 0.75 s | not measured | whole build |

Notes on the table:

- The 056 row comes from the follow-up measurement of the last-activity key.
  That measurement recorded the build time only. It did not record the size or
  a 12,148-row build.
- The earlier root-order candidate had the same column shape on a different
  key. It built in 41.7 ms (729,088 bytes) at 12,148 rows and in 753.4 ms
  (58,908,672 bytes) at 1,000,000 rows.
- At 1,000,000 rows the `transcripts` heap was 465,567,744 bytes. Building both
  root-order and project candidates in sequence took 2.7 s in total, which is
  0.9 percent of the budget.

All of these figures come from one laptop. Build time grows with the table and
depends on the host, so measure the build on your own data before you deploy
(see the next section).

## Before the deploy: measure the build

1. Restore a recent copy of the production database to a scratch PostgreSQL
   instance of the same major version.
2. On the copy, record the size of `transcripts`:

   ```sql
   SELECT count(*) AS rows, pg_size_pretty(pg_relation_size('transcripts')) AS heap
   FROM transcripts;
   ```

3. Time the migration from `backend/` at the release revision, pointed at the
   copy:

   ```sh
   time DATABASE_URL="<copy DSN>" go run ./cmd/server -migrate-only
   ```

   The command applies every pending migration, so it also applies any other
   migration that ships in the same release.

4. Record the duration in the deployment record. The longer of the two builds
   is the longest write pause the deploy causes. The two pauses come one after
   the other, not at the same time.

If the measured time is well under five minutes, deploy normally. Writes pause
for about the measured time, plus the time the build waits for open write
transactions.

## If the build would exceed the budget

A build that runs longer than five minutes is cancelled and rolled back. The
backend then fails to start, and the old instances keep serving. Choose one of
the following and record the choice in the deployment record:

1. **Raise the migration budget.** Change `migrationTimeout` in a separate,
   reviewed change that ships before this release.
2. **Deploy in a maintenance window.** State the maximum write pause, which is
   the measured build time plus a margin. Then run `-migrate-only` against
   production inside the window, before the new backend starts.

Do not change either migration to `CREATE INDEX CONCURRENTLY`. A concurrent
build cannot run inside the migration transaction. It would need a different
migration design, and that design was rejected.

## Release order

```
1. contract merge and tag         peasant-labs/schema
2. village re-pin                 peasant-labs/village  (go.mod and the frontend package)
3. backend reads and selection write
4. frontend browse and selection
5. frontend submit
6. index migrations 055 and 056
7. legacy inventory and this note
```

Steps 2 to 7 merge as one Village pull request, so they ship in one build. The
order constrains how that build is deployed:

- **The contract tags before the re-pin.** Village serves and enforces the
  contract module's own bytes. The new operations exist in Village only when
  its pin names a contract release that declares them.
- **The backend deploys before the frontend.** The new page asks for the
  `paged` and `snapshot` views, the project picker and the selection write. A
  backend without them answers the page with a flat or grouped `200` or a `413`.
  The page treats any of those as an incomplete deployment: it shows no rows
  and says so. It does not fall back to the legacy read.
- **The migrations run when the new backend starts,** before it serves. The new
  reads work without the indexes, but at a large library they are slow. A
  1,000,000-row library's first page took about 3.5 s without 056 and 26 ms with
  it.

## Compatibility window

These routes stay mounted, and their behavior stays as it is:

- `GET /api/v1/groups/{id}/contributable`, with no `view` (the flat list) or
  with `view=grouped`. Above the contributable row limit, both still answer
  `413`.
- `POST /api/v1/groups/{id}/shares`, with its rule that an empty selection
  means the whole project, and with its boolean consent.
- `GET /api/v1/transcript-groups/{groupId}/members`, with its legacy payload.
- The `/api/v1/collectives` alias, which mounts every route above under
  `/api/v1/collectives/{id}/...` as well.

External clients of these shapes are unknown, so the window does not end with
this release. It ends only by a named decision:

- `peasant-labs/village#289` removes the deprecated `/groups` mounts.
- `peasant-labs/schema#158` removes the deprecated operations from the
  contract.

Neither may land before this release ships. The legacy-caller inventory is
[`contribution-legacy-callers.md`](contribution-legacy-callers.md).

## Rollback

### Application

Deploy the previous build of the backend and the frontend. The new views, the
project picker and the selection write disappear. The previous contribute page
works again, because the legacy reads and the legacy write never changed.

The previous build does not refuse a database that carries migrations 055 and
056. It skips versions it does not know, and it never uses the indexes. You
can leave the indexes in place after an application rollback.

### Contract pin

The Village build and its contract pin roll back together. A build that uses
the new operations cannot run on an older contract module. Its handlers use
the new types, and the document it serves would not declare the routes it
mounts. Rolling back the pin alone is therefore not possible. Roll back the
whole build, which carries the previous pin. The contract release itself stays
published. A release is never withdrawn, and other consumers may already pin
it.

### Indexes

Drop the indexes only if they are the problem, for example if their write cost
is too high. Roll back the application first, because the new reads depend on
the indexes for speed. Then, in one transaction:

```sql
BEGIN;
SET LOCAL lock_timeout = '5s';
SELECT pg_advisory_xact_lock(24287440042805061);  -- the Village migration lock
DROP INDEX IF EXISTS idx_transcripts_owner_last_activity;
DELETE FROM schema_migrations WHERE version = 52;
DROP INDEX IF EXISTS idx_transcripts_owner_parent_session;
DELETE FROM schema_migrations WHERE version = 51;
COMMIT;
```

- The two `DROP INDEX` statements are the bodies of the down migrations. The
  migration runner has no down command, so they are run by hand.
- The advisory lock keeps a starting backend from migrating at the same time.
  The key is `migrationAdvisoryLockKey` in `migrate.go`.
- Deleting the registry rows lets a later deploy of this release build the
  indexes again. If the rows stay, that deploy skips both migrations and serves
  without the indexes.
- `DROP INDEX` takes an `ACCESS EXCLUSIVE` lock on `transcripts`. It blocks
  reads and writes for as long as the drop takes, which is brief. It must wait
  for every open transaction on `transcripts`, and queries that arrive meanwhile
  queue behind it. `lock_timeout` ends that wait after five seconds and rolls
  back, so the drop cannot stall the service. If it times out, retry it.

### Selection data

There is none on the server. A selection lives in the browser, and each chunk of
the submit is an ordinary contribution: a share attempt in the existing ledger.
Contributions made through the new page stay valid after a rollback. The
previous page and the review page read them like any other contribution.

## How this relates to the migration invariants

Migrations 055 and 056 follow the rules in
[`database-invariants.md`](database-invariants.md) §1 and add no exception to
them:

- They are numbered up and down pairs, immutable once shipped.
- They run in the same bounded transaction, under the same advisory lock.
- Their files contain no transaction control.
- Their registry rows are pinned by the registry test.

The section on the paged contribution view in that document states what each
index serves. It also states the invariant that 056's key must stay identical,
character for character, to the page query's sort expression. A difference
leaves the index usable for the owner range but not for the order, and the
query falls back to a full sort without any error. An integration test plans
the page query against the index and fails if that happens.

## What this change does not fix

- **Organization membership is read at sign-in.** A person removed from an
  organization keeps the access it granted until they next sign in
  (`peasant-labs/village#286`). The selection write reads the stored membership
  record again inside its transaction, so a removal recorded there refuses the
  write.
- **A session's last activity can change during one browse.** The page orders
  roots by last activity, so a session that is updated while the person pages
  can move: it can be listed twice or not at all in that traversal. The page
  shows "loaded N of M matching" and offers a refresh. A per-project snapshot
  is the exact set at the moment it is read.
- **The legacy write keeps its boolean consent.** Old clients that call
  `POST /api/v1/{collectives,groups}/{id}/shares` keep its race: what they
  consented to and what is written can differ if the project changes in
  between. The new page uses the selection write, which names every session.
- **Other callers of the legacy reads.** The collective library, the review
  page, my-shares, home, explore, profile and project pages, and the helper
  member replay still load their whole scope before they page.
  [`contribution-legacy-callers.md`](contribution-legacy-callers.md) lists them
  and proposes the follow-up work.
