# Transcript storage security architecture

This is the canonical architecture and invariant reference for encrypted
transcript storage. It works backward from outcomes: an authorized reader gets
the exact authenticated transcript, an object-store credential leak reveals no
plaintext, a failed distributed write does not destroy readable data, and an
operator can explain what was retained or erased. Database details remain in
[`database-invariants.md`](database-invariants.md), procedures in
[`transcript-encryption-operations.md`](transcript-encryption-operations.md), and
production activation in
[`transcript-encryption-cutover.md`](transcript-encryption-cutover.md). Fresh
Railway PostgreSQL and private Cloudflare R2 provisioning is in
[`railway-cloudflare-r2-activation.md`](railway-cloudflare-r2-activation.md).

## Outcomes, threat model, and non-goals

Village encrypts every public, shared, and private transcript body before it
reaches R2 or MinIO. A leaked object-store credential exposes authenticated
ciphertext, opaque generation names, and object metadata, but not plaintext or a
usable DEK. PostgreSQL holds the wrapped DEK, algorithm, key version, object key,
and plaintext identity. The process environment currently holds the KEK set.

This does not protect against a compromised running Village process. Current
Railway service-environment access or live-process compromise exposes the
database URL, R2 credential, and KEK together: they are distinct credentials,
but not separate runtime trust boundaries. It also does not protect against a party
that combines PostgreSQL, object storage, and the relevant KEK, or plaintext
already pulled by a user. It does not hide PostgreSQL metadata, provide
client-side encryption, guarantee physical cleanup after account cascades, or
prove universal erasure across backups and replicas. Vault is a future,
separately operated custodian, not a name for putting another service beside the
same compromised process.

### Trust-boundary topology

```text
 user / peasant
       |
       | authenticated HTTP, plaintext only after authorization
       v
+---------------- Village process ----------------+
| scan -> hash -> AES-GCM -> response allowlist    |
|          |          |             |              |
|          |          +---- KEK API +----+         |
+----------|-------------------------|----|---------+
           |                         |    |
           v                         v    v
   +---------------+       +-----------+  +------------------+
   | PostgreSQL    |       | process   |  | future Vault     |
   | wrapped DEK,  |       | env KEK   |  | separate auth,   |
   | descriptor,   |       | current   |  | custody, audit   |
   | identity      |       +-----------+  +------------------+
   +---------------+
           | exact object key
           v
   +-------------------+
   | R2 / MinIO        |
   | opaque AES-GCM    |
   | ciphertext only   |
   +-------------------+

 Compromise of R2 alone: no plaintext and no DEK.
 Compromise of the live process: plaintext is in scope.
 Backups inherit the trust boundary of every component they copy.
```

## Envelope and stored state

Go standard-library `crypto/rand`, `crypto/aes`, and
`cipher.NewGCMWithRandomNonce` implement one AES-256-GCM envelope. There is no
Tink dependency, custom envelope framing, visibility-specific algorithm, or
plaintext compatibility path. Each write creates a random 256-bit DEK and an
opaque immutable generation key. The canonical transcript UUID is
domain-separated AAD:

```text
 body AAD = "village:transcript-body:v1:" + transcript UUID
 DEK  AAD = "village:transcript-dek:v1:"  + transcript UUID
```

### Envelope write and stored split

```text
 plaintext transcript
      |                    random 32-byte DEK
      +--> SHA3-256,size           |
      |                            v
      +--> AES-256-GCM(body AAD) -> ciphertext
      |                                  |
      |                                  v
      |                     R2/MinIO opaque generation.bin
      |
      +--> AES-256-GCM KEK(DEK AAD) -> wrapped DEK
                                           |
                                           v
 PostgreSQL row --------------------------------------------------+
 | transcript UUID | opaque object key | wrapped DEK              |
 | algorithm=aes-256-gcm-random-nonce-v1 | positive key version   |
 | plaintext SHA3-256 | plaintext size                         |
 +---------------------------------------------------------------+

 Neither store alone has plaintext plus the authority to recover it.
 Swapping a body or wrapped DEK to another transcript fails UUID-bound AAD.
```

`BlobDescriptor` and `ContentIdentity` have private validated state. Wrapped
key access returns a clone; plaintext ownership moves once from successful AEAD
open to the caller. A loaded identity is strongly typed `Known` or `Pending`.
Known means canonical lowercase SHA3-256 plus non-negative size and is validated
before a 200 response. Pending exists only for an older row with NULL
`content_hash`; it still requires authenticated ciphertext, emits no ETag, and
can be repaired by descriptor-and-size CAS. New writes are always known.

## HTTP boundary and authorized reads

PostgreSQL deliberately retains internal fields. The single transcript-metadata
projection is `toTranscriptResponse`, an explicit 61-field allowlist used by publish, detail,
update, list, and group. Group adds only three owner fields. Encryption metadata,
`blob_key`, source paths, project paths, worktrees, and future columns do not
appear automatically. Authorization decides whether a row is visible; it never
turns a database row into an implicit response schema.

### Authorized read state flow

```text
 request
   |
   v
 load full row -> authorize route ------------------ denied/deleted -> 404
   |
   v
 known identity + matching If-None-Match ---------- authorized -> 304
   |
   v
 fetch opaque generation -> unwrap DEK -> GCM open -> hash/size validate
   |                                  |
   |                                  +-- integrity/provider error -> fail, no retry
   |
   +-- typed generation missing only
          |
          v
      reload full row once -> reauthorize -> rebuild identity and ETag
          |                     |                    |
          |                     |                    +-> fresh 304
          |                     +-> denied/deleted -> 404
          +-> descriptor changed -> one authenticated fetch
              unchanged/second miss -> actionable failure, no loop

 A 200 starts only after complete authentication and identity validation.
 Stale successful headers are never carried across the reload.
```

## Writes across PostgreSQL and object storage

R2 and PostgreSQL have no shared transaction. Publish, republish, and canonical
rewrite upload a fresh generation, then atomically install the complete
descriptor and identity. Republish, rewrite, identity repair, and rewrap use
exact compare-and-swap. Process locks are only an optimization.

### Distributed write and commit outcomes

```text
 upload candidate generation
          |
          v
 PostgreSQL transaction: actor + writer marker + exact descriptor/CAS
          |
          +-- committed ----------> candidate authoritative
          |                          clean only proven superseded objects
          |
          +-- known rollback/stale -> candidate cannot be referenced
          |                          delete unique candidate best effort
          |
          +-- commit ambiguous ----> RETAIN candidate and prior generation
                                     emit reconciliation-required evidence
                                     immediate missing/different row does not
                                     prove rollback and cannot authorize delete

 create | republish | rewrite | identity repair | rewrap | row-first delete
 all preserve the same completion distinction with operation-specific results.
```

Immutable generations prevent a failed write from corrupting the prior readable
body. Ambiguous outcomes retain all candidates, superseded objects, and deletion
targets. Reconciliation events contain operation, opaque key, stable transcript
identity, typed completion/observation, timestamp, and safe correlation only.
They never contain plaintext, ciphertext, DEKs, KEKs, credentials, titles, paths,
or raw provider errors.

## Migration 031 and the writer fence

The migration runner places each migration's exact-version check, SQL body, and
registry insert in one bounded PostgreSQL transaction under a namespaced advisory
transaction lock. Migration 031 takes `ACCESS EXCLUSIVE`, refuses any existing
transcript row, installs non-null encryption descriptor constraints and the
writer trigger, then commits with its registry row.

### Migration and fence activation

```text
 old writers drained                         cooperating migrators
 old DB credential blocked                         |
          |                                         v
          +--> advisory xact lock <---------- wait / serialize
                         |
                         v
                 exact version absent?
                         |
                         v
              ACCESS EXCLUSIVE transcripts
                         |
              non-empty -- yes --> rollback all, actionable stop
                         |
                        no
                         v
       descriptor columns + constraints + index + writer trigger
                         |
                 registry row in same tx
                         |
                      commit
                         v
 encryption-aware mutations set app.transcript_writer_version=1
 old INSERT/DELETE/storage mutations without marker -> rejected
 safe metadata-only updates -> outside compatibility fence
```

The marker is compatibility metadata, not authentication. It protects live row
coherence. Fresh, bucket-scoped credentials issued only to the verified new
revision separately prevent old processes from uploading plaintext orphan
objects.

## Deletion, reconciliation, backups, and erasure

Direct deletion is row-first: the writer-marked transaction removes the row and
wrapped DEK, then a known commit permits ciphertext deletion. Rollback or
ambiguity retains ciphertext. A cleanup failure emits reconciliation evidence.
Account cascades remove key-bearing rows but do not yet guarantee physical object
cleanup.

Operators wait the documented settle window, query the authoritative writable
primary for the exact object key, and prove no relevant transaction can still
commit. Referenced, replica-only, failover-affected, unreadable, or uncertain
state stays retained. Only no reference plus no in-flight transaction permits
one exact administrative deletion. Prefix deletion is forbidden.

PostgreSQL, object-store, and KEK backups can recreate plaintext when reunited.
Provider snapshots, replicas, logs, Peasant-local stores, and pulled copies are
independent retention domains. Therefore deletion evidence names the exact live
resource and remaining retention policy; it never claims universal
crypto-shredding or guaranteed erasure.

## Identity repair and key rotation

Identity repair is explicit and listener-free. Each invocation currently processes
all pending rows; it is not bounded. It authenticates the
current descriptor, computes plaintext hash and size, and installs both only when
the transcript ID, complete descriptor, NULL hash, and NULL-safe prior size still
match. A concurrent write wins safely.

Rewrap changes only the wrapped DEK and key version. It never downloads or
rewrites the body and never changes object key, hash, or size.

### Rotation and rewrap

```text
 keyring before: active=1, keys={1}
          |
 add key 2, retain key 1, set active=2
          |
          v
 bounded keyset page: rows with key_version < 2
          |
          v
 unwrap wrapped DEK with key 1 + transcript UUID AAD
          |
 wrap same DEK with key 2 + transcript UUID AAD
          |
 exact DB CAS: wrapped DEK/version only
     |          |             |
 installed     stale         ambiguous/failure
 continue      fresh page     retain both KEKs, investigate
          |
  authoritative primary proves zero live references to key 1
          |
  retain key 1 for every unexpired backup that may reference it

 R2 ciphertext bytes remain byte-identical throughout rewrap.
```

The live primary cannot prove what backup copies reference, and no backup-rewrap
tool exists. Retain every old KEK until all backups that may reference it expire.
The current environment keyring must retain every referenced version. Future
Vault Transit can implement `KeyCustodian` and migrate wrapped DEKs without
changing body ciphertext. It requires separate workload identity, policy, audit,
unseal custody, backup, and recovery operations before it becomes a stronger
trust boundary.

## Maintenance-window activation

Production activation is manual and never performed by application code.

### Maintenance-window cutover

```text
 verify exact images, gates, backups, log retention, approvals
                          |
 close ingress -> drain requests -> zero old processes/sessions/transactions
                          |
 block old DB credential -+-> intentional PostgreSQL reset
                          |             |
                          |       migration 031, KEK/S3-free
                          |             |
 fresh private bucket + revision-scoped credentials
                          |
 configure KEK -> start verified new stack with ingress closed
                          |
 mounted smoke: publish/read/pull/304/republish/rewrap/delete
 inspect raw object: opaque application/octet-stream ciphertext
                          |
 first encrypted write: old-binary rollback boundary
                          |
 open ingress -> stabilize -> reconcile every uncertainty
                          |
 fresh destructive approval -> revoke old credentials
                          |
 permanently delete named old bucket and record retention limits

 Any overlap, plaintext object, integrity failure, stale evidence, or unknown
 transaction closes ingress and stops progression.
```

## Actionable failures and evidence

Errors state what failed, why, where, at which stage, the impact on callers and
prior readability, and the exact safe repair. HTTP responses do not expose raw
provider errors. Logs never print key material or credentials.

Required evidence includes fixture row guards, Go race/build/format/vet, real
PostgreSQL and MinIO tests with machine-checked rejection of every skip event,
fresh migration 031 and writer-fence proof, mounted encrypted lifecycle proof,
two zero-diff sqlc generations, frozen frontend lint/build, artifact provenance,
and clean resource teardown. For a developer, `make backend-encrypted-test`
creates a uniquely namespaced disposable stack, initializes its bucket, runs the
real no-skip integration gate with a process-scoped test-only KEK, and removes
only that project's containers, network, and volumes.
