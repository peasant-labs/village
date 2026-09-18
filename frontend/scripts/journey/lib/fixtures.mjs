/* Journey fixtures: the deterministic, authenticated, theme-pinned page every
 * journey starts from. Overrides Playwright's context fixture so determinism and
 * auth are installed BEFORE the first navigation, never inside a test body.
 *
 * The theme fixture reads the Playwright project name ("dark" | "light"), so the
 * two projects run the identical journey against both themes.
 */
import { test as base, expect } from '@playwright/test'
import { installDeterminism } from './determinism.mjs'

export const test = base.extend({
  theme: async ({}, use, testInfo) => {
    await use(testInfo.project.name)
  },
  context: async ({ context, theme }, use) => {
    await installDeterminism(context)
    await context.addInitScript((activeTheme) => {
      try {
        localStorage.setItem('peasant-theme', activeTheme)
      } catch {
        /* no storage; the app falls back to its default theme */
      }
    }, theme)
    await context.addCookies([
      { name: 'peasant_token', value: 'demo-token', domain: 'localhost', path: '/' },
    ])
    await use(context)
  },
})

export { expect }