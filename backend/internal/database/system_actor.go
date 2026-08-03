package database

import "strings"

// SystemActorID is the reserved actor UUID for sanctioned NON-USER mutations of
// transcripts: seeds (scripts/seed.sql), data backfills, and operator runbooks.
// The migration-026 governance-audit triggers are FAIL-CLOSED — every
// INSERT/UPDATE(governance-axis)/DELETE on transcripts must declare an actor via
// the txn-local GUC `app.actor_id` or the mutation aborts — so paths with no
// authenticated user attribute to this sentinel instead of guessing a person.
//
// It deliberately references no users row: transcript_governance_events_audit
// retains actor values with NO FK (the audit outlives accounts), so a sentinel is
// clean, filterable ("show me every system action"), and unforgeable from the user
// path — handlers can only reach it through inTxAsSystem, and inTxAs rejects
// non-Valid actor UUIDs so a zero value can never silently impersonate the system.
//
// Registry of app.* GUCs and the lawful-basis record: see
// docs/deletion-data-lifecycle-model.md §7.
const SystemActorID = "00000000-0000-0000-0000-000000000000"

// ReservedSystemUUIDPrefix is the first-80-bits-zero UUID text range
// (2^48 addresses) set aside for SYSTEM identities: SystemActorID today, plus any
// future named system sentinel. It is NOT a user-assignable range — user ids are
// fenced out of it so a person can never be minted with an id that collides with
// the system-actor attribution in the governance audit, which would let a user
// forge system actions or break "show me every system action" filtering.
//
// The reservation is enforced primarily at the storage boundary by a CHECK on
// users.id (see the 027 migration), which catches every insert path — the app
// default (gen_random_uuid() is v4, so structurally outside this prefix), raw-SQL
// seeds, and any future explicit/external-id import. IsReservedSystemID is the
// dependency-free Go mirror of that predicate for defense-in-depth at call sites.
const ReservedSystemUUIDPrefix = "00000000-0000-0000-0000-"

// IsReservedSystemID reports whether id falls in the system-identity range
// (ReservedSystemUUIDPrefix). It normalizes case (UUID text is case-insensitive)
// and does a pure prefix check — no uuid parsing, so it never panics on a
// malformed value and stays dependency-free.
func IsReservedSystemID(id string) bool {
	return strings.HasPrefix(strings.ToLower(id), ReservedSystemUUIDPrefix)
}
