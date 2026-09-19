/* VENDORED from fairtrade-design-system/scripts/journey/lib/determinism-constants.mjs.
   Byte-faithful except this banner. Do not edit here: edit the upstream file and
   re-vendor. scripts/journey/lib/vendor-guard.test.mjs fails when the bodies drift. */
/* Shared constants for the journey determinism shim.
 *
 * Split from determinism.mjs so tests can read the pinned values without
 * importing the browser-evaluated shim.
 *
 * App-agnostic: this is the canonical copy consumers vendor into their own
 * journey harness. See scripts/journey/README.md. */

export const FROZEN_EPOCH_MS = Date.UTC(2024, 0, 1, 12, 0, 0)
export const PRNG_SEED = 0x9e3779b9