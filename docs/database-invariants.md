# Database & Data-Model Invariants

The single reference for village's database intricacies: the migration system's
rules, the governance/licensing data model, the audit triggers, the `app.*`
GUCs, and the invariants tests are allowed to rely on. If code or a migration
changes any of these, this file changes in the same commit.

Companion documents: [`deletion-data-lifecycle-model.md`](deletion-data-lifecycle-model.md)
(deletion semantics + §7 lawful basis), the checklists in `AGENTS.md`
(*Adding a license*, *Adding a visibility tier*), and the header comments of
`backend/internal/database/migrations/026_license_governance.up.sql`.
Cross-system storage security, including envelope, read/write, rotation, and
cutover invariants, is canonical in
[`transcript-storage-security.md`](transcript-storage-security.md).
Provider provisioning and the current combined migration/runtime database-role
boundary are documented in
[`railway-cloudflare-r2-activation.md`](railway-cloudflare-r2-activation.md).

---

## 1. Migration system

- **Numbered pairs, immutable once shipped.** `NNN_name.up.sql` / `NNN_name.down.sql`
  in `internal/database/migrations/`. A migration that has been applied to any
  shared environment is never edited - fixes ship as a NEW migration.
- **Version-keyed application.** `RunMigrations` (`migrate.go`) skips a migration
  iff its integer version exists in `schema_migrations`. It never inspects
  content - which is why a same-numbered rewrite is invisible to it (the burned-025
  lesson below).
- **Body and registry are atomic.** Every migration runs in one bounded pgx
  transaction under the namespaced Village advisory transaction lock. The exact
  version check, SQL body, and one `schema_migrations` insert commit together.
  Migration files contain no transaction control. A failed body or registry
  insert rolls both back; an ambiguous commit is retried rather than repaired by
  hand, and the locked exact-version check safely skips or reapplies it.
- **Registry invariants live in ONE test.** `migrations_registry_test.go` pins
  strictly-increasing versions and `highest == wantLatestMigration` (bump the
  const in the same commit as a new migration). Per-migration test files assert
  only their own migration; prior migrations' tests are never retrofitted.
- **Version gaps are legal** (19 and 25 are absent). **025 is deliberately
  unregistered and must not be reused.** Migration 026 converges databases that
  may already carry a `version=25` registry row; that row is left untouched.
- **026 is a convergent fixpoint.** All DDL is guarded (`IF NOT EXISTS` /
  `DROP … IF EXISTS`; triggers use `DROP TRIGGER IF EXISTS` + `CREATE` since
  `CREATE TRIGGER` has no guard) and **ordered drop-first**: old-generation
  objects are dropped before any final-design object whose NAME they hold -
  index and relation names share one schema-wide namespace, so gen-1's
  `idx_gov_events_transcript` must be freed before the guarded audit-index
  create, or the guard silently skips.
- **Guarded DDL's compensating control** is
  `TestMigration026_ConvergesAllEnvClasses`: fresh / gen-1 / gen-2 scratch
  databases must reach the IDENTICAL schema at catalog granularity
  (columns+defaults, named constraints, indexes, triggers, function bodies).
  Guards silently skip; only this diff proves convergence.
- **A superseding migration's down.sql reverses its FINAL design only** - it
  does not resurrect the superseded generations (documented asymmetry).
- **028 adds owner-scoped association identity.** `transcript_associations` is
  keyed by `(owner_id, association_id)` and has a second unique key on
  `(owner_id, transcript_id, observed_commit_sha)`. The composite transcript FK
  proves an owner cannot bind an association to another owner's transcript; the
  schema-derived opaque-ID shape check and immutable-update trigger make the
  ledger append-only. Exact replay is a
  no-op; rebinding or adding a second ID for the same relationship is rejected
  before the publish mutation. `annotations.target_association_id` references
  that owner-scoped key and may be populated only for an otherwise-empty
  `target_kind='association'` arm.
- **029 makes annotation dedup owner-scoped.** `content_hash` describes a
  producer's canonical annotation payload and does not contain the Village
  account id. `annotations_owner_content_hash_key` is therefore
  `UNIQUE(owner_id, content_hash)`: two owners may persist byte-identical
   annotations, while a replay for one owner updates only that owner's row.
