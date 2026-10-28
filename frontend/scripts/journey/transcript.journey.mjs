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
import { readFileSync, writeFileSync } from 'node:fs'
import { parse } from 'yaml'
import { test, expect } from './lib/fixtures.mjs'
import { setScenario } from './lib/scenario.mjs'
import { OWNED_TRANSCRIPT, OWNED_PULL_REQUESTS } from './lib/transcript-fixtures.mjs'
import { scanAxe, seriousViolations, expectTheme, expectComputedTokens } from './lib/assertions.mjs'

const parity = parse(readFileSync(new URL('./testdata/transcript-context-parity.yaml', import.meta.url), 'utf8'))
const evidence = parse(readFileSync(new URL('../../../backend/internal/handler/testdata/transcript-unverified-pulls.yaml', import.meta.url), 'utf8'))
const expectedContexts = ['home-control', 'direct-collective', 'inferred-collective', 'collective-with-pending-notice']
if (parity.contexts.map((c) => c.name).sort().join('|') !== expectedContexts.sort().join('|')) {
  throw new Error('transcript context parity fixture has missing or duplicate required case names')
}
const expectedMobileCases = ['home-short', 'home-long', 'collective-short', 'collective-long-pending', 'home-clipboard-denied']
if (parity.mobile_cases.map((c) => c.name).sort().join('|') !== expectedMobileCases.sort().join('|')) {
  throw new Error('mobile transcript fixture has missing or duplicate required case names')
}

