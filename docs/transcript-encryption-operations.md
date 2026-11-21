# Transcript encryption operations

This runbook covers encrypted transcript-key maintenance, conservative object
reconciliation, deletion limits, and backup threats. It is a manual operator
procedure. Village does not provision credentials, mutate production
infrastructure, delete buckets, or reconcile retained objects automatically.
The architecture and invariant source of truth is
[`transcript-storage-security.md`](transcript-storage-security.md); this file
contains only operator procedure.
For fresh Railway PostgreSQL and private Cloudflare R2 provisioning, credential
mapping, and activation, use
[`railway-cloudflare-r2-activation.md`](railway-cloudflare-r2-activation.md).

## Safety boundary

Village encrypts each transcript body with a fresh data-encryption key and stores
only authenticated ciphertext in object storage. PostgreSQL stores the wrapped
key and object descriptor. The active key-encryption key is supplied to the
running process.

This separation protects against an object-store credential leak by itself. It
does not protect plaintext from a compromised running Village process. A party
that reunites a PostgreSQL backup, object-store backup, and the corresponding
key-encryption key can decrypt transcripts. Copies already pulled by users,
provider snapshots, replicas, logs, and independent backups are separate
retention domains.

Deleting an active bucket or database row is therefore not proof of universal or
cryptographic erasure. Record exactly which live resource was deleted and which
backup, replica, client-copy, and retention policies still apply.

## Required log retention

Before accepting the first encrypted write, route structured backend logs to a
durable sink that survives process, node, and deployment replacement. Access to
that sink must be limited and audited. Retain reconciliation events longer than:

1. the maximum request and transaction timeout;
2. the documented PostgreSQL failover and recovery bound;
3. the object-store consistency and incident-response bound; and
4. an explicit operational safety margin.

The deployment owner must record the resulting settle window. If any bound is
unknown, the settle window is unknown and the object must be retained.

The event name is `transcript_blob_reconciliation_required`. Current events
include `operation`, `transcript_id`, opaque `object_key`, transaction
`completion`, `meaning`, and `remediation`. They intentionally exclude body
bytes, ciphertext, wrapped or plaintext keys, credentials, titles, paths, and
raw provider errors. Treat the object key as operationally sensitive even though
it contains no transcript identity.

## Conservative reconciliation

An event means Village retained ciphertext because deleting it was not proven
safe. A missing row observed immediately after a commit error does not prove the
transaction rolled back. Delayed visibility or recovery can still make that row
authoritative.

For each event:

1. Preserve the complete structured event in the durable incident record.
2. Wait at least the recorded settle window. Restart the window after any
   failover, recovery, replica lag, or transaction-status uncertainty.
3. Connect directly to the authoritative writable PostgreSQL primary, not a
   replica, cache, analytics copy, or failover candidate.
4. In a read-only operator session, check every live reference to the exact
   opaque key:

   ```sql
   SELECT id
   FROM transcripts
   WHERE blob_key = :'object_key';
   ```

5. Inspect the primary's transaction/session inventory and deployment inventory.
   Confirm no transaction that began before or during the event can still commit
   a transcript mutation and no old or draining Village process remains. If a
   session cannot be attributed, it is relevant and the object stays retained.
6. If any row references the key, any transaction remains in flight, the primary
   cannot be established, failover is active, results are unreadable, or any
   evidence conflicts, stop. Record `retained: unknown or referenced` and do not
   delete the object.
7. Only when the authoritative primary returns no reference and no relevant
   transaction or process can still commit may an authorized operator delete the
   one exact object through the object provider's audited administrative tooling.
   Do not use prefix deletion, bucket emptying, or a guessed key.
8. Record the provider deletion result, primary query evidence, session evidence,
   timestamps, operator identity, and incident reference. A provider timeout or
   ambiguous result remains unresolved and must be checked again after a new
   settle window.

There is no ordinary case where uncertainty authorizes deletion. Unknown always
retains ciphertext.

## Bounded data-key rewrap

Rewrap changes only the wrapped data-encryption key and key version in
PostgreSQL. It must not fetch, rewrite, move, or delete object bytes, and must not
change the object key, plaintext hash, or plaintext size.

Before a batch:

1. Back up PostgreSQL and verify restore access to every old key-encryption-key
   version referenced by live rows. Losing an old version before its rows are
   rewrapped makes those rows unreadable.
