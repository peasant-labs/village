/* Vendoring guard for the journey shared layer.
 *
 * scripts/journey/lib holds the app-agnostic pieces vendored from
 * fairtrade-design-system/scripts/journey/lib. The vendored body (everything
 * after the VENDORED banner) must be byte-identical to the canonical copy, so a
 * shared fix cannot land on one side only.
 *
 * The guard needs the fairtrade checkout the copy was taken from. It skips when
 * FAIRTRADE_CHECKOUT is unset (local runs without a checkout), and fails when it
 * is set to a checkout whose body has drifted or is missing.
 */
import { existsSync, readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const HERE = dirname(fileURLToPath(import.meta.url))
const CHECKOUT = process.env.FAIRTRADE_CHECKOUT
const VENDORED = ['determinism-constants.mjs', 'determinism.mjs', 'assertions.mjs']

const stripBanner = (source) => source.replace(/^\/\* VENDORED from [\s\S]*?\*\/\n/, '')

describe.skipIf(!CHECKOUT)('journey lib vendoring', () => {
  for (const name of VENDORED) {
    it(`${name} matches the fairtrade canonical body`, () => {
      const upstream = join(CHECKOUT, 'scripts/journey/lib', name)
      expect(existsSync(upstream), `upstream not found: ${upstream}`).toBe(true)
      const vendored = stripBanner(readFileSync(join(HERE, name), 'utf8'))
      expect(vendored).toBe(readFileSync(upstream, 'utf8'))
    })
  }
})