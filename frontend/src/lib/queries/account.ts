import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { VillageUserStats } from "@peasant-labs/schema";
import { api } from "../api";
import type { AsJSONNumber } from "../types";

/**
 * The signed-in person's own totals, as `GET /users/me/stats` serves them.
 *
 * The contract types the three 64-bit sums as bigint; the JSON body carries
 * ordinary numbers, which is what this app reads.
 */
export type UserStats = AsJSONNumber<
  VillageUserStats,
  "total_duration_ms" | "total_tokens" | "total_turns"
>;

// Every "me" read below keys on the handle it was read for: the route is always
// "me", but a sign-out followed by another sign-in must not serve the previous
// account's answer from the cache.
function meKey(what: string, username: string) {
  return [what, username.toLowerCase()] as const;
}

/** The home page's totals. Withheld until the caller's handle is known. */
export function useMyStats(username: string, enabled: boolean) {
  return useQuery({
    queryKey: meKey("my-stats", username),
    queryFn: () => api<UserStats>("/users/me/stats"),
    enabled: enabled && username !== "",
  });
}

/**
 * One key as `GET /auth/api-keys` lists it. The route predates the contract,
 * so this shape is declared here rather than generated.
 */
export interface ApiKey {
  id: string;
  key_prefix: string;
  label: string | null;
  created_at: string;
  last_used: string | null;
  revoked_at: string | null;
}

/** The label the CLI's sign-in gives every key it makes. */
export const PEASANT_CLI_KEY_LABEL = "peasant-cli";

/**
 * The keys a peasant sign-in made that still work.
 *
 * Each `peasant village login` makes one key, so this counts sign-ins rather
 * than machines: signing in twice on one computer counts twice. The settings
 * page says "computers" because that is what a person recognises; it adds no
 * machine identity of its own.
 */
export function activePeasantKeys(keys: readonly ApiKey[]): ApiKey[] {
  return keys.filter((key) => key.label === PEASANT_CLI_KEY_LABEL && key.revoked_at == null);
}

/** The most recent `last_used` among the keys, or null when none was ever used. */
export function newestUse(keys: readonly ApiKey[]): string | null {
  let newest: string | null = null;
  let newestAt = Number.NEGATIVE_INFINITY;
  for (const key of keys) {
    if (key.last_used == null) continue;
    const at = Date.parse(key.last_used);
    if (Number.isNaN(at) || at <= newestAt) continue;
    newest = key.last_used;
    newestAt = at;
  }
  return newest;
}

export function apiKeysKey(username: string) {
  return meKey("api-keys", username);
}

export function useMyApiKeys(username: string, enabled: boolean) {
  return useQuery({
    queryKey: apiKeysKey(username),
    queryFn: () => api<ApiKey[]>("/auth/api-keys"),
    enabled: enabled && username !== "",
  });
}

/**
 * A sign-out-everywhere that stopped part way. The keys before `revoked` are
 * gone; the one at `revoked` failed, and the rest were not attempted.
 */
export class RevokeKeysError extends Error {
  constructor(
    readonly revoked: number,
    readonly total: number,
    readonly failure: unknown,
  ) {
    const reason = failure instanceof Error ? failure.message : String(failure);
    super(
      revoked === 0
        ? `peasant could not be signed out: ${reason}`
        : `peasant was signed out on ${revoked} of ${total}, and the next one failed: ${reason}`,
    );
    this.name = "RevokeKeysError";
  }
}

/**
 * Revokes each given key with its own `DELETE /auth/api-keys/{id}`, one after
 * another. The first failure stops the run and says how far it got, so a
 * person is never told everything was signed out when some keys still work.
 * The key list is read again afterwards either way, so the page counts what
 * is really left.
 */
export function useRevokeApiKeys(username: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (ids: readonly string[]) => {
      for (let i = 0; i < ids.length; i++) {
        try {
          await api(`/auth/api-keys/${encodeURIComponent(ids[i])}`, { method: "DELETE" });
        } catch (cause) {
          throw new RevokeKeysError(i, ids.length, cause);
        }
      }
    },
    onSettled: () => qc.invalidateQueries({ queryKey: apiKeysKey(username) }),
  });
}
