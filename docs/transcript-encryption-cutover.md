# Transcript encryption maintenance-window cutover

This is the destructive production procedure for moving Village to encrypted
transcript objects. It is a maintenance-window cutover, not a rolling or parallel
replacement. The old and encryption-capable writers must never overlap.
The design rationale and storage invariants are canonical in
[`transcript-storage-security.md`](transcript-storage-security.md); this file is
the production checklist.
The provider-specific prerequisite and activation commands are in
[`railway-cloudflare-r2-activation.md`](railway-cloudflare-r2-activation.md).

Every infrastructure action below is performed deliberately by an authorized
operator through the deployment, PostgreSQL, and object-provider control planes.
Application code must not reset databases, create or delete production buckets,
issue or revoke credentials, or advance this checklist automatically.

## Roles and evidence

Assign separate named owners for deployment, PostgreSQL, object storage, key
custody, validation, and final destructive approval. Record timestamps and
evidence in one change record. A person must explicitly approve each destructive
step; absence of evidence is a stop condition.

Record before the window:

- the intended full Git revision and backend/frontend image digests;
- the `org.opencontainers.image.revision` label from both production images;
- old deployment instance identities and database role;
- old bucket identity and bucket-scoped credential identities;
- new database role, new private bucket, and revision-scoped credential plan;
- PostgreSQL reset and backup disposition;
- the durable reconciliation log sink and settle window;
- current key-encryption-key versions and separately protected recovery material;
- smoke-test identities whose data can be destroyed after validation.

Run `scripts/verify-production-artifacts.sh <full-HEAD-revision>` from a clean
tracked checkout with the intended `NEXT_PUBLIC_API_URL`. It must build both
production entry points and prove the checkout, labels, and image commands agree.
This local proof complements, but does not replace, deployment-platform image
digest and rollout evidence.

## Go/no-go preflight

Do not begin the window unless all of these are true:

1. The consolidated backend build, unit race, real PostgreSQL/MinIO integration
   race, formatting, vet, sqlc regeneration/zero-diff, frozen frontend lint/build,
   and production artifact checks passed on the exact intended revision.
2. Restore owners have validated the required backups and documented that
   PostgreSQL, object, and key backups can recreate plaintext when reunited.
3. Reconciliation logs are durable beyond the settle window and on-call staff can
   query the authoritative primary.
4. The new object bucket is private, empty, dedicated to the new stack, and has
   no public or old-role access. Fresh bucket credentials will not be issued to
   the new deployment until old instances are at zero.
5. The encryption-capable artifact can run migration-only without JWT, object
   credentials, or transcript key-encryption-key settings, and serving fails
   closed without valid custody and object authority.
6. Roll-forward and abort owners agree on the first-encrypted-write boundary.

If any check is unavailable, stale, skipped, or ambiguous, postpone the cutover.

## Maintenance sequence

### 1. Close ingress and drain

1. Announce maintenance and stop new web, API, background, and administrative
   traffic before stopping processes.
2. Wait for requests and transcript mutations to finish within their documented
   timeout. Do not terminate a process while its database outcome is ambiguous.
3. Scale every old Village backend and maintenance worker to zero. Verify through
   the deployment control plane; a desired count alone is insufficient.
4. Query the authoritative PostgreSQL primary for sessions using the old
   application role. End only sessions whose ownership and transaction state are
   understood. Repeat until there are zero old sessions and no relevant
   transaction remains in flight.
5. Disable the old database credential and prove a new connection with it is
   denied. Keep the audit evidence.

Do not proceed while an instance, session, transaction, queue consumer, scheduled
job, or unknown actor can still write.

### 2. Reset and isolate storage authority

1. Perform the approved intentional PostgreSQL reset against the named production
   database. Verify it is the intended empty target before applying migrations.
2. Confirm the new private object bucket is empty and inaccessible to the old
   bucket credential.
3. Only after zero old instances and sessions are proven, issue fresh,
   bucket-scoped credentials to the encryption-capable deployment identity.
   Never place old and new bucket credentials in the same deployment.
4. Keep the old bucket private and unchanged during validation. It is rollback
   evidence until post-cutover stabilization authorizes permanent deletion.

### 3. Migrate without blob or key authority

Run the exact production backend artifact in migration-only mode:

```text
/server -migrate-only
```

JWT, object-store, and transcript key-encryption-key settings must be absent. The
process must apply the schema and exit without constructing blob jobs or opening a
listener. Verify the schema registry and encryption writer fence on the
authoritative primary. Any unexpected authority requirement, non-empty-transcript
refusal, migration ambiguity, or old-writer acceptance is a stop condition.

### 4. Start the encryption-capable stack closed to users

1. Configure the validated transcript keyring, fresh new-bucket credentials,
   revision-scoped database role, JWT authority, and other serving settings.
2. Start only the recorded backend and frontend image digests while public ingress
   remains closed.
3. Verify every running instance reports the intended image digest/revision and
   no old instance or old database session reappeared.
4. Verify health without treating a mount-only response as encryption proof.

### 5. Encrypted smoke and rollback boundary

Using disposable operator-owned data through mounted production paths:

1. Publish a transcript and confirm the database row has a complete encrypted
   descriptor and known plaintext identity.
2. Inspect the exact new-bucket object through provider tooling. It must be
   nondeterministic opaque bytes, `application/octet-stream`, and not valid
   transcript JSON or recognizable plaintext.
3. Read the transcript through the authenticated web path and verify plaintext,
   authorization, hash, and ETag behavior.
4. Pull it through the production pull path, then prove a matching conditional
   request returns `304` only after authorization.
5. Republish or rewrite disposable content and verify the new immutable generation
   remains readable without exposing internal descriptor fields.
6. Delete the disposable transcript. Verify row-first behavior and either
   confirmed object cleanup or a durable reconciliation-required event.
7. Repeat the relevant publish/read check for every supported visibility tier so
   all tiers prove the same encrypted store.

The first accepted encrypted write is the rollback boundary. Before it, operators
may abort and restore the old stack under the approved reset/restore plan. After
it, never route the new database or bucket back to an old plaintext-capable
binary. Resolve forward with encryption-capable artifacts or restore a compatible
encrypted-state backup.

### 6. Open traffic and stabilize

Open ingress only after every smoke assertion passes and all smoke data is
accounted for. During the recorded stabilization period:

- watch publish, republish, rewrite, web read, pull, conditional request,
  backfill, rewrap, and delete outcomes;
- alert on `transcript_blob_reconciliation_required`, custody failures, unknown
  key versions, authentication/tamper failures, descriptor/identity failures,
  object absence, writer-fence rejection, and old-role connection attempts;
- verify new objects remain ciphertext with the expected content type;
- confirm database sessions and deployed image revisions remain limited to the
  new stack;
- reconcile every uncertain or failed cleanup using
  `transcript-encryption-operations.md`;
- test backup capture and restore authority without exposing key material.

Any integrity failure, plaintext object, overlapping writer, missing event
retention, or unexplained uncertainty closes ingress. Do not delete evidence or
the old bucket while investigating.

## Credential revocation and old-bucket deletion

Permanent deletion is last and requires a fresh destructive approval. Proceed
only after the full stabilization period, no unresolved reconciliation or
integrity incident, successful encrypted backup/restore evidence, and explicit
sign-off from deployment, database, object-storage, and key-custody owners.

1. Reconfirm zero old instances, old database sessions, and relevant in-flight
   transactions on the authoritative primary.
2. Reconfirm the new stack uses only the fresh new-bucket credential.
3. Revoke every old database and old-bucket credential. Prove new connections and
   object requests with each revoked credential are denied.
4. Record the old bucket identity, retention/legal hold state, object count, and
   final approval. A hold or unknown policy stops deletion.
5. Permanently delete the old bucket and all of its contents through the object
   provider's audited administrative control plane. Require the provider to
   confirm the named bucket no longer exists; do not substitute lifecycle expiry
   or an empty listing for deletion.
6. Preserve the deletion audit evidence, but never credentials, keys, ciphertext,
   or plaintext in the change record.

This step deletes the named live old bucket. It does not prove erasure from
provider backups, replicas, logs, Peasant-local stores, already-pulled copies, or
other independently retained data.

## Completion record

The change record must contain exact artifact revisions/digests, gate results,
drain and zero-session evidence, credential denial evidence, migration result,
smoke outcomes, first-write time, stabilization interval, reconciliation status,
backup/restore limits, revocation results, old-bucket deletion confirmation, and
all named approvals. Any missing item keeps the cutover operationally incomplete.
