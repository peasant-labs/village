/* Journey: the signed-in home.
 *
 * Lands on `/` as a signed-in person and checks what the home page owes them:
 * their transcripts in a table with who can read each one and the pull requests
 * it is attached to, the totals line above it, and their collectives and what
 * is waiting for approval beside it. Then it searches, and it opens the page
 * for a person who has published nothing. Each test records its still frame,
 * the accessible tree and an axe report.
 */
import { writeFileSync } from 'node:fs'
import { test, expect } from './lib/fixtures.mjs'
import { setScenario } from './lib/scenario.mjs'
import { scanAxe, seriousViolations, expectTheme, expectComputedTokens } from './lib/assertions.mjs'
import { HOME_ROWS } from './lib/home-fixtures.mjs'

async function evidence(page, testInfo, name, locator) {
  const aria = await locator.ariaSnapshot()
  writeFileSync(testInfo.outputPath(`${name}-aria.yml`), aria)
  await testInfo.attach(`${name}-aria.yml`, { path: testInfo.outputPath(`${name}-aria.yml`), contentType: 'text/yaml' })
  await page.evaluate(() => window.scrollTo(0, 0))
  await page.screenshot({ path: testInfo.outputPath(`${name}.png`), fullPage: true })
  await testInfo.attach(`${name}.png`, { path: testInfo.outputPath(`${name}.png`), contentType: 'image/png' })
}

async function expectNoSeriousAxe(page, testInfo) {
  const axe = await scanAxe(page)
  writeFileSync(testInfo.outputPath('axe.json'), JSON.stringify(axe, null, 2))
  await testInfo.attach('axe.json', { path: testInfo.outputPath('axe.json'), contentType: 'application/json' })
  const blocking = seriousViolations(axe)
  expect(blocking, JSON.stringify(blocking, null, 2)).toEqual([])
}

test.describe('home', () => {
  test('lists your transcripts, their totals and your collectives', async ({ page, theme }, testInfo) => {
    await page.goto('/')
    const home = page.getByTestId('home-page')
    await expect(home).toBeVisible()
    await expectTheme(page, theme)

    await expect(page.getByRole('heading', { level: 1, name: 'your transcripts' })).toBeVisible()
    // Every row the server listed, newest first, each leading to its transcript.
    const rows = page.getByTestId('home-transcript-row')
    await expect(rows).toHaveCount(HOME_ROWS.length)
    for (const [i, row] of HOME_ROWS.entries()) {
      await expect(rows.nth(i).getByRole('link', { name: row.title, exact: true })).toHaveAttribute(
        'href',
        `/transcripts/${row.id}`,
      )
    }

    // The totals line, from the person's own stats and memberships.
    const stats = page.getByRole('list', { name: 'your transcripts in numbers' })
    await expect(stats).toContainText('38 transcripts')
    await expect(stats).toContainText('3 collectives')
    await expect(stats).toContainText('9 pull requests')
    await expect(stats).toContainText('19.1M tokens')
    await expect(stats).toContainText('31h recorded')

    // Two pull request numbers, then the true count of the rest. Known
    // received harnesses lead with their mark, without a provider fact line.
    const first = page.locator('tr.tbl-row').first()
    await expect(first.getByRole('list', { name: 'pull requests' })).toContainText('#45')
    await expect(first.getByRole('link', { name: '2 more pull requests on the transcript page' })).toHaveText('+2')
    for (const [i] of HOME_ROWS.entries()) {
      const mark = rows.nth(i).locator('.iu-session > .pv-icon > svg[data-brand]')
      await expect(mark).toHaveAttribute('data-brand', 'claude')
      await expect(mark).toHaveAttribute('role', 'img')
      await expect(mark).toHaveAttribute('aria-label', 'Claude Code')
      await expect(rows.nth(i).locator('.iu-session-sub')).not.toContainText('Claude Code')
    }

    // The rail: the collectives this person belongs to, and what is waiting.
    await expect(page.getByTestId('home-rail-collective')).toHaveCount(3)
    await expect(page.getByTestId('home-rail-waiting')).toContainText('ML Reading Group')
    await expect(page.getByTestId('home-rail-waiting')).toContainText('1 transcript waiting')

    await expectComputedTokens(page, '[data-testid="home-page"]', {
      fontFamilyIncludes: 'atkinson',
      borderRadius: '0px',
      minFontSize: 16,
    })
    await expectNoSeriousAxe(page, testInfo)
    await evidence(page, testInfo, 'home-rows', home)
  })

  test('searches your transcripts', async ({ page, theme }, testInfo) => {
    await page.goto('/')
    await expect(page.getByTestId('home-transcript-row')).toHaveCount(HOME_ROWS.length)
    await expectTheme(page, theme)

    const search = page.getByRole('searchbox', { name: 'search your transcripts' })
    await search.fill('fix')
    await expect(page.getByTestId('home-transcript-row')).toHaveCount(1)
    await expect(page.getByRole('link', { name: 'Fix flaky ingest test', exact: true })).toBeVisible()
    await expect(page.getByTestId('home-count')).toHaveText('1 of 1 transcript')

    await expectNoSeriousAxe(page, testInfo)
    await evidence(page, testInfo, 'home-search', page.getByTestId('home-page'))

    // A search that matches nothing says so, and clearing it brings every row back.
    await search.fill('nothing like this')
    await expect(page.getByTestId('home-no-match')).toContainText('nothing like this')
    await page.getByRole('button', { name: 'clear search' }).click()
    await expect(page.getByTestId('home-transcript-row')).toHaveCount(HOME_ROWS.length)
  })

  test('teaches publishing when nothing is published yet', async ({ page, request, theme }, testInfo) => {
    await setScenario(request, 'empty')
    try {
      await page.goto('/')
      const empty = page.getByTestId('home-empty-state')
      await expect(empty).toBeVisible()
      await expectTheme(page, theme)
      await expect(empty).toContainText('nothing published yet')
      await expect(empty).toContainText('peasant village login')
      await expect(page.getByTestId('home-transcript-row')).toHaveCount(0)
      await expect(page.getByRole('searchbox')).toHaveCount(0)

      await expectNoSeriousAxe(page, testInfo)
      await evidence(page, testInfo, 'home-empty', page.getByTestId('home-page'))
    } finally {
      // The scenario persists for the rest of the run; restore the default.
      await setScenario(request, 'default')
    }
  })
})
