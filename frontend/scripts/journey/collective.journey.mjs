/* Journey: the collectives list and a collective.
 *
 * Opens the collectives list (the collectives the viewer belongs to), a
 * collective as its owner, as a member, as the owner of a curated collective
 * with sessions waiting, and as a signed-out reader of a public one, and the
 * repo picker the owner opens from the stats tab, all through the real routes
 * against the composed mock's collective fixtures (lib/collective-fixtures.mjs).
 * It asserts roles, names and computed tokens rather than class strings,
 * records the accessible tree and the axe report, and attaches the still a
 * reviewer reads first.
 */
import { writeFileSync } from 'node:fs'
import { test, expect } from './lib/fixtures.mjs'
import { setScenario } from './lib/scenario.mjs'
import { scanAxe, seriousViolations, expectTheme, expectComputedTokens } from './lib/assertions.mjs'
import { JOURNEY_COLLECTIVES, JOURNEY_PENDING, RETAINED_CONTRIBUTIONS, RETAINED_GROUPED } from './lib/collective-fixtures.mjs'

const OWNER = JOURNEY_COLLECTIVES.owner
const MEMBER = JOURNEY_COLLECTIVES.member
const VISITOR = JOURNEY_COLLECTIVES.visitor

/** Opens a tab by its label and returns its panel, which must then be shown. */
async function openTab(page, label) {
  const tab = page.getByRole('tab', { name: new RegExp(`^${label}`) })
  await tab.click()
  await expect(tab).toHaveAttribute('aria-selected', 'true')
  const panel = page.locator(`#${await tab.getAttribute('aria-controls')}`)
  await expect(panel).toBeVisible()
  return panel
}

async function still(page, testInfo, name) {
  const path = testInfo.outputPath(`${name}.png`)
  await page.screenshot({ path, fullPage: true })
  await testInfo.attach(`${name}.png`, { path, contentType: 'image/png' })
}

async function record(page, testInfo, locator, name) {
  writeFileSync(testInfo.outputPath(`${name}-aria.yml`), await locator.ariaSnapshot())
  await testInfo.attach(`${name}-aria.yml`, { path: testInfo.outputPath(`${name}-aria.yml`), contentType: 'text/yaml' })
  const axe = await scanAxe(page)
  writeFileSync(testInfo.outputPath('axe.json'), JSON.stringify(axe, null, 2))
  await testInfo.attach('axe.json', { path: testInfo.outputPath('axe.json'), contentType: 'application/json' })
  const blocking = seriousViolations(axe)
  expect(blocking, JSON.stringify(blocking, null, 2)).toEqual([])
}

test.describe('collectives', () => {
  test.beforeEach(async ({ request }) => {
    // A fresh world: no earlier journey's link or settings write carries over.
    await setScenario(request, 'default')
  })

  test('lists the collectives the viewer belongs to in a table', async ({ page, theme }, testInfo) => {
    await page.goto('/collectives')
    await expectTheme(page, theme)

    // The list holds memberships; a collective the viewer only sees is on
    // /discover (scripts/journey/discover.journey.mjs).
    const table = page.getByRole('table', { name: 'collectives you belong to' })
    await expect(table).toBeVisible()
    for (const { name } of [OWNER, MEMBER]) {
      await expect(table.getByRole('link', { name, exact: true })).toBeVisible()
    }
    await expect(table.getByRole('link', { name: JOURNEY_COLLECTIVES.visitor.name })).toHaveCount(0)
    await expectComputedTokens(page, '[data-testid="collectives-page"]', { fontFamilyIncludes: 'atkinson', borderRadius: '0px', minFontSize: 16 })
    await still(page, testInfo, 'collectives-list')
    await record(page, testInfo, table, 'collectives-list')

    await expect(page.getByTestId('collectives-page').getByRole('link', { name: 'discover', exact: true })).toHaveAttribute('href', '/discover')
    await table.getByRole('link', { name: OWNER.name, exact: true }).click()
    await expect(page).toHaveURL(new RegExp(`/collectives/${OWNER.id}$`))
  })

  test('shows the owner the chips, the tabs, the table and the github org', async ({ page, theme }, testInfo) => {
    await page.goto(`/collectives/${OWNER.id}`)
    await expectTheme(page, theme)

    await expect(page.getByRole('heading', { level: 1, name: OWNER.name })).toBeVisible()
    // The name is user content: it keeps its case although headings are lowercase chrome.
    expect(await page.getByRole('heading', { level: 1 }).evaluate((h1) => h1.innerText.trim())).toBe(OWNER.name)
    const chips = page.getByRole('list', { name: 'how this collective works' })
    await expect(chips.getByRole('listitem')).toHaveText([
      /who can publish:\s*anyone who joins publishes/,
      /who can read:\s*members read/,
      /your role:\s*you are the owner/,
    ])
    // An invite in an earlier journey adds to the member list the mock's reset starts from, so the tab counts whoever is there.
    await expect(page.getByRole('tab')).toHaveText(['transcripts 248', /^members \d+$/, 'stats'])
    await expect(page.getByRole('table', { name: 'transcripts 248' })).toContainText('Fix flaky ingest test')
    // The app sidebar carries a settings gear of its own, so the collective's
    // own control is read inside the collective's header actions.
    await expect(page.getByTestId('collective-header-actions').getByRole('link', { name: 'settings' })).toHaveAttribute('href', `/collectives/${OWNER.id}/settings`)
    await expect(page.getByRole('link', { name: 'invite people' })).toHaveAttribute('href', `/collectives/${OWNER.id}/settings?section=members`)
    await expectComputedTokens(page, '.on-page', { fontFamilyIncludes: 'atkinson', borderRadius: '0px', minFontSize: 16 })

    await page.getByRole('button', { name: 'more', exact: true }).click()
    await expect(page.getByRole('menuitem')).toHaveText(['publish several transcripts'])
    await expect(page.getByRole('menuitem')).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('menu')).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'more', exact: true })).toBeFocused()

    await still(page, testInfo, 'collective-owner')
    await record(page, testInfo, page.getByRole('main'), 'collective-owner')

    // The arrow keys move along the tablist, and the address keeps the tab.
    await page.getByRole('tab', { name: /^transcripts/ }).focus()
    await page.keyboard.press('ArrowRight')
    await expect(page.getByRole('tab', { name: /^members/ })).toHaveAttribute('aria-selected', 'true')
    await expect(page.getByRole('tab', { name: /^members/ })).toBeFocused()
    await expect(page).toHaveURL(new RegExp(`/collectives/${OWNER.id}\\?tab=members$`))
    await expect(page.getByRole('list', { name: 'members' })).toContainText('@bob-ai')

    const stats = await openTab(page, 'stats')
    await expect(stats.getByTestId('collective-org-line')).toContainText('acme: 2 of 14 repos linked')
    await expect(stats.getByRole('button', { name: 'manage repositories' })).toBeVisible()
    await still(page, testInfo, 'collective-owner-stats')
  })

  test('shows the owner of a curated collective what waits for approval', async ({ page, request, theme }, testInfo) => {
    await setScenario(request, 'curated-owner')
    await page.goto(`/collectives/${OWNER.id}`)
    await expectTheme(page, theme)

    await expect(page.getByRole('list', { name: 'how this collective works' })).toContainText('you approve each session')
    // The table lists only approved transcripts, so it draws no status column:
    // what still waits is the waiting tab's own table.
    const table = page.getByRole('table', { name: 'transcripts 248' })
    await expect(table.getByRole('columnheader')).toHaveText(['transcript', 'contributor', 'agent', 'turns', 'when'])
    await page.getByRole('button', { name: 'more', exact: true }).click()
    await expect(page.getByRole('menuitem')).toHaveText(['publish several transcripts', 'review transcripts'])
    await page.keyboard.press('Escape')

    const waiting = await openTab(page, 'waiting for approval')
    await expect(page.getByRole('tab', { name: /^waiting/ })).toHaveText(`waiting for approval ${JOURNEY_PENDING.length}`)
    const queue = waiting.getByRole('table', { name: `waiting for approval ${JOURNEY_PENDING.length}` })
    await expect(queue.locator('tbody a.is-title')).toHaveText(JOURNEY_PENDING.map((row) => row.title))
    await expect(queue.locator('tbody .on-handle-cell')).toHaveText(JOURNEY_PENDING.map((row) => `@${row.login}`))
    await expect(queue.locator('tbody .on-tag')).toHaveText(JOURNEY_PENDING.map(() => 'waiting'))
    await expect(waiting.getByRole('link', { name: 'review transcripts' })).toHaveAttribute('href', `/collectives/${OWNER.id}/review`)
    await expect(page).toHaveURL(new RegExp(`\\?tab=waiting$`))
    await still(page, testInfo, 'collective-waiting')
    await record(page, testInfo, waiting, 'collective-waiting')
  })

  test('retains transcript grouping and private contribution withdrawal on the mounted detail page', async ({ page, theme }, testInfo) => {
    await page.goto(`/collectives/${OWNER.id}`)
    await expectTheme(page, theme)
    const library = page.getByTestId('collective-transcript-library')
    await library.getByTestId('collective-library-disclosure-toggle').click()
    await expect(library.getByRole('combobox', { name: 'filter transcripts by contributor' })).toBeVisible()
    await expect(library.getByRole('checkbox', { name: 'select all' })).toBeVisible()
    const contribution = page.getByTestId('my-contributions-panel')
    await expect(contribution).toContainText(RETAINED_CONTRIBUTIONS[0].title)
    await expect(contribution).toContainText('pending')
    await contribution.getByTestId('child-session-disclosure-toggle').click()
    await expect(contribution.getByRole('link', { name: RETAINED_CONTRIBUTIONS[1].title })).toBeVisible()
    await expect(library.getByRole('alert')).toHaveCount(0)
    const continuation = contribution.getByTestId('grouped-helper-continuation')
    const laterPage = page.waitForResponse((response) => response.url().includes(`/collectives/${OWNER.id}/my-shares?`) && new URL(response.url()).searchParams.get('page') === '2')
    await continuation.getByRole('button', { name: /load/ }).click()
    expect((await laterPage).ok()).toBe(true)
    const helpers = contribution.locator(`[data-group-id="${RETAINED_GROUPED.groupID}"]`)
    await helpers.getByTestId('helper-group-toggle').click()
    await expect(helpers.getByRole('link', { name: RETAINED_GROUPED.title })).toBeVisible()
    await expect(library.getByRole('alert')).toHaveCount(0)
    await library.getByRole('button', { name: 'repos', exact: true }).click()
    await expect(library).toContainText('repositories')
    const rowStyles = await library.locator('.group.relative').evaluateAll((rows) => rows.map((row) => {
      const title = row.querySelector('span.font-medium')
      const facts = title?.nextElementSibling
      const handle = [...row.querySelectorAll('span')].find((span) => span.textContent === 'alice-dev' || span.textContent === 'bob-ai' || span.textContent === 'carol-ml')
      const probe = (element) => element ? { text: element.textContent, fontSize: getComputedStyle(element).fontSize, fontFamily: getComputedStyle(element).fontFamily } : null
      return { title: probe(title), facts: probe(facts), handle: probe(handle) }
    }))
    expect(rowStyles.length).toBeGreaterThan(0)
    for (const row of rowStyles) {
      expect(parseFloat(row.title.fontSize)).toBeGreaterThanOrEqual(16)
      expect(parseFloat(row.facts.fontSize)).toBeGreaterThanOrEqual(14)
      expect(row.facts.fontFamily.toLowerCase()).toContain('mono')
      if (row.handle) {
        expect(parseFloat(row.handle.fontSize)).toBeGreaterThanOrEqual(14)
        expect(row.handle.fontFamily.toLowerCase()).toContain('mono')
      }
    }
    writeFileSync(testInfo.outputPath('retained-row-computed-styles.json'), JSON.stringify(rowStyles, null, 2))
    await testInfo.attach('retained-row-computed-styles.json', { path: testInfo.outputPath('retained-row-computed-styles.json'), contentType: 'application/json' })
    const repositoryLinks = library.getByRole('link', { name: 'open repository', exact: true })
    await expect(repositoryLinks).toHaveCount(2)
    expect(await repositoryLinks.evaluateAll((links) => links.every((link) => !link.closest('button, [role="button"]')))).toBe(true)
    await page.evaluate(() => window.scrollTo(0, 0))
    await still(page, testInfo, 'collective-retained-flows')
    await record(page, testInfo, library, 'collective-retained-flows')
    const request = page.waitForResponse((response) => response.request().method() === 'DELETE' && response.url().endsWith(`/transcripts/${RETAINED_CONTRIBUTIONS[1].id}/share/${OWNER.id}`))
    await contribution.getByTestId('child-session-disclosure-rows').getByTitle('withdraw contribution').click()
    expect((await request).ok()).toBe(true)
    await expect(contribution.getByRole('link', { name: RETAINED_CONTRIBUTIONS[1].title })).toHaveCount(0)
  })

  test('shows a member the org without manage, and leave', async ({ page, theme }, testInfo) => {
    await page.goto(`/collectives/${MEMBER.id}`)
    await expectTheme(page, theme)

    await expect(page.getByRole('heading', { level: 1, name: MEMBER.name })).toBeVisible()
    await expect(page.getByRole('list', { name: 'how this collective works' })).toContainText('you are a member')
    await expect(page.getByTestId('collective-header-actions').getByRole('link', { name: 'settings' })).toHaveCount(0)
    await expect(page.getByRole('link', { name: 'invite people' })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'leave' })).toBeVisible()

    await still(page, testInfo, 'collective-member')
    await record(page, testInfo, page.getByRole('main'), 'collective-member')

    const stats = await openTab(page, 'stats')
    await expect(stats.getByTestId('collective-org-line')).toHaveText('acme')
    await expect(stats.getByRole('button', { name: 'manage repositories' })).toHaveCount(0)

    await page.getByRole('button', { name: 'leave' }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toContainText(`leave ${MEMBER.name}?`)
    await expect(dialog.getByRole('button', { name: 'leave collective' })).toBeDisabled()
    await dialog.getByRole('button', { name: 'cancel' }).click()
    await expect(dialog).toHaveCount(0)
  })

  test('sends a signed-out reader of a public collective to the marketing log-in, coming back to it', async ({ page, request }) => {
    await setScenario(request, 'signed-out-public-collective')
    try {
      // The app keeps no signed-out view: a page load is served the frame, which
      // hands the reader to the marketing site's log-in popup once /auth/me
      // answers, carrying this collective as where to come back to.
      await page.route('https://peasantlabs.org/**', (route) =>
        route.fulfill({ status: 200, contentType: 'text/html', body: '<title>marketing</title>' }),
      )
      await page.goto(`/collectives/${VISITOR.id}`)
      await expect(page).toHaveURL(`https://peasantlabs.org/?login&next=${encodeURIComponent(`/collectives/${VISITOR.id}`)}`)
      const destination = new URL(page.url())
      expect(destination.protocol).toBe('https:')
      expect(destination.host).toBe('peasantlabs.org')
      expect(destination.pathname).toBe('/')
      expect(destination.searchParams.has('login')).toBe(true)
      expect(destination.searchParams.get('next')).toBe(`/collectives/${VISITOR.id}`)
      // The collective page never mounts for a reader without a session.
      await expect(page.getByTestId('collective-page')).toHaveCount(0)
    } finally {
      await setScenario(request, 'default')
    }
  })

  test('opens the repo picker, links a repository and says what it saved', async ({ page, theme }, testInfo) => {
    await page.goto(`/collectives/${OWNER.id}?tab=stats`)
    await expectTheme(page, theme)

    const stats = page.getByRole('tabpanel')
    await stats.getByRole('button', { name: 'manage repositories' }).click()
    const picker = page.getByRole('dialog', { name: 'link repositories from acme' })
    await expect(picker).toBeVisible()
    await expect(picker).toContainText('2 of 14 selected')
    await expect(picker).toContainText('3 members publish from it')

    // The row sits under its owner's heading, so it names the repository alone.
    const cliRow = picker.locator('.rpk-repo-row').filter({ has: picker.locator('.rpk-repo-name', { hasText: /^cli$/ }) })
    await cliRow.getByRole('checkbox').check()
    await expect(picker).toContainText('3 of 14 selected')
    await expect(picker.getByRole('button', { name: 'save: link 1 repository' })).toBeEnabled()
    await still(page, testInfo, 'repo-picker')
    await record(page, testInfo, picker, 'repo-picker')

    await picker.getByRole('button', { name: 'save: link 1 repository' }).click()
    await expect(page.getByRole('status').filter({ hasText: 'repositories saved' })).toContainText('linked acme/cli.')
    await expect(stats.getByTestId('collective-org-line')).toContainText('acme: 3 of 14 repos linked')
  })
})