test.describe('transcript context parity', () => {
  test.beforeEach(async ({ page, request }, testInfo) => {
    await setScenario(request, 'transcript-owner')
    const hasNotice = testInfo.title.includes('pending')
    const shortContent = testInfo.title.includes('short') || testInfo.title.includes('clipboard-denied')
    const base = `/api/v1/transcripts/${OWNED_TRANSCRIPT.id}`
    await page.route('**/api/v1/collectives', async (route) => {
      const response = await route.fetch()
      const data = await response.json()
      if (hasNotice) {
        const group = data.find((g) => g.id === parity.pending_group.id)
        if (group) group.role = 'owner'
        else data.push(parity.pending_group)
      }
      await route.fulfill({ response, json: data })
    })
    await page.route(`**${base}`, async (route) => {
      const response = await route.fetch()
      const data = await response.json()
      const shares = data.enriched_shares
      data.enriched_shares = shares.slice(0, 1)
      if (hasNotice) data.enriched_shares.push({ ...shares[1], status: 'pending' })
      await route.fulfill({ response, json: data })
    })
    await page.route(`**${base}/content`, async (route) => {
      const data = JSON.parse(evidence.content.current)
      const session = data.sessionDetail
      const source = session.turns[0]
      const turnCount = shortContent ? 1 : parity.turn_count
      session.turns = Array.from({ length: turnCount }, (_, index) => ({
        ...source, index, content: `synthetic turn ${index}`, toolCalls: index === 42 ? source.toolCalls : [],
      }))
      session.turnCount = session.turns.length
      session.toolCallCount = turnCount > 42 ? 2 : 0
      await route.fulfill({ json: data, headers: { 'access-control-allow-origin': '*' } })
    })
    await page.route(`**${base}/pulls`, async (route) => {
      const response = await route.fetch()
      const data = await response.json()
      data.unverified_pull_requests = [parity.source]
      await route.fulfill({ response, json: data })
    })
  })
  test.afterEach(async ({ request }) => { await setScenario(request, 'default') })

  for (const c of parity.contexts) {
    test(`${c.name}: full root, header and zen`, async ({ page }) => {
      await page.goto(`/transcripts/${OWNED_TRANSCRIPT.id}${c.search}`)
      await expect(page.locator('.txn-stream')).toBeVisible()
      await expect(page.getByRole('navigation', { name: 'breadcrumb' })).toContainText(OWNED_TRANSCRIPT.title)
      if (c.expected === 'collective') await expect(page).toHaveURL(/in=collective/)
      else await expect(page).toHaveURL(/in=home/)
      const sidebar = page.getByRole('complementary', { name: 'peasant' })
      await expect(sidebar).toBeVisible()
      const zen = page.getByRole('button', { name: 'zen mode' })
      await zen.click()
      await expect(sidebar).toBeHidden()
      await zen.click()
      await expect(sidebar).toBeVisible()
      const stream = page.locator('.txn-stream')
      // Reduced motion is configured by the runner: state should follow the
      // actual stream scroll by the next painted frames, not a timer.
      const condensed = await stream.evaluate(async (el) => {
        el.scrollTop = 500
        await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)))
        return !!document.querySelector('.txn-app-condensed')
      })
      expect(condensed).toBe(true)
      const restored = await stream.evaluate(async (el) => {
        el.scrollTop = 0
        await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)))
        return !document.querySelector('.txn-app-condensed')
      })
      expect(restored).toBe(true)
      const box = await page.locator('.txn-app').boundingBox()
      expect(box.y + box.height).toBeLessThanOrEqual(page.viewportSize().height + 1)
    })
  }

  test('unverified source opens the original call from another tab', async ({ page }) => {
    await page.goto(`/transcripts/${OWNED_TRANSCRIPT.id}?in=home`)
    await expect(page.locator('.txn-stream')).toBeVisible()
    await page.getByRole('tab', { name: /^files/ }).click()
    await page.getByRole('button', { name: /source/i }).click()
    const stream = page.locator('.txn-stream')
    await expect(stream.getByRole('button', { name: `Bash ${parity.command}` })).toHaveAttribute('aria-expanded', 'true')
    await expect(stream.getByText(parity.result, { exact: false })).toBeVisible()
  })

  for (const c of parity.mobile_cases) {
    test(`${c.name}: mobile utilities and exits`, async ({ page }) => {
      await page.setViewportSize({ width: 390, height: 844 })
      await page.addInitScript(({ rejected }) => {
        window.__transcriptCopies = []
        Object.defineProperty(navigator, 'clipboard', { configurable: true, value: {
          writeText: async (value) => {
            window.__transcriptCopies.push(value)
            if (rejected) throw new Error('clipboard permission denied')
          },
        } })
      }, { rejected: !!c.copy_rejected })
      await page.goto(`/transcripts/${OWNED_TRANSCRIPT.id}${c.search}`)
      await expect(page.locator('.txn-stream')).toBeVisible()
      await page.getByRole('button', { name: 'transcript tools' }).click()
      const actions = page.getByTestId('transcript-header-actions')
      const footer = page.locator('.txn-footer')
      await expect(actions).toHaveCount(1)
      await expect(footer.getByTestId('transcript-header-actions')).toBeVisible()
      await expect(page.getByRole('button', { name: 'copy link', exact: true })).toHaveCount(1)
      await expect(page.getByRole('button', { name: 'more', exact: true })).toHaveCount(1)
      await actions.getByRole('button', { name: 'copy link' }).click()
      await expect.poll(() => page.evaluate(() => window.__transcriptCopies)).toEqual([
        `${new URL(page.url()).origin}/transcripts/${OWNED_TRANSCRIPT.id}`,
      ])
      const toast = page.locator('.tsx-host .fb-toast')
      if (c.copy_rejected) {
        await expect(toast).toHaveCount(0)
      } else {
        await expect(toast).toContainText('transcript link copied.')
        await expect(page.locator('.txn-header .fb-toast')).toHaveCount(0)
      }
      await actions.getByRole('button', { name: 'more', exact: true }).click()
      const menu = page.getByRole('menu')
      await expect(menu.getByRole('menuitem')).toHaveText(['manage access', 'edit title', 'download markdown', 'download json', 'download jsonl'])
      await menu.getByRole('menuitem', { name: 'manage access' }).click()
      await expect(page.getByRole('dialog', { name: 'manage access' })).toBeVisible()
      await page.keyboard.press('Escape')
      await expect(page.getByRole('dialog', { name: 'manage access' })).toBeHidden()
      if (c.pending) {
        await expect(page.getByRole('button', { name: 'approve' })).toBeVisible()
        await expect(page.getByRole('button', { name: 'reject' })).toBeVisible()
        let approval = null
        await page.route(`**/api/v1/collectives/*/shares/${OWNED_TRANSCRIPT.id}`, async (route) => {
          approval = { method: route.request().method(), body: route.request().postDataJSON() }
          await route.fulfill({ json: {}, headers: { 'access-control-allow-origin': '*' } })
        })
        await page.getByRole('button', { name: 'approve' }).click()
        await expect.poll(() => approval).toEqual({ method: 'PATCH', body: { status: 'approved' } })
        await expect(page.getByText('Acme Company — approved')).toBeVisible()
      }
      const last = page.locator('.txn-stream').getByText(`synthetic turn ${c.last_turn}`, { exact: true })
      await last.scrollIntoViewIfNeeded()
      await expect(last).toBeVisible()
      const geometry = await page.evaluate(() => ({
        width: document.documentElement.scrollWidth,
        viewport: window.innerWidth,
        footerBottom: document.querySelector('.txn-footer').getBoundingClientRect().bottom,
        viewportBottom: window.innerHeight,
      }))
      expect(geometry.width).toBeLessThanOrEqual(geometry.viewport)
      expect(geometry.footerBottom).toBeLessThanOrEqual(geometry.viewportBottom + 1)
    })
  }
})

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
