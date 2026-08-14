# Deletion & Data Lifecycle Model - Design (v1)

Encrypted-body deletion, reconciliation, backup, and erasure limits are
canonical in [`transcript-storage-security.md`](transcript-storage-security.md).
This document remains the governance audit and broader user-data lifecycle
decision record.

**Status: PARTIALLY DECIDED.** The
governance **audit log survives deletion** - the model is **hard-delete +
durable audit**, *not* soft-delete: transcripts and users are still physically
deleted with FK cascades, and nothing in §4's soft-delete constraint list is
triggered. The **broader** soft-delete / PII-erasure model for transcripts and
users (below) remains **OPEN**.

**DECIDED (migration 026):**
- **The audit log has NO application writer.** All five event types are DB
  triggers on `transcripts` - `published` (AFTER INSERT), `license_changed` /
  `visibility_changed` / `governance_changed` (AFTER UPDATE, WHEN a governance
  axis moved - no-op suppression is the WHEN clause), `retracted` (BEFORE
  DELETE, the only mechanism that catches *every* delete path including the
  `DeleteAccount` `ON DELETE CASCADE`, migration 010, which runs no
  per-transcript Go).
- **Attribution is FAIL-CLOSED.** The actor comes from the txn-local GUC
  `app.actor_id` (set by the handler helpers `inTxAs` / `inTxAsSystem`); there
  is **no owner fallback** - a guessed attribution is fabricated evidence in a
  legal log, so a mutation without a declared actor **aborts**. Sanctioned
  non-user mutations (seeds, backfills, ops) attribute to the reserved SYSTEM
  actor `00000000-0000-0000-0000-000000000000` (`database.SystemActorID`). See §7.
- **The audit table is append-only at the DB layer** (block-trigger on
  UPDATE/DELETE with a deliberate, txn-scoped `app.audit_maintenance` escape;
  production additionally REVOKEs UPDATE/DELETE from the non-owner app role).
- **Writer semantics: one row per logical action**, carrying the full post-change
  snapshot. A single action moving BOTH axes is recorded as ONE `governance_changed`
  row (not two half-snapshots); per-axis types (`license_changed`,
  `visibility_changed`) remain for single-axis changes.

---

## 1. Problem

village is **hard-delete everywhere**: `DeleteAccount` (`auth.go:331`) and
`DeleteTranscript` (`transcripts.go:645`) issue raw `DELETE …`, and the deletion
story is built entirely on FK cascades (migration 010 made `transcripts.owner_id`
and `groups.created_by` cascade). There is no `deleted_at` / tombstone anywhere.

Two needs are in tension:
- **Audit / governance** wants *"who set what license/visibility on transcript T,
  and when"* to be answerable.
- **Users + GDPR** want real account/transcript deletion and PII erasure.

## 2. The pivotal decision

> **Must the governance/audit history SURVIVE deletion of the transcript and/or
> the account that it references?**

- **If NO** → keep hard-delete + cascade (status quo). Minimal work. Accept that a
  deleted transcript/account takes its governance history with it.
- **If YES** → you need references that stay valid after the referenced row is
  gone, which forces some form of **soft-delete + erasure**. That is a large,
  security-sensitive change - see the constraints in §4, which a pressure-test
  proved are non-negotiable.

Everything else in this doc only matters if the answer is YES.

### RESOLVED (for the audit log) - migration 026
The answer is **YES for the governance audit log**, done the *targeted* way rather
than via full soft-delete: 026's `transcript_governance_events_audit` keeps
`transcript_id` and `changed_by` as **retained values with NO foreign key** (only
`event_type` / `license_id` keep FKs to never-deleted reference tables). A
transcript or account can be hard-deleted and its audit events **persist** - a
self-contained, append-only, immutable legal record, without (yet) requiring the
broader soft-delete model.

Two caveats this introduces:
- **GDPR lawful basis.** Retaining `changed_by` (and any future actor snapshot) in
  a legally-mandated audit relies on the legal-obligation carve-out that overrides
  right-to-erasure. §7 below is the durable record of that lawful basis.
- **Actor-after-erasure.** The retained `changed_by` is a bare user id. If that user
  later FULLY erases their account, the id no longer resolves to a person ("user X"
  with nothing to look up). Preserving *who, by name, forever* would need either an
  actor-identity snapshot on the event (more PII to justify) or soft-deleted /
  tombstoned users (the broader project below). Deferred - flag if it becomes a hard
  legal need.

## 3. Options (for "audit survives")

| # | Option | Benefit | Cost |
|---|--------|---------|------|
| O1 | **Status quo** - hard-delete + cascade | trivial; nothing to build | audit not durable |
| O2 | **Soft-delete + inline-null erasure** | durable; GDPR-correct erasure | large blast radius (§4) |
| O3 | **Stripe-style** - soft-delete + stable IDs + separated `user_private_data` + loose FK coupling | clean erasure (drop the private row); flexible | PII-table refactor; JOINs in reads |
| O4 | **Crypto-shred** - encrypt PII, destroy per-user key on erasure | reversible until shred; auditable | needs an encryption layer village lacks |
| O5 | **GitHub-style Ghost reattribution** (complementary) | clean handling of *non-audit* refs (annotations) | loses original authorship unless an audit copy is kept |

**Rejected earlier (do not re-litigate):**
- *Snapshot/denormalized audit table* (copy `github_id`/username into the event) -
  duplicates PII; erasure forces scrubbing the copy anyway.
- *Two physical tables* (`current_`/`deleted_`) - a FK references one table, so
  moving a row breaks every `users` FK and forces `UNION`s. (Views over one table
  give the ergonomic for free.)
- *`RESTRICT` everywhere + tombstone invariants* - over-centralizes correctness
  into DB constraints; created real rigidity (broke hermetic test teardown).

## 4. Hard constraints - what a pressure-test proved any "audit survives" design MUST satisfy

These were verified against the real schema/code. Any soft-delete/erasure design
that ignores one of these is wrong:

1. **Erasure is illegal against the current schema as a naive `SET NULL`.**
   `users.github_id`, `github_username` (001) and `provider_user_id` (015) are all
   `NOT NULL`. Erasure must `ALTER … DROP NOT NULL` first, or erase to a sentinel
   (`'erased-' || id`). There is **no `username` column** - the canonical handle is
   `github_username`, uniquely indexed on `lower(github_username)` (018).
2. **`provider_user_id` is the re-login key.** `UpsertUserByProvider … ON CONFLICT
   (provider, provider_user_id)` (`users.sql:36`). Nulling `github_id` alone does
   **not** prevent re-login; login would resurrect the row. Erasure must scrub
   `provider_user_id`, and `UpsertUser*` must refuse to revive a `deleted_at` row.
3. **The read-path filter surface is ~18 queries across 7 areas, not ~5** -
   including the hand-built `ListTranscripts` SQL (`transcripts.go:871`, no
   `deleted_at`), the **separate** `canViewTranscript` web policy
   (`transcripts.go:1072`) vs `canPullTranscript` (`pull.go:76`), public profile,
   org member/discovery, group contributors/pending, `ListTranscriptCommits`, and
   attestation/annotation joins. Every `JOIN users` / `FROM transcripts` needs the
   filter.
4. **The leak window is between deactivation and erasure.** A deactivated-but-not-
   erased row has `deleted_at` set but PII intact, so a missed filter leaks **real
   PII**, not a placeholder. Every read path is security-critical.
5. **Auth must consult `deleted_at`.** JWTs are stateless - a deactivated user keeps
   access until token expiry; `GetAPIKeyByHash` has no `deleted_at` filter. Needs a
   `deleted_at`/`token_version` check or short TTL + denylist.
6. **S3 erasure must be reconciled.** Direct deletion is row-first and classifies
   commit and object-cleanup outcomes; failed or ambiguous cleanup emits reconciliation evidence. Account cascades
   (`transcripts.go:665`, return ignored). Commit + blob-delete-fail leaves PII
   content in S3 (erasure not real); blob-delete + rollback leaves a live transcript
   with a missing blob. Needs post-commit deletion + an idempotent reconcile state.
7. **`UNIQUE(owner_id, local_id)` is not partial** (001) - soft-delete blocks
   re-publishing a previously-deleted `local_id`. Need resurrect-on-republish or a
   partial unique index `WHERE deleted_at IS NULL`.
8. **Child-table PII** must be erased too: `transcript_commits.author_name/
   author_email` (020), `annotations.value/reason/annotator_name` (009),
   `attestations.note` (006). Annotations key on a **text `session_id`, not an FK**,
   so they orphan rather than cascade.
9. **Pull honors surviving shares** regardless of owner liveness
   (`canPullTranscript`) - deactivation must revoke/ignore `transcript_shares`.
10. **Ghost user** (if O5) must be seeded by a migration satisfying every
    `NOT NULL`/`UNIQUE`, set `is_discoverable = FALSE`, be excluded from counts, and
    - if authorship must stay auditable - preserve `original_owner_id`.
11. **Reconcile with migration 016** (`transcript_deletion_policy`,
    `user_choice`/`mandatory`) so account deletion and member-departure don't define
    two contradictory "what happens to my shared transcripts" rules.
12. **Pull stays 404, never 410** - the pull surface is deliberately
    404-never-403 for anti-enumeration; 410 Gone would re-leak existence.

## 5. Open decisions

- The pivotal §2 question (does audit survive deletion?).
- If yes: which of O2–O5 (or a blend); inline-null vs separated PII vs crypto-shred.
- Deactivation vs erasure: is there a reversible grace window, and what fires
  erasure (immediate, request, retention timer)?
- `annotations`/`attestations`/commits on erasure - erase body, reattribute, or
  delete?
- `groups`/`collective_repositories.linked_by` whose creator departs - reassign vs
  keep with a tombstoned reference.

## 6. Relationship to migration 026 - RESOLVED 2026-07-02

The §2 question is answered **YES**. Migration 026
makes the governance audit **survive deletion**: `transcript_id` and
`changed_by` are retained values with **no FK**, and the `BEFORE DELETE`
trigger appends the terminal `retracted` snapshot on every exit path, including
the `DeleteAccount` cascade. The governance log has **no application writer** -
all five event types are DB triggers; the table is append-only (block-trigger);
actor attribution is **fail-closed** (`app.actor_id` required; reserved
system-actor UUID for sanctioned non-user mutations - §7).

This is **hard-delete + durable audit**, *not* soft-delete: transcripts and
users are still physically deleted with FK cascades; nothing in §4's
soft-delete constraint list is triggered. Soft-delete/erasure of primary rows
(O2–O5 above) remains **unbuilt and undecided**.

## 7. Lawful basis & the `app.*` GUC registry

**Lawful basis (GDPR).** The audit log retains, past transcript deletion and
account erasure: `transcript_id`, `changed_by` (a bare user UUID), the
license/visibility snapshot, and timestamps. Retention rests on the
**legal-obligation carve-out** (GDPR Art. 17(3)(b)) over the right to erasure:
the log exists to answer *"who set what license/visibility on transcript T,
and when"* for the data-rights system, and an erasable audit is not an audit.
Account-erasure requests therefore do **not** scrub audit rows. The retained
`changed_by` is a bare id: after a full account erasure it no longer resolves
to a person (see the actor-after-erasure caveat in §2). Update this section if
counsel adopts a different basis or a retention limit.

**System actor.** `00000000-0000-0000-0000-000000000000`
(`database.SystemActorID`) is the reserved attribution for sanctioned non-user
mutations - seeds (`scripts/seed.sql`), backfills, operator runbooks. It
deliberately references no `users` row (the audit has no FK on actors) and is
reachable in code only through `inTxAsSystem`; `inTxAs` rejects non-Valid
actor UUIDs so a zero value can never silently impersonate the system.

**`app.*` GUC registry** (txn-scoped unless noted):

| GUC | Meaning | Set by |
|---|---|---|
| `app.actor_id` | WHO performs this transaction's transcript mutations (required - the audit triggers are fail-closed) | `inTxAs` / `inTxAsSystem`; `scripts/seed.sql` (session-scoped: autocommit psql makes `SET LOCAL` a no-op); sanctioned ops via `SET LOCAL` |
| `app.audit_maintenance` | Deliberate, statement-log-visible escape that permits UPDATE/DELETE on the append-only audit table within one transaction | test teardown (`purgeAuditRows`); operator runbooks, with a recorded reason |