- **030 gives Village-created manual labels an exact transcript locator.**
  `annotations.target_transcript_id` is nullable and references `transcripts(id)`
  with `ON DELETE CASCADE`. New manual session/entry labels always write it,
  even when their annotator merely views a public or shared transcript. Their
  Village-only, versioned hash includes that UUID, so one viewer can label two
  same-`local_id` transcripts independently while a repeat on one exact target
  remains idempotent. Existing human manual rows are backfilled only when their
  historic entry/session local id resolves to exactly one transcript in the full
  catalog, so a viewer-owned label may resolve to a publisher-owned transcript.
  Zero or duplicate local-id candidates remain NULL and retain the old
  owner-scoped local-id lookup. Pushed schema annotations never receive this
  local-only hash or locator.
- **031 activates encrypted transcript descriptors on a clean database.** It
  takes `ACCESS EXCLUSIVE` on `transcripts`, refuses any existing row, and adds
  non-null `wrapped_data_key`, closed `encryption_algorithm`, and positive
  `key_version` state plus the key-version index. Its writer trigger requires
  transaction-local `app.transcript_writer_version=1` for INSERT, DELETE
  (including account cascades), and updates to storage, encryption, hash, or size
  fields. Metadata-only updates remain outside this compatibility fence. The
  marker is compatibility metadata, not authentication or authorization.
- **032 records accepted publication currency.**
  `transcripts.accepted_request_operation_fingerprint` is nullable for publications made
  before the authoritative protocol and otherwise stores one lowercase SHA3-256
  operation fingerprint. Publish writes it atomically with metadata, content
  hash, commits, and association appends, then rereads the complete association
  ledger in canonical order for the response.

## 2. Licensing data model

- **`licenses`** is the home of license OBLIGATIONS (BCNF: `id` is the key and
  determines every column). The **id set's source of truth is the
  `github.com/peasant-labs/schema` module** (`schema.AllLicenses`); the SQL seed
  repeats it as literals and the integration drift guard pins seed == `AllLicenses`
  exactly. The
  **obligation booleans are village-owned** (peasant deliberately carries only
  the id menu) and every seeded row's full tuple is pinned by test - they drive
  the future collective join-consent screen, so a wrong boolean is a wrong
  legal UX.
- **Obligation model ceiling:** `attribution_required` / `share_alike` /
  `commercial_ok` cover the CC menu ONLY. `proprietary` / `unlicensed` / `*-ND`
  require new axes (e.g. `redistribution_ok`, `derivatives_ok`) -
  an `ALTER`, never a reinterpretation of the existing three.
- **No permissiveness rank / no computed "meet".** Licenses form a PARTIAL order
  on independent axes (CC-BY-NC and CC-BY-SA are incomparable); any scalar total
  order is incoherent. Collective licensing is **decided by the collective at
  creation and consented to at join time** - a human decision, never a computed
  resolution.
