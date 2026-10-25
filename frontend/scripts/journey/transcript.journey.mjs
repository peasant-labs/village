/* Journey: village transcript viewer.
 *
 * Opens a stored transcript through the real route (REST list/detail/content ->
 * the SessionDetailV2 adapter -> Fairtrade's TranscriptViewer), which also
 * exercises the composed mock's transcript proxy. Records the viewer's
 * accessibility tree and axe report for agent review.
 *
 * The owner's header is its own group: a transcript the signed-in viewer owns
 * (lib/transcript-fixtures.mjs), its link, copy button and `more` menu, the
 * `manage access` popup, and its pull request list collapsed and expanded.
 * Each test ends on the state its screenshot shows.
 */
import { writeFileSync } from 'node:fs'
import { test, expect } from './lib/fixtures.mjs'
import { setScenario } from './lib/scenario.mjs'
import { OWNED_TRANSCRIPT, OWNED_PULL_REQUESTS } from './lib/transcript-fixtures.mjs'
import { scanAxe, seriousViolations, expectTheme, expectComputedTokens } from './lib/assertions.mjs'

test.describe('transcript viewer', () => {
  test('opens a stored transcript', async ({ page, theme }, testInfo) => {
    await page.goto('/transcripts/demo')

    const viewer = page.locator('.txn-app')
    await expect(viewer).toBeVisible()
    await expectTheme(page, theme)

    // Turns render from the proxied detail + content payloads.
    await expect(page.locator('.txn-turnwrap').first()).toBeVisible()

    const aria = await viewer.ariaSnapshot()
    writeFileSync(testInfo.outputPath('transcript-aria.yml'), aria)
    await testInfo.attach('transcript-aria.yml', {
      path: testInfo.outputPath('transcript-aria.yml'),
      contentType: 'text/yaml',
    })

    await expectComputedTokens(page, '.txn-body', {
      fontFamilyIncludes: 'atkinson',
      minFontSize: 16,
    })

    const axe = await scanAxe(page)
    writeFileSync(testInfo.outputPath('axe.json'), JSON.stringify(axe, null, 2))
    await testInfo.attach('axe.json', {
      path: testInfo.outputPath('axe.json'),
      contentType: 'application/json',
    })
    const blocking = seriousViolations(axe)
    expect(blocking, JSON.stringify(blocking, null, 2)).toEqual([])
  })
})
const SHOWN_BEFORE_SHOW_ALL = 3

async function recordAxe(page, testInfo) {
  const axe = await scanAxe(page)
  writeFileSync(testInfo.outputPath('axe.json'), JSON.stringify(axe, null, 2))
  await testInfo.attach('axe.json', { path: testInfo.outputPath('axe.json'), contentType: 'application/json' })
  const blocking = seriousViolations(axe)
  expect(blocking, JSON.stringify(blocking, null, 2)).toEqual([])
}

