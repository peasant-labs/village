/**
 * Shared harness identity — the coding-agent wire values village stores on
 * `transcripts.model_provider`, plus the guard that narrows a free-text provider
 * string to one of them.
 *
 * The TYPE is the canonical `Provider` from the shared
 * `@peasant-labs/schema` package, so this module cannot drift from the wire contract. The
 * runtime list is tied to that type via `satisfies`, so a typo or non-harness
 * value fails to compile.
 *
 * These are the harnesses the fairtrade provider family (`ProviderTag` /
 * `ProviderName`) renders a real brand mark for; an out-of-set `model_provider`
 * falls back to a neutral tag at the call-site.
 */
import type { Harness as SchemaHarness } from "@peasant-labs/schema";

export type Harness = Extract<SchemaHarness, "claude-code" | "gemini-cli" | "codex" | "opencode" | "cursor">;

/** The harness wire values, in display order. Single source of truth — replaces
 *  the per-component copies that previously drifted. */
export const HARNESSES = [
  "claude-code",
  "gemini-cli",
  "codex",
  "opencode",
  "cursor",
] as const satisfies readonly Harness[];

/** Narrow a (possibly null) free-text `model_provider` to a brand-marked harness. */
export function isHarness(value: string | null | undefined): value is Harness {
  return value != null && (HARNESSES as readonly string[]).includes(value);
}
