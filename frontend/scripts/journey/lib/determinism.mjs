/* Deterministic page state for the journey harness.
 *
 * Ported from scripts/visual/determinism.mjs (the Puppeteer capture harness) so a
 * journey run is reproducible: it freezes the wall clock and replaces Math.random
 * and crypto randomness with a seeded PRNG. (Reduced motion is set at the context
 * level by playwright.journey.config.mjs `use.reducedMotion`.) Installed via
 * context.addInitScript, it runs before any document script on every navigation.
 *
 * App-agnostic on purpose: this is the module intended to move to the fairtrade
 * canonical harness and be vendored back into consumers, the same way
 * scripts/visual/surface-gate.mjs is vendored today.
 */
import { FROZEN_EPOCH_MS, PRNG_SEED } from './determinism-constants.mjs'

export { FROZEN_EPOCH_MS, PRNG_SEED }

export async function installDeterminism(
  context,
  { epochMs = FROZEN_EPOCH_MS, seed = PRNG_SEED } = {},
) {
  await context.addInitScript(
    ({ FIXED, SEED }) => {
      let s = SEED >>> 0
      const prng = () => {
        s |= 0
        s = (s + 0x6d2b79f5) | 0
        let t = Math.imul(s ^ (s >>> 15), 1 | s)
        t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296
      }
      Math.random = prng
      try {
        if (typeof crypto !== 'undefined' && typeof crypto.getRandomValues === 'function') {
          crypto.getRandomValues = (arr) => {
            const bytes = new Uint8Array(arr.buffer, arr.byteOffset, arr.byteLength)
            for (let i = 0; i < bytes.length; i++) bytes[i] = (prng() * 256) | 0
            return arr
          }
          crypto.randomUUID = () => {
            let out = ''
            for (let i = 0; i < 36; i++) {
              if (i === 8 || i === 13 || i === 18 || i === 23) {
                out += '-'
                continue
              }
              if (i === 14) {
                out += '4'
                continue
              }
              const r = (prng() * 16) | 0
              out += (i === 19 ? (r & 0x3) | 0x8 : r).toString(16)
            }
            return out
          }
        }
      } catch {
        /* a locked-down crypto; Math.random is still seeded */
      }
      const RealDate = Date
      class FrozenDate extends RealDate {
        constructor(...args) {
          if (args.length === 0) super(FIXED)
          else super(...args)
        }
        static now() {
          return FIXED
        }
      }
      FrozenDate.parse = RealDate.parse
      FrozenDate.UTC = RealDate.UTC
      window.Date = FrozenDate
      globalThis.Date = FrozenDate
    },
    { FIXED: epochMs, SEED: seed },
  )
}