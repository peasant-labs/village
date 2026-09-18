/* Shared constants for the determinism shim, split out so both the shim and tests
 * can read them without importing the shim's browser-evaluated function. */
export const FROZEN_EPOCH_MS = Date.UTC(2024, 0, 1, 12, 0, 0)
export const PRNG_SEED = 0x9e3779b9