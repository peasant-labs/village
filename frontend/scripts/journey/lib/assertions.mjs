/* VENDORED from fairtrade-design-system/scripts/journey/lib/assertions.mjs.
   Byte-faithful except this banner. Do not edit here: edit the upstream file and
   re-vendor. scripts/journey/lib/vendor-guard.test.mjs fails when the bodies drift. */
/* Journey assertion helpers: the design-system and accessibility contract a
 * mounted surface must satisfy.
 *
 * App-agnostic: this is the canonical copy consumers vendor into their own
 * journey harness. See scripts/journey/README.md. */
import { expect } from '@playwright/test'
import { AxeBuilder } from '@axe-core/playwright'

export const DEFAULT_AXE_TAGS = ['wcag2a', 'wcag2aa']

/** Run axe-core over the current page and return a compact, JSON-serializable report. */
export async function scanAxe(page, tags = DEFAULT_AXE_TAGS) {
  const results = await new AxeBuilder({ page }).withTags(tags).analyze()
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

/** The document element must carry the requested theme (the app's own control writes it). */
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