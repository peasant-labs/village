/* Journey: the collectives list and a collective.
 *
 * Opens the collectives list (a table with a search), a collective as its
 * owner and as a member, and the repo picker the owner opens from the
 * `github orgs` rail, all through the real routes against the composed mock's
 * collective fixtures (lib/collective-fixtures.mjs). It asserts roles, names
 * and computed tokens rather than class strings, records the accessible tree
 * and the axe report, and attaches the still a reviewer reads first.
 */
import { writeFileSync } from 'node:fs'
import { test, expect } from './lib/fixtures.mjs'
import { setScenario } from './lib/scenario.mjs'
import { scanAxe, seriousViolations, expectTheme, expectComputedTokens } from './lib/assertions.mjs'
import { JOURNEY_COLLECTIVES, RETAINED_CONTRIBUTIONS, RETAINED_GROUPED } from './lib/collective-fixtures.mjs'

const OWNER = JOURNEY_COLLECTIVES.owner
const MEMBER = JOURNEY_COLLECTIVES.member

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

  test('lists the collectives in a table and searches them', async ({ page, theme }, testInfo) => {
    await page.goto('/groups')
    await expectTheme(page, theme)

    const table = page.getByRole('table', { name: 'collectives 3' })
    await expect(table).toBeVisible()
    for (const { name } of Object.values(JOURNEY_COLLECTIVES)) {
      await expect(table.getByRole('button', { name })).toBeVisible()
    }
    await expectComputedTokens(page, '.iu-page', { fontFamilyIncludes: 'atkinson', borderRadius: '0px', minFontSize: 16 })
    await still(page, testInfo, 'collectives-list')
    await record(page, testInfo, table, 'collectives-list')

    await page.getByRole('searchbox', { name: 'collective or github org' }).fill('acme')
    await page.getByRole('button', { name: 'search' }).click()
    const results = page.getByRole('table', { name: 'search results 2' })
    await expect(results.getByRole('button', { name: OWNER.name })).toBeVisible()
    await expect(results.getByRole('button', { name: JOURNEY_COLLECTIVES.visitor.name })).toHaveCount(0)

    await results.getByRole('button', { name: OWNER.name }).click()
    await expect(page).toHaveURL(new RegExp(`/groups/${OWNER.id}$`))
  })

  test('shows the owner the boxes, the stats, the table and the github orgs rail', async ({ page, theme }, testInfo) => {
    await page.goto(`/groups/${OWNER.id}`)
    await expectTheme(page, theme)

    await expect(page.getByRole('heading', { name: OWNER.name })).toBeVisible()
    const boxes = page.getByRole('list', { name: 'how this collective works' })
    await expect(boxes.getByRole('listitem')).toHaveText([
      /who can read\s*members of Acme Platform can read every transcript published here\./,
      /who can publish\s*anyone can join and publish\. transcripts are approved automatically\./,
      /your role\s*owner: you manage settings and members\./,
    ])
    const rail = page.getByRole('complementary', { name: 'github orgs and members' })
    await expect(rail).toContainText('acme · 2 of 14 repos linked')
    await expect(rail.getByRole('button', { name: 'manage' })).toBeVisible()
    await expect(page.getByRole('table', { name: 'transcripts 248' })).toContainText('Fix flaky ingest test')
    await expect(page.getByRole('button', { name: 'settings' })).toBeVisible()

    await page.getByRole('button', { name: 'more', exact: true }).click()
    await expect(page.getByRole('menuitem')).toHaveText(['publish several transcripts'])
    await page.keyboard.press('Escape')
    await expect(page.getByRole('menu')).toHaveCount(0)

    await still(page, testInfo, 'collective-owner')
    await record(page, testInfo, page.getByRole('main'), 'collective-owner')
  })

  test('retains transcript grouping and private contribution withdrawal on the mounted detail page', async ({ page, theme }, testInfo) => {
    await page.goto(`/groups/${OWNER.id}`)
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
    const laterPage = page.waitForResponse((response) => response.url().includes(`/groups/${OWNER.id}/my-shares?`) && new URL(response.url()).searchParams.get('page') === '2')
    await continuation.getByRole('button', { name: /load/ }).click()
    expect((await laterPage).ok()).toBe(true)
    const helpers = contribution.locator(`[data-group-id="${RETAINED_GROUPED.groupID}"]`)
    await helpers.getByTestId('helper-group-toggle').click()
    await expect(helpers.getByRole('link', { name: RETAINED_GROUPED.title })).toBeVisible()
    await expect(library.getByRole('alert')).toHaveCount(0)
    await library.getByRole('button', { name: 'repos', exact: true }).click()
    await expect(library).toContainText('repositories')
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
    await page.goto(`/groups/${MEMBER.id}`)
    await expectTheme(page, theme)

    await expect(page.getByRole('heading', { name: MEMBER.name })).toBeVisible()
    await expect(page.getByRole('list', { name: 'how this collective works' })).toContainText('member: you read and publish here.')
    const rail = page.getByRole('complementary', { name: 'github orgs and members' })
    await expect(rail).toContainText('acme')
    await expect(rail.getByRole('button', { name: 'manage' })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'settings' })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'leave' })).toBeVisible()

    await still(page, testInfo, 'collective-member')
    await record(page, testInfo, page.getByRole('main'), 'collective-member')
  })

  test('opens the repo picker, links a repository and says what it saved', async ({ page, theme }, testInfo) => {
    await page.goto(`/groups/${OWNER.id}`)
    await expectTheme(page, theme)

    const rail = page.getByRole('complementary', { name: 'github orgs and members' })
    await rail.getByRole('button', { name: 'manage' }).click()
    const picker = page.getByRole('dialog', { name: 'link repositories from acme' })
    await expect(picker).toBeVisible()
    await expect(picker).toContainText('2 of 14 selected')
    await expect(picker).toContainText('3 members publish from it')

    await picker.locator('.rpk-repo-row', { hasText: 'acme/cli' }).getByRole('checkbox').check()
    await expect(picker).toContainText('3 of 14 selected')
    await expect(picker.getByRole('button', { name: 'save: link 1 repository' })).toBeEnabled()
    await still(page, testInfo, 'repo-picker')
    await record(page, testInfo, picker, 'repo-picker')

    await picker.getByRole('button', { name: 'save: link 1 repository' }).click()
    await expect(page.getByRole('status').filter({ hasText: 'repositories saved' })).toContainText('linked acme/cli.')
    await expect(rail).toContainText('acme · 3 of 14 repos linked')
  })
})