- **`transcripts.license_id`** is a nullable `TEXT REFERENCES licenses(id)`.
  `NULL` means *unset/legacy* - nothing was granted; default copyright applies.
  Nothing is ever retroactively licensed; owners license/re-license via the
  audited PATCH path. **A granted license can never be CLEARED** (un-licensing
  is blocked at the PATCH gate with a 400 - CC grants are irrevocable for prior
  recipients; changing to a different menu license stays allowed): through the
  application, `NULL` is reachable only by never having licensed (a sanctioned
  raw-SQL ops correction under a declared actor can still clear, and is audited
  as `license_changed` - mirroring §5's append-only escape framing). Partial index
  `idx_transcripts_license … WHERE license_id IS NOT NULL`.
- **The license menu is enforced on two surfaces**, no longer three: (a) the
  **DB seed** (village-owned; pinned by the migration-026 seed guard + obligation
  tuple pinning against `schema.AllLicenses`), and (b) the **contract module's
  single byte-source** - served via `schema.VillageAPISpecJSON()` and enforced via
  `schema.ValidatePublishRequest`, which read the SAME in-module bytes, so served
  vs enforced cannot drift and follow the `go.mod` pin automatically. The two
  former vendored-bytes guards + the served-spec baseline are RETIRED: the module
  makes that drift class structurally impossible. What still needs a village-side
  guard is the cross-repo 422 error body (`TestValidatePublish_BadLicense_ErrorBodyPinsMenu`)
  and the served-from-module + contract-version pins (`openapi_test.go`).

## 3. Visibility

- The closed set `public | private | shared` is CHECK-constrained in TWO
  immutable migrations (001 `transcripts`, 026 audit table) and mirrored by the
  Go `dbVisibility*` constants (`pull.go`) and the PATCH gate.
- **Widening is a NEW migration that ALTERs BOTH checks** plus the Go constants
  and PATCH switch. A partial widen is not merely incomplete - the audit
  triggers write `NEW.visibility` into the audit table's CHECK, so an
  unrecognized value **blocks the mutation**. See the AGENTS.md checklist.

## 4. Governance event taxonomy

- **`governance_event_types`** is a reference table (`id` = the event name)
  WITH a Go constant mirror (`internal/database/governance_events.go`) - by
  decision: governance UIs operate on these codes in both languages; they are
  data model, not an implementation detail. The integration drift guard pins
  seed == `AllGovernanceEventTypes`, so adding a type is one `INSERT` + one Go
  constant, in lockstep.
- Five types: `published`, `license_changed`, `visibility_changed`,
  `governance_changed` (BOTH axes moved in one action - recorded as ONE row so
  a reader never sees a half-snapshot), `retracted`.

## 5. The audit table (`transcript_governance_events_audit`)

- **Append-only, trigger-written, deletion-surviving.** One row = one typed
  event carrying the full post-change policy snapshot (`license_id`,
  `visibility`) + the actor + `effective_at`.
- **No application writer exists.** All five event types are written by the
  DB triggers (§6). Handlers never INSERT into this table.
- **Deletion survival:** `transcript_id` and `changed_by` are RETAINED VALUES
  with **no FK** - the audit outlives transcripts and accounts (hard-delete +
  durable audit; NOT soft-delete). `event_type`/`license_id` keep FKs to the
  never-deleted reference tables. Lawful basis for retention: lifecycle doc §7.
- **Ordering is `seq`** (`BIGINT GENERATED ALWAYS AS IDENTITY`), never
  `effective_at`: `now()` is transaction time, so same-txn events share a
  timestamp. "Latest" = `MAX(seq)` per transcript. Same-instant events are
  legal by design (no `UNIQUE(transcript_id, effective_at)`).
- **Tamper resistance:** `trg_governance_audit_immutable` (BEFORE UPDATE OR
  DELETE) raises unless the transaction set the maintenance escape (§7). The
  trigger - not just REVOKE - because **REVOKE cannot bind the table owner**,
  which is exactly how integration tests connect. Production hardening adds
  `REVOKE UPDATE, DELETE … FROM <app_role>` on a non-owner app role.
- Audit-table INSERTs are unrestricted (append-only ≠ insert-blocked); the
  fail-closed actor rule applies to the `transcripts` triggers that produce
  the rows, not to direct historical appends (which only tests perform).

## 6. Audit triggers (migration 026)

| Trigger | Timing | Writes | Notes |
|---|---|---|---|
| `trg_audit_transcript_publish` | AFTER INSERT ON transcripts | `published` | **No WHEN clause** - every transcript INSERT requires an actor |
| `trg_audit_transcript_governance` | AFTER UPDATE … WHEN (license/visibility `IS DISTINCT FROM`) | `license_changed` / `visibility_changed` / `governance_changed` | The WHEN clause IS the no-op suppression - a title-only or content_hash-only UPDATE never fires it and needs no actor |
| `trg_audit_transcript_retract` | BEFORE DELETE ON transcripts | `retracted` (from `OLD.*`) | Fires per cascaded row too - the only mechanism covering `DeleteAccount`'s `ON DELETE CASCADE` (migration 010), which runs no per-transcript Go |
| `trg_governance_audit_immutable` | BEFORE UPDATE OR DELETE ON the audit table | - (RAISE) | §5 tamper resistance |

- All three functions are `SET search_path = pg_catalog, public`.
- **Fail-closed attribution:** both mutation-side functions read
  `NULLIF(current_setting('app.actor_id', true), '')::uuid` and **RAISE if
  NULL** - there is NO owner fallback (a guessed attribution is fabricated
  evidence in a legal log; a path that forgot the actor plumbing must fail
  loudly). The account-deletion cascade inherits the deleting transaction's
  GUC, so it stays correct.

## 7. `app.*` GUC registry

GUCs are **context-passing, not credentials** - any session can set its own;
their trustworthiness comes from the server code that sets them after
authenticating. Both are custom Postgres parameters read via
`current_setting(name, true)`.

| GUC | Meaning | Scope | Set by |
|---|---|---|---|
| `app.actor_id` | WHO performs this transaction's transcript mutations. REQUIRED by the fail-closed audit triggers for INSERT / governance-axis UPDATE / DELETE on `transcripts`. | Txn (`set_config(..., true)` / `SET LOCAL`) - except seeds (below) | `Handler.inTxAs` (authenticated user; rejects non-Valid UUIDs) / `Handler.inTxAsSystem` (the ONLY route to the system actor); `scripts/seed.sql` sets it SESSION-scoped (`set_config(..., false)`) because `make seed` pipes autocommit psql where `SET LOCAL` is a no-op; sanctioned ops via `SET LOCAL` in a transaction |
| `app.audit_maintenance` | Deliberate, statement-log-visible escape permitting UPDATE/DELETE on the append-only audit table within one transaction. | Txn only | Test teardown (`purgeAuditRows`, the sole sanctioned cleaner); operator runbooks, with a recorded reason |
| `app.transcript_writer_version` | Compatibility marker proving a transcript storage mutation uses the encryption-aware descriptor shape. Exact current value: `1`. It is not authorization. | Txn only (`set_config(..., true)` / `SET LOCAL`) | Every encryption-aware create, republish, rewrite, hash backfill, rewrap, direct delete, and account-delete transaction |

- **System actor:** `00000000-0000-0000-0000-000000000000`
  (`database.SystemActorID`) - the reserved attribution for non-user mutations
  (seeds, backfills, runbooks). It references no `users` row on purpose and is
  unforgeable from the user path: `inTxAs` rejects non-Valid actor UUIDs (a
  zero value would otherwise render as the system id), and `inTxAsSystem` is
  the only code path that sets it.
- **Reserved system-identity range:** the entire
  `00000000-0000-0000-0000-*` prefix (first 80 bits zero - `SystemActorID` plus
  any future named system sentinel; `database.ReservedSystemUUIDPrefix` /
  `IsReservedSystemID`) is fenced out of `users.id` by a CHECK
  (`users_id_not_system_reserved`, migration 027), so no insert path - the
  `gen_random_uuid()` default, raw-SQL seeds, or a future explicit/external-id
  import - can mint a user that collides with the system-actor attribution.
- **Transaction plumbing:** `internal/handler/tx.go` is the ONE transaction
  scaffold (`inTxRaw` private / `inTxAs` / `inTxAsSystem`). Every handler
  mutation of `transcripts` goes through it; under the fail-closed triggers a
  bypass cannot commit a governance mutation at all.

## 8. Related standing invariants

- **`users → transcripts` is `ON DELETE CASCADE`** (migration 010); groups
  cascade from their creator. Account deletion is a DB-level cascade - any
  future per-transcript bookkeeping must therefore live in triggers, not Go.
- **`UNIQUE(owner_id, local_id)`** on transcripts (001) - `local_id` is the
  peasant session id; publish upserts by this key (ID-only existence probe +
  locked narrow pre-image inside the publish transaction).
- **Re-publish never changes visibility**, and an absent CLI license preserves
  the stored one - pinned from the FOR-UPDATE pre-image
  (`pinRepublishGovernance`) inside the same transaction.
- **Content replacement is private-before-write.** Publish, owner PATCH, and
  sharing serialize on the owner/local-session advisory-lock key. Before a
  re-publish stages new S3 bytes, any public or shared row is moved to private
  in an actor-attributed transaction. Public widening remains a separate owner
  PATCH.
- **Content replacement uses immutable, content-addressed objects.** Publish
  uploads to an owner/transcript/content-hash key, then swaps `blob_key` in the
  same database transaction as the authoritative receipt. A database failure
  deletes only the uncommitted staged object, leaving the prior row and object
  paired. After commit, deletion of the superseded object is best effort and
  cannot invalidate the new durable pairing. Exact replay reuses the current
  content-addressed key without overwriting or deleting it.
- **Legacy re-publish invalidates successor currency.** A validated legacy
  update clears `accepted_request_operation_fingerprint` in the same transaction
  as its metadata replacement. Its separately recomputed content hash therefore
  cannot coexist with stale evidence from an earlier authoritative operation.
- **content_hash updates fire no governance trigger** (WHEN-false), but migration
  031's compatibility fence requires writer marker `1` because identity must stay
  coherent with the encrypted descriptor. The explicit
  `-backfill-content-identity` mode processes all pending rows per invocation and
  validates PostgreSQL, KEK, and S3 authority,
  authenticates ciphertext through the production blob store, and exits without
  starting an HTTP listener.
- **Published associations are not governance events.** The association ledger
  is immutable relationship identity, not a policy axis: publishing it shares
  the authenticated actor transaction with its transcript but adds no audit row
  or event type. Deleting a transcript cascades its associations and their
  association-target annotations; the transcript retract trigger remains the
  sole lifecycle evidence writer.
- **Transcript identity, not `local_id`, resolves annotation discovery.** A
  local session id is unique only within an owner. The list, pull, count, and
  annotation-currency queries all start from the transcript UUID; session and
  entry arms additionally require matching owner ids, association arms require
  `transcript_associations.transcript_id = transcripts.id`, and Village-created
  manual labels use `annotations.target_transcript_id = transcripts.id`. This
  keeps same-local-ID records from crossing owners in lists, counts, or
  skip-gates while still letting a non-owner's label follow the shared transcript
  it actually viewed.
- **Pull authorization is enforced at the query level.** The live pull-surface
  SQL (`ListPullableTranscripts` and the skip-gate's
  `ListPullableTranscriptsByIDs`) encodes `canPullTranscript`'s policy directly
  in the query, keyed on the transcript id (PK), never on `content_hash`. A
  requested id the caller may not pull is simply ABSENT from the result set, so
  the batch skip-gate withholds that id's currency by omission rather than
  emitting a per-id denial or existence oracle.
  `TestPull_AuthorizationEquivalence_RealPostgres` pins the membership
  predicates equal to `canPullTranscript` over the full divergence table (the
  policy matrix itself is owned by the external auth docs), so the pinned places
  (one Go predicate, two membership queries: the pull list and the skip-gate
  batch, plus `CountPullableTranscripts` for count-consistency) cannot drift.
- **sqlc generated code is never hand-edited** (`internal/database/sqlc/`);
  `emit_db_tags: true` exists so hand-built pool queries can use name-addressed
  scanning (`pgx.RowToStructByName`) - SELECT-list/scan-list pairs are a
  forbidden pattern (the `license_id`-returned-null regression class).

## 9. Test-suite invariants

- **Integration tests are hermetic.** Fixture writes to `transcripts` declare
  the system actor - WHERE depends on the package's isolation model: the
  `internal/database` helper (`insertTranscript`) sets it on the CALLER's
  transaction (migration tests run one rolled-back outer tx, so that helper
  must never open its own), while the `internal/handler` helpers
  (`execAsSystem`, `pullInsertTranscript`, `countsInsertTranscript`)
  deliberately own their transactions
  (those suites commit real rows and clean up after). Teardown order: delete
  transcripts as system → delete users (cascade now fires nothing) →
  `purgeAuditRows` LAST (entity deletes re-append `retracted` rows). Teardown
  errors are loud, never `_, _ =` swallowed.
- **Handler UNIT tests never see the triggers** - the `pool == nil` seam runs
  the mock Querier with no database, so fail-closed/audit semantics are
  integration-only-testable; unit tests assert validation, routing, and
  error-mapping.
- **The convergence test's template cache**: `village_conv_base` (001–024,
  content-addressed by a sha256 of the embedded prefix SQL in
  `conv_base_meta`) deliberately persists between runs - the ONE sanctioned
  scratch-DB survivor. The three per-class clones are dropped every run.
- Statements that MUST fail run inside savepoints (`tx.Begin` on a `pgx.Tx`)
  so the outer transaction survives; a fail-closed UPDATE test must actually
  MOVE a governance axis (a same-value update is WHEN-false and passes for the
  wrong reason).
