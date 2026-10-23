/**
 * Shared harness identity: the coding-agent wire values village stores on
 * `transcripts.model_provider`, and the guard that narrows a free-text provider
 * string to one of them.
 *
 * Both the type and the guard are the shared `@peasant-labs/schema` package's
 * own, so this module cannot drift from the wire contract. fairtrade's provider
 * family (`ProviderTag` / `ProviderName`) takes that same type and draws a brand
 * mark for every member of it, so a harness the contract adds is marked
 * everywhere without a change here. An out-of-set `model_provider` falls back to
 * a neutral tag at the call site.
 */
import { isHarness as isSchemaHarness, type Harness } from "@peasant-labs/schema";

export type { Harness };

/** Narrow a (possibly null) free-text `model_provider` to a brand-marked harness. */
export function isHarness(value: string | null | undefined): value is Harness {
  return value != null && isSchemaHarness(value);
}
