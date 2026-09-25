/* VENDORED from fairtrade-design-system/scripts/journey/lib/assertions.mjs.
   Byte-faithful except this banner. Do not edit here: edit the upstream file and
   re-vendor. scripts/journey/lib/vendor-guard.test.mjs fails when the bodies drift. */
/* Journey assertion helpers: the design-system and accessibility contract a
 * mounted surface must satisfy.
 *
 * App-agnostic: this is the canonical copy consumers vendor byte-for-byte into
 * their own journey harness. See scripts/journey/README.md. A vendored body must
 * therefore load from nothing but its own directory and the two declared
 * dependencies below: no app module, no app target registry, and no private
 * workspace package. An app that renders one theme as an absent or empty
 * attribute owns that normalization in its own adapter or target, never here;
 * do not add a second copy of that rule below. */
import { expect } from '@playwright/test'
import { AxeBuilder } from '@axe-core/playwright'

export const DEFAULT_AXE_TAGS = ['wcag2a', 'wcag2aa']

/**
 * Exact field set of one compact axe report, declared once so a consumer
 * writing the report into its own artifact never re-declares the shape. Every
 * scan this module returns carries exactly these keys, whatever scope it ran
 * over, so two scopes of one row stay comparable in a single artifact.
 * @type {readonly string[]}
 */
export const AXE_RESULT_FIELDS = Object.freeze(['tags', 'violations', 'incomplete', 'passes'])

/**
 * Run axe-core over the current page and return a compact, JSON-serializable
 * report. An optional root scopes the run to one subtree; the returned shape is
 * the same either way, so a page-wide scan and a row-scoped scan are
 * interchangeable in one artifact.
 * @param {import('@playwright/test').Page} page
 * @param {object} [options] scan options
 * @param {string[]} [options.tags] axe tags, pinned to the shared default
 * @param {string} [options.root] selector the run is scoped to
 */
export async function scanAxe(page, { tags = DEFAULT_AXE_TAGS, root } = {}) {
  const builder = new AxeBuilder({ page }).withTags(tags)
  const results = await (root === undefined ? builder : builder.include(root)).analyze()
  return {
    tags,
    violations: results.violations.map((v) => ({
      id: v.id,
      impact: v.impact,
      nodes: v.nodes.map((n) => n.target),
    })),
    incomplete: results.incomplete.map((v) => v.id),
    passes: results.passes.length,
  }
}

/** Violations that block a merge: critical and serious impact. */
export function seriousViolations(scan) {
  return scan.violations.filter((v) => v.impact === 'critical' || v.impact === 'serious')
}

/**
 * The document element must carry the requested theme (the app's own control
 * writes it). The retry budget is Playwright's own `expect.timeout`: the
 * assertion polls the rendered attribute until it settles, so a caller may
 * assert immediately after a theme toggle, navigation, or client-side state
 * change that writes data-theme. A consumer that renders dark as an absent or
 * empty value normalizes the raw value in its own adapter first.
 * @param {import('@playwright/test').Page} page
 * @param {string} theme the rendered data-theme value
 */
export async function expectTheme(page, theme) {
  await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
}

/**
 * Assert computed design tokens on a surface. Values, not class names: a class that
 * stopped resolving to a token would still be present in the markup.
 * @param {import('@playwright/test').Page} page
 * @param {string} selector
 * @param {{ fontFamilyIncludes?: string, borderRadius?: string, minFontSize?: number }} expected
 */
export async function expectComputedTokens(page, selector, expected) {
  const actual = await page.locator(selector).first().evaluate((el) => {
    const cs = getComputedStyle(el)
    return {
      fontFamily: cs.fontFamily,
      borderRadius: cs.borderRadius,
      fontSize: parseFloat(cs.fontSize),
    }
  })
  if (expected.fontFamilyIncludes) {
    expect(actual.fontFamily.toLowerCase()).toContain(expected.fontFamilyIncludes.toLowerCase())
  }
  if (expected.borderRadius !== undefined) {
    expect(actual.borderRadius).toBe(expected.borderRadius)
  }
  if (expected.minFontSize !== undefined) {
    expect(actual.fontSize).toBeGreaterThanOrEqual(expected.minFontSize)
  }
  return actual
}