2. Add and validate the new key version while retaining all referenced old
   versions. Do not remove the old version merely because it is no longer active.
3. Verify the selected production artifact revision and authority configuration.
4. Run the listener-free rewrap mode with a bounded limit from `1` through
   `1000`; the default bound is `100`:

   ```text
   /server -rewrap-transcript-keys -rewrap-limit <bounded-count>
   ```

5. Require the process to execute only the maintenance job, print its bounded
   result, and exit without opening an HTTP listener. If the final integrated
   artifact does not do all three, stop rather than substituting another flag or
   running an unbounded database update.
6. Review installed, stale, failed, and uncertain outcomes before starting the
   next batch. Stale rows may be retried from a fresh keyset page. Failed or
   uncertain outcomes require investigation; do not retire any old key version.
7. Sample authenticated reads through the mounted web and pull paths. Confirm
   object identity and bytes are unchanged using provider metadata or a recorded
   ciphertext digest from before the batch.

Retire an old key version only after an authoritative-primary query proves no
live row references it, all uncertain batches are resolved, backups that require
it have expired or been rewrapped under an approved process, and restore testing
confirms the remaining key set is sufficient.

## Deletion lifecycle

Direct transcript deletion is row-first: the key-bearing PostgreSQL row commits
its deletion before Village attempts object cleanup. A known rollback or
ambiguous commit retains ciphertext. A committed cleanup failure emits a
reconciliation-required event and follows the procedure above.

Account cascades remove key-bearing rows, but guaranteed physical cleanup of all
corresponding ciphertext requires durable cleanup tracking that is not part of
the current system. Do not claim that account deletion physically removed every
object. Handle discovered orphan ciphertext conservatively and retain it unless
the authoritative-primary and no-in-flight proof succeeds.

## Transcript content cache

Each Village process keeps a process-local cache of decrypted transcript
content for the positioned reads. It holds one entry per transcript, content
revision, model provider and harness. A main read and a history read of one
generation share the entry, and two viewers who open the same generation at the
same moment cause one object read and one decryption. The cache never decides
access: every read applies the viewer's access rule to a freshly read row before
it asks the cache for a byte.

Operating constants, all compiled into the binary:

- **Memory budget: 256 MiB** per process (`transcriptContentCacheBudgetBytes`).
  An entry is weighed at its plaintext plus an estimate of every index built
  from it: the served rows, the cooking verdicts, the segment refs and
  positions, the decoded native metadata, and the header and history
  summaries. The weight grows each time a partition (the main turns or one
  history section) is first indexed, so a transcript of many short turns or
  many history sections is weighed by its indexes and not only by its bytes.
  The budget is strict: the retained weight never exceeds it.
- **Eviction: least recently read first.** When retained entries are what
  stand between a read and the budget, the least recently read entries are
  dropped and the read proceeds. An entry whose indexes grow past the room left
  evicts colder entries; one that outgrows the whole budget leaves the cache,
  and the reads already holding it finish with their copy.
- **Heavy-work limit: k = 2** per process (`transcriptHeavyWorkLimit`). Cache
  loads (object read, decryption, structural pass), partition index builds and
  locator scans share these two slots. Concurrent first readers of one
  generation share one load, and concurrent first readers of one partition
  wait for the one build without taking a slot; a read of an entry already
  loaded and indexed takes no slot.
- **Oversized transcripts.** A transcript whose recorded size exceeds the whole
  budget is served and not retained. One such load runs at a time, and a
  second one is refused while it runs.
- **Idle lifetime: 15 minutes** without a read
  (`transcriptContentCacheIdleLifetime`). A background ticker sweeps once a
  minute; no request sweeps.

### What a `503` from a positioned read means

The header, directory, segment, window and locate routes answer `503` with
`Retry-After: 1` and no partial body only when the cache cannot admit the work
now, for one of two reasons, both of which clear when a load in flight ends:

- `weight`: the loads in flight have reserved so much of the budget that this
  load does not fit even with every retained entry evicted, or this transcript
  is larger than the whole budget while another such transcript is loading;
- `count`: both heavy-work slots are taken.

A full cache of retained entries is never a reason to refuse: those are
evicted. A viewer who may not read the transcript is answered `404` before
admission, whatever the load.

### Reading the refusal signal

Each refusal writes one structured log line at info level with the message
`transcript read admission refused`, `operation=transcript_read_admission`, the
matched `route` template (for example
`/api/v1/transcripts/{id}/segments/{index}`) and the `kind` (`weight` or
`count`). It never holds transcript content, identifiers, query values or
credentials. There is no in-process counter; count these lines in the log
pipeline by route and kind. A sustained rate of `count` refusals means more
concurrent first loads than two slots serve; a sustained rate of `weight`
refusals means loads near the budget are running side by side.

### Memory

The budget bounds what the cache retains. It does not bound a process's
resident memory while loads are in flight: a load holds its decrypted bytes,
and a build holds the partition's parsed turns until the build ends. With two
slots, at most two such transient copies exist at once. The design target is a
peak resident memory within 1.5 times what `GET /content` needs for the same two
concurrent first loads, inside the 32 GiB per-instance limit; that is checked
with benchmarks, not by a required test. The Go soft memory limit is separate
from this budget. An evicted entry stays in memory until the reads holding it
finish.

A process drops a transcript's entries when it publishes, rewrites, changes the
visibility of, unshares or deletes that transcript, and drops an owner's entries
on account deletion. It also drops a transcript's older entries when a read sees
the row at another revision or harness. A write by another process, or by the
operator repair mode, cannot reach this process's cache: the next read of that
transcript sees the new revision and drops the older generation, and an entry
for a deleted transcript is never served because the row read fails first.

After a database restore, restart every Village process so each starts with an
empty cache, then require readers to reload. A restore can rewind a revision,
and a cache entry keyed by a revision the restored database reissues with other
bytes would otherwise be served.

## Operator content repair

Use this only when a transcript's stored content is damaged or lost and the
ordinary paths cannot fix it: the object is missing, its bytes no longer
authenticate to the identity the row attests, or the row carries a malformed
identity. It is not for key rewrap, identity backfill, deletion, visibility, or
licensing. It is a manual, audited, one-transcript procedure.

### Recorded authorization

Record the authorization before the command runs, in the incident record. It
names who approved the repair, which transcript, which action, why, and the
evidence that justified it. For a restore, it also names the exact preserved
copy by object key, because the ledger does not store object keys.

The command takes three values from that record and writes them to the ledger
as given:

| flag | meaning |
|---|---|
| `-repair-actor` | the operator's own account UUID. The ledger derives the actor from the transaction, so it cannot be set by a statement. A UUID in the reserved range `00000000-0000-0000-0000-…` is refused. Never use a shared or system identity. |
| `-repair-authorization-ref` | the reference to the recorded approval, 1 to 256 characters. Blank is refused. |
| `-repair-reason` | one sentence, 1 to 1000 characters. Blank is refused. |

Both text values are stored verbatim and kept for the life of the transcript.
Never put transcript content, keys, secrets, paths, object keys, or identity data
in them, and keep them out of shell history you do not control.

### Before you run

1. Confirm the release is deployed and its migrations are applied. Repair mode
   never runs migrations. A database that lacks the repair ledger or the content
   state column stops the command before it changes anything. Fix that through the
   normal migration step, never by running a migration as part of the repair.
2. Provide the same authority a blob-processing job needs: `DATABASE_URL`, the
   `S3_*` settings, and `TRANSCRIPT_KEK_ACTIVE_VERSION` with
   `TRANSCRIPT_KEK_KEYRING`. Repair mode opens no HTTP listener, runs one repair,
   and exits.
3. Connect to the authoritative primary and read the transcript's current
   revision and state. Do not read them from a replica:

   ```sql
   SELECT content_revision, content_state FROM transcripts WHERE id = :'transcript_id';
   ```

4. Generate a new `-repair-id` UUID. A repair ID is used once. Any ledger row for
   it, applied, refused, or an unresolved attempt, makes it unusable for a second
   run.

### The command

```text
/server -repair-transcript-content \
  -repair-id <new-uuid> \
  -repair-transcript <transcript-uuid> \
  -repair-action <action> \
  -repair-actor <operator-account-uuid> \
  -repair-authorization-ref '<approval reference>' \
  -repair-reason '<one sentence>' \
  -repair-expect-revision <revision read from the primary> \
  [-repair-source-key transcripts/<uuid>.bin] \
  [-repair-reconciles <unresolved-attempt-repair-uuid>]
```

`-repair-transcript-content` cannot be combined with any other runtime mode, and
no `-repair-*` flag is accepted without it. `-repair-action` takes the kebab
spelling below, never the ledger's underscore spelling. A bad flag is refused
before any database or storage access and the message names the flag, never its
value.

`-repair-expect-revision` is the content revision you read. If the locked row is
at a different revision, nothing is attempted and the repair is refused as
`precondition_mismatch`. Read the revision again and decide afresh.
`-repair-source-key` is required for `restore-attested-generation` and refused for
every other action. `-repair-reconciles` is optional and is described under
unresolved attempts.

### The four actions

Each action reads the stored object at most once and acts only on what that read
authenticates. When it cannot show what it needs, it refuses and the transcript is
unchanged.

| action | use it when | requires | effect |
|---|---|---|---|
| `declare-unavailable` | the content is lost: the object is absent, or the provider cannot show it exists | content state `present` at the expected revision | state becomes `unavailable`. The identity pair and the revision do not change. Refused if the object authenticates. |
| `clear-malformed-identity` | a revision-one row carries a content hash that is not a valid hash | revision exactly 1, a non-NULL malformed hash, no usable identity, and an object that authenticates | the identity pair becomes empty. The revision does not change. No identity is invented. |
| `replace-third-value` | the object authenticates, but to a value that is neither the stored identity nor absent | a usable stored identity at the expected revision | the revision advances by one and the identity becomes the value the object authenticates to. Refused if the object authenticates to the stored identity. |
| `restore-attested-generation` | a preserved copy of the original ciphertext exists | a usable stored identity at the expected revision, and a preserved copy that authenticates to exactly that identity | the copy's bytes overwrite the transcript's own object and the state becomes `present`. The identity and the revision do not change. |

A restore changes the object, which no database transaction can undo. It
therefore commits an `attempted` ledger row first, writes the object, reads it
back, and writes one terminal row. It records `applied` only when the read-back
authenticates to the stored identity and the row still carries the same revision,
object key, and identity it had at the attempt.

### Output and exit status

The command prints stable lines on standard output and nothing else about the
transcript:

```text
repair_id=<uuid>
transcript_id=<uuid>
action=<ledger action>
phase=<applied|refused>
content_revision=<n>
failure_class=<class>     # only when refused
```

The exit status is zero only for `applied`. A refusal prints its lines and exits
non-zero, so a script cannot mistake it for a repair. A refusal is recorded in the
ledger under its repair ID. The command logs no transcript content, identity,
hash, key, or object key, and the output carries none.

| `failure_class` | meaning | next step |
|---|---|---|
| `precondition_mismatch` | the revision, the state, or the identity did not match what the action needs, or the object matched the stored identity, so nothing is wrong | read the row again; do not retry with the same ID |
| `object_mismatch` | the object, or the restore source, authenticates to a different identity | for a declare, the content is not lost: consider `replace-third-value`. For a restore, the copy is the wrong one: find the right copy |
| `object_uncertain` | absence or a bad descriptor, a key failure, or a failed authentication could not be told apart, or a restore write was not proved | investigate storage and key custody. Never infer absence from this |
| `guard_refused` | the restore source key is the transcript's own object key | name a separate preserved copy |
| `object_write_failed` | the provider refused the restore write | fix the provider, then use a new ID. The object is unchanged |
| `writer_fence_refused` | reserved in the closed set. No current action emits it | stop and report it |

### Fail-closed rules

- A repair that cannot write its ledger row does nothing. For a declare, clear, or
  replace, the row change and the ledger row commit in one transaction. For a
  restore, the attempt row commits before the object is touched, so a missing
  ledger or a failed attempt insert leaves the object unwritten.
- Repair never creates its own audit destination. See "Before you run".
- Repair holds the transcript's publish lock for the whole run, including a
  restore's object write and terminal row, and takes it before the row lock like
  every other writer. A concurrent publish or owner deletion of that transcript
  waits for the repair, and the repair waits for one already running.
- An account deletion cascades without taking that lock. If it removes the
  transcript during a restore, no terminal row is written, the restore deletes the
  object it wrote, prints `outcome=transcript_deleted` (with no `phase` or
  `failure_class`) and exits non-zero. A ledger write refused by the transcript's
  foreign key ends every action the same way. The transcript's ledger rows are
  removed with it, so there is nothing left to reconcile and no alert remains. If
  the object delete itself fails, the error says so: remove the leftover
  ciphertext with the storage provider's tooling.
