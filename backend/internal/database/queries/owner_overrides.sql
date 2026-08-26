-- Owner corrections to derived, published metadata.
--
-- Every statement here is pinned to one (target_kind, field) pair supplied by the
-- caller. The table's CHECK constraints hold the full RESERVED menus so a later
-- field is a code change rather than a migration; narrowing those menus to the one
-- pair the application actually implements today is the handler's typed enum, not a
-- second database constraint. That is why these statements take the kind and the
-- field as parameters instead of hard-coding them: the closed set lives in exactly
-- one place in Go.
--
-- target_key is untyped TEXT by design — a project's key is its 64-hex hash, a
-- transcript's would be a UUID — so the caller validates the key's shape for the
-- kind it is writing before it gets here.

-- name: UpsertOwnerOverride :one
INSERT INTO owner_overrides (owner_id, target_kind, target_key, field, value)
VALUES (@owner_id, @target_kind, @target_key, @field, @value)
ON CONFLICT (owner_id, target_kind, target_key, field)
DO UPDATE SET value = EXCLUDED.value, updated_at = now()
RETURNING owner_id, target_kind, target_key, field, value, provenance, created_at, updated_at;

-- name: DeleteOwnerOverride :execrows
-- Returns the affected row count so the caller can tell "the correction was
-- removed" from "there was no correction to remove" without a preceding read.
DELETE FROM owner_overrides
WHERE owner_id = @owner_id
  AND target_kind = @target_kind
  AND target_key = @target_key
  AND field = @field;

-- name: GetOwnerOverride :one
SELECT owner_id, target_kind, target_key, field, value, provenance, created_at, updated_at
FROM owner_overrides
WHERE owner_id = @owner_id
  AND target_kind = @target_kind
  AND target_key = @target_key
  AND field = @field;