test.describe('transcript page, as its owner', () => {
  test.beforeEach(async ({ page, request, theme }) => {
    await setScenario(request, 'transcript-owner')
    await page.goto(`/transcripts/${OWNED_TRANSCRIPT.id}`)
    await expect(page.locator('.txn-app')).toBeVisible()
    await expectTheme(page, theme)
    // The owner's rows arrive once the session names the owner.
    await expect(page.getByRole('button', { name: 'more', exact: true })).toBeVisible()
  })

  test.afterEach(async ({ request }) => {
    await setScenario(request, 'default')
  })

  test('the header copies the address and keeps more', async ({ page }, testInfo) => {
    // The clipboard is the host's, not the app's: record what the control
    // writes, so this asserts the real click callback instead of a dormant
    // button. The app itself is untouched.
    await page.addInitScript(() => {
      window.__copiedFromTranscriptHeader = []
      const write = async (value) => { window.__copiedFromTranscriptHeader.push(value) }
      try {
        // The host owns the clipboard; stand one in that records the write.
        Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: write } })
      } catch {
        // A browser that will not let the instance be shadowed: patch the
        // shared Clipboard prototype instead, which every call goes through.
        Object.defineProperty(Object.getPrototypeOf(navigator.clipboard), 'writeText', { configurable: true, value: write })
      }
    })
    await page.reload()
    await expect(page.locator('.txn-app')).toBeVisible()
    await expect(page.getByRole('button', { name: 'more', exact: true })).toBeVisible()

    const actions = page.getByTestId('transcript-header-actions')
    // The address is copied, never spelled out in the header.
    await expect(actions.locator('a')).toHaveCount(0)
    const copy = actions.getByRole('button', { name: 'copy link' })
    await expect(copy).toBeVisible()
    await expect(actions.getByRole('button', { name: 'more', exact: true })).toBeVisible()

    const expected = `${new URL(page.url()).origin}/transcripts/${OWNED_TRANSCRIPT.id}`
    await copy.click()
    await expect(actions.getByRole('status')).toHaveText('copied')
    await expect.poll(() => page.evaluate(() => window.__copiedFromTranscriptHeader)).toEqual([expected])
    await expect(actions.getByRole('button', { name: 'copied' })).toBeVisible()

    // The composite's own share/more tail and the outcome chip are off.
    await expect(page.locator('.txn-actions').getByRole('button', { name: /share|more actions/ })).toHaveCount(0)

    await expectComputedTokens(page, '[data-testid="transcript-header-actions"] .btn', { borderRadius: '0px' })

    const aria = await page.locator('.txn-header').ariaSnapshot()
    writeFileSync(testInfo.outputPath('transcript-header-aria.yml'), aria)
    await testInfo.attach('transcript-header-aria.yml', {
      path: testInfo.outputPath('transcript-header-aria.yml'),
      contentType: 'text/yaml',
    })
    await recordAxe(page, testInfo)
  })

  test('the more menu, open', async ({ page }, testInfo) => {
    await page.getByRole('button', { name: 'more', exact: true }).click()
    const menu = page.getByRole('menu')
    await expect(menu.getByRole('menuitem')).toHaveText(['manage access', 'edit title', 'download markdown', 'download json', 'download jsonl'])
    // Who can read it, with the collective names in their own case: the menu
    // sets its chrome in lowercase, and the names are user content.
    const caption = page.getByTestId('transcript-access-caption')
    await expect(caption).toHaveText('who can read it: Acme Platform, Acme Company')
    const names = caption.locator('span')
    await expect(names).toHaveCSS('text-transform', 'none')
    await recordAxe(page, testInfo)
  })

  test('manage access, open', async ({ page }, testInfo) => {
    await page.getByRole('button', { name: 'more', exact: true }).click()
    await page.getByRole('menuitem', { name: 'manage access' }).click()
    const dialog = page.getByRole('dialog', { name: 'manage access' })
    await expect(dialog).toBeVisible()
    await expect.poll(() => dialog.evaluate((node) => node.contains(document.activeElement))).toBe(true)
    const readers = dialog.getByRole('list', { name: 'who can read it' })
    await expect(readers.getByRole('listitem')).toHaveCount(2)
    await expect(readers).toContainText('Acme Platform')
    await expect(readers).toContainText('Acme Company')
    await expect(dialog.getByRole('button', { name: 'remove Acme Company' })).toBeVisible()
    await expect(dialog.getByRole('button', { name: 'add ML Reading Group' })).toBeVisible()
    await recordAxe(page, testInfo)
  })

  test('pull requests, collapsed', async ({ page }) => {
    const section = page.getByTestId('transcript-pull-requests')
    await expect(section).toBeVisible()
    await expect(section.getByTestId('transcript-pull-request')).toHaveCount(SHOWN_BEFORE_SHOW_ALL)
    await expect(section.getByRole('button', { name: `show all ${OWNED_PULL_REQUESTS.length}` })).toBeVisible()
  })

  test('pull requests, expanded', async ({ page }) => {
    const section = page.getByTestId('transcript-pull-requests')
    await section.getByRole('button', { name: `show all ${OWNED_PULL_REQUESTS.length}` }).click()
    await expect(section.getByTestId('transcript-pull-request')).toHaveCount(OWNED_PULL_REQUESTS.length)
    // Focus moves to the first row the button revealed.
    await expect(section.getByRole('link', { name: 'acme/ingest-api #51' })).toBeFocused()
    await expect(section.getByRole('button', { name: /^show all/ })).toHaveCount(0)
  })
})
