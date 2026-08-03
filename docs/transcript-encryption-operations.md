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