- A repair never changes visibility, license, or governance state, and it writes no
  rewrite lineage and no governance event. Those records describe other things.
- The ledger is append-only. A row is never updated or deleted. A correction is a
  new repair ID.

### Finding unresolved attempts

A restore that crashed or lost its database after the attempt row committed leaves
an attempt with no terminal row. Run this on the authoritative primary on a
schedule that fits the recorded settle window, and alert on any row:

```sql
SELECT a.repair_id, a.transcript_id, a.action, a.prior_revision, a.recorded_at
FROM transcript_content_repairs a
WHERE a.phase = 'attempted'
  AND a.recorded_at < now() - :'settle'::interval
  AND NOT EXISTS (
      SELECT 1
      FROM transcript_content_repairs t
      WHERE t.phase <> 'attempted'
        AND (t.repair_id = a.repair_id OR t.reconciles_repair_id = a.repair_id)
  )
ORDER BY a.transcript_id, a.recorded_at, a.repair_id;
```

An attempt is resolved by a terminal row, applied or refused, that carries its own
repair ID or cites it. An attempt that only cites another attempt resolves nothing,
and a terminal row resolves only the attempt it cites.

### An ambiguous outcome

If the command fails with an error rather than printing a phase, or its process
dies, do not guess. Read the ledger for that repair ID on the authoritative primary,
after the recorded settle window, restarting the window after any failover:

```sql
SELECT seq, phase, action, failure_class, prior_revision, resulting_revision,
       reconciles_repair_id, recorded_at
FROM transcript_content_repairs
WHERE repair_id = :'repair_id'
ORDER BY seq;
```

| the ledger shows | meaning | do |
|---|---|---|
| no rows | nothing committed, and for a restore the object was not written | the same ID may be run again, or use a new one |
| one terminal row | the repair ended as the row says, whatever the caller heard | accept it. Do not run that ID again; a replayed ID is refused |
| an `attempted` row only | a restore's object write may or may not have happened | follow the next section. Never rerun that ID |

### Reconciling an unresolved attempt

1. Preserve the alert row and the ledger rows for the repair ID in the incident
   record.
2. Do not assume the object is old or new. Either may be true.
3. Run a new restore with a new `-repair-id`, the same preserved copy, the current
   `-repair-expect-revision`, and `-repair-reconciles <the unresolved attempt's
   repair ID>`. A restore writes the same authenticated bytes, so repeating it is
   safe. Every row of the new repair cites the old attempt, and its terminal row
   resolves the alert.
4. A reconciling repair that ends `refused` still resolves the alert, because it
   records a decision. It does not repair anything. Treat the transcript as
   unresolved: find the cause from the failure class, then either restore from a
   copy that authenticates or declare the content unavailable.
5. An attempt that is itself unresolved does not clear the one it cites. Resolve
   both.

### The preserved copy for restore

A restore needs a verbatim copy of the transcript's own ciphertext, made while it
was whole. It cannot be rebuilt from a hash, a title, a pull, or any plaintext.
The copy must:

- sit in the same bucket under its own opaque key of the form `transcripts/<uuid>.bin`,
  never under the transcript's own object key, which is refused as `guard_refused`;
- authenticate, under the transcript's stored descriptor, to exactly the identity
  the row attests. Any other result is `object_mismatch` or `object_uncertain` and
  nothing is written;
- stay in place and unchanged until the repair is `applied` and an authenticated
  read of the transcript succeeds. A restore never deletes or edits the copy.

Record the copy's key in the authorization record. Treat it as operationally
sensitive, like any object key.

### Never

- Never reconstruct, edit, or invent ciphertext or content, and never restore from
  a hash or from bytes that did not authenticate.
- Never update or delete a ledger row, disable its triggers, or write the ledger
  by hand. Record a new repair ID that cites the old one.
- Never change a transcript's content columns, state, or revision with SQL in place
  of a repair action.
- Never reuse a repair ID, and never run two repairs for one transcript at once.
- Never use a shared, system, or reserved-range actor.
- Never run a migration to create the ledger as part of a repair.
- Never put content, secrets, keys, paths, object keys, or identity data in
  `-repair-reason` or `-repair-authorization-ref`.
- Never delete the preserved copy before the restore is `applied` and verified.
- Never read a `refused` outcome, or a declared `unavailable` state, as proof that
  the object is gone or that data was erased. Absence stays unproven, and the
  conservative reconciliation rules above still apply.
