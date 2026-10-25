/* Journey: the signed-in home.
 *
 * Lands on `/` as a signed-in person and checks what the home page owes them:
 * the totals line, the newest transcripts as cards, what is waiting for a
 * collective's approval, and the table of their transcripts, grouped and with
 * the columns they choose, remembered on this device. Then it searches, fits a
 * phone, and opens the page for a person who has published nothing. Each test
 * records its still frame, the accessible tree and an axe report.
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

/** Shows every row as one flat list, the way a person who chose it sees it. */
async function groupByNothing(page) {
  await page.getByTestId('home-group-by').getByRole('button').first().click()
  await page.getByRole('radio', { name: 'nothing' }).click()
}

test.describe('home', () => {
  test('lists your transcripts, their totals and the newest as cards', async ({ page, theme }, testInfo) => {
    await page.goto('/')
    const home = page.getByTestId('home-page')
    await expect(home).toBeVisible()
    await expectTheme(page, theme)

    await expect(page.getByRole('heading', { level: 1, name: 'your transcripts' })).toBeVisible()

    // The totals line, from the person's own stats and memberships.
    const stats = page.getByRole('list', { name: 'your transcripts in numbers' })
    await expect(stats).toContainText('38 transcripts')
    await expect(stats).toContainText('3 collectives')
    await expect(stats).toContainText('9 pull requests linked')
    await expect(stats).toContainText('19.1M tokens recorded')

    // The newest three as cards, each leading to its transcript.
    const recent = page.getByRole('region', { name: 'recent' })
    await expect(recent.getByRole('link')).toHaveCount(Math.min(3, HOME_ROWS.length))
    // A card carries where it was opened from, so the transcript page can lead back here.
    await expect(recent.getByRole('link').first()).toHaveAttribute('href', `/transcripts/${HOME_ROWS[0].id}?in=home`)

    // The table opens grouped by project with every group closed; one control
    // opens them all.
    const groups = page.locator('[data-testid="home-table"] tr.is-group button')
    await expect(groups.first()).toHaveAttribute('aria-expanded', 'false')
    await expect(page.getByTestId('home-transcript-row')).toHaveCount(0)
    await page.getByRole('button', { name: 'expand all' }).click()
    const rows = page.getByTestId('home-transcript-row')
    await expect(rows).toHaveCount(HOME_ROWS.length)
    for (const row of HOME_ROWS) {
      await expect(page.getByTestId('home-table').getByRole('link', { name: row.title, exact: true })).toHaveAttribute(
        'href',
        `/transcripts/${row.id}?in=home`,
      )
    }

    // The agent column leads with the provider's mark beside its name.
    const agent = page.locator('[data-testid="home-table"] td[data-col="agent"]').first()
    await expect(agent.locator('svg[data-brand]')).toHaveAttribute('data-brand', 'claude')
    await expect(agent).toHaveText('claude-code')

    // The pull requests column is one the person turns on: two numbers, then
    // the true count of the rest.
    await page.getByTestId('home-columns').getByRole('button', { name: 'columns' }).click()
    await page.getByRole('checkbox', { name: 'pull requests' }).click()
    await page.keyboard.press('Escape')
    const first = page.locator('[data-testid="home-table"] td[data-col="pullRequests"]').first()
    await expect(first.getByRole('list', { name: 'pull requests' })).toContainText('#45')
    await expect(first.getByRole('link', { name: '2 more pull requests on the transcript page' })).toHaveText('+2')

    // What is waiting for a collective's approval.
    await expect(page.getByTestId('home-waiting')).toContainText('ML Reading Group')
    await expect(page.getByTestId('home-waiting')).toContainText('1 transcript waiting')

    await expectComputedTokens(page, '[data-testid="home-page"]', {
      fontFamilyIncludes: 'atkinson',
      borderRadius: '0px',
      minFontSize: 16,
    })
    await expectNoSeriousAxe(page, testInfo)
    await evidence(page, testInfo, 'home-rows', home)
  })

  test('remembers how the table is arranged', async ({ page, theme }) => {
    await page.goto('/')
    await expect(page.getByTestId('home-page')).toBeVisible()
    await expectTheme(page, theme)
    await groupByNothing(page)
    await expect(page.getByTestId('home-transcript-row')).toHaveCount(HOME_ROWS.length)
    await page.reload()
    await expect(page.getByTestId('home-transcript-row')).toHaveCount(HOME_ROWS.length)
    await expect(page.getByTestId('home-group-by')).toContainText('group by: nothing')
  })

  test('searches your transcripts', async ({ page, theme }, testInfo) => {
    await page.goto('/')
    await expect(page.getByTestId('home-page')).toBeVisible()
    await expectTheme(page, theme)
    await groupByNothing(page)
    await expect(page.getByTestId('home-transcript-row')).toHaveCount(HOME_ROWS.length)

    const search = page.getByRole('searchbox', { name: 'search your transcripts' })
    await search.fill('fix')
    await expect(page.getByTestId('home-transcript-row')).toHaveCount(1)
    await expect(page.getByTestId('home-table').getByRole('link', { name: 'Fix flaky ingest test', exact: true })).toBeVisible()
    await expect(page.getByTestId('home-count')).toHaveText('1 of 1 transcript')

    await expectNoSeriousAxe(page, testInfo)
    await evidence(page, testInfo, 'home-search', page.getByTestId('home-page'))

    // A search that matches nothing says so, and clearing it brings every row back.
    await search.fill('nothing like this')
    await expect(page.getByTestId('home-no-match')).toContainText('nothing like this')
    await page.getByRole('button', { name: 'clear search' }).click()
    await expect(page.getByTestId('home-transcript-row')).toHaveCount(HOME_ROWS.length)
  })

  test('fits a phone, with the table scrolling in its own box', async ({ page, theme }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/')
    await expect(page.getByTestId('home-page')).toBeVisible()
    await expectTheme(page, theme)
    await page.getByRole('button', { name: 'expand all' }).click()
    await expect(page.getByTestId('home-transcript-row')).toHaveCount(HOME_ROWS.length)
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
  })

  test('teaches publishing when nothing is published yet', async ({ page, request, theme }, testInfo) => {
    await setScenario(request, 'empty')
    try {
      await page.goto('/')
      const empty = page.getByTestId('home-empty-state')
      await expect(empty).toBeVisible()
      await expectTheme(page, theme)
      await expect(empty).toContainText('nothing published yet')
      // How to publish lives in the docs; the app links there instead of
      // restating the command.
      const publish = empty.getByRole('link', { name: /how to publish/ })
      await expect(publish).toHaveAttribute('href', 'https://peasantlabs.org/docs#publish')
      // It leaves the app: a new tab on the docs, with no window handle back.
      await expect(publish).toHaveAttribute('target', '_blank')
      await expect(publish).toHaveAttribute('rel', /noopener/)
      await expect(empty).not.toContainText('curl -fsSL')
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
