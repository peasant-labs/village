/* Journey: a collective's settings.
 *
 * Opens the owner's collective settings through the real route against the
 * composed mock's collective fixtures (lib/collective-fixtures.mjs): the
 * sections, a text field in edit, a switch that settles, and the page the
 * GitHub App install handshake returns to. Every change is its own write; the
 * journey asserts what the owner sees and the state each row settles in.
 */
import { writeFileSync } from 'node:fs'
import { test, expect } from './lib/fixtures.mjs'
import { setScenario } from './lib/scenario.mjs'
import { scanAxe, seriousViolations, expectTheme, expectComputedTokens } from './lib/assertions.mjs'
import { loadCollectiveInviteSearchFixtures } from '../../src/test/collectiveInviteSearchFixtures.ts'
import { JOURNEY_COLLECTIVES } from './lib/collective-fixtures.mjs'

const OWNER = JOURNEY_COLLECTIVES.owner
const SETTINGS = `/groups/${OWNER.id}/settings`
const SECTIONS = ['general', 'access', 'members', 'github orgs', 'pull requests', 'danger zone']

async function still(page, testInfo, name, locator) {
  const path = testInfo.outputPath(`${name}.png`)
  if (locator) await locator.screenshot({ path })
  else await page.screenshot({ path, fullPage: true })
  await testInfo.attach(`${name}.png`, { path, contentType: 'image/png' })
}

test.describe('collective settings', () => {
  test.beforeEach(async ({ request }) => {
    await setScenario(request, 'default')
  })

  test('shows the sections, and no check-mode radios', async ({ page, theme }, testInfo) => {
    await page.goto(SETTINGS)
    await expectTheme(page, theme)

    const nav = page.getByRole('navigation', { name: 'settings sections' })
    await expect(nav.getByRole('link')).toHaveText(SECTIONS)
    for (const section of SECTIONS) {
      await expect(page.locator('summary', { hasText: section })).toBeVisible()
    }
    await expect(page.getByRole('radio')).toHaveCount(0)
    await expect(page.getByRole('switch', { name: 'show transcripts on pull requests' })).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByRole('combobox', { name: 'role for @bob-ai' })).toHaveValue('member')
    await expectComputedTokens(page, '.cmg-settings', { fontFamilyIncludes: 'atkinson', borderRadius: '0px', minFontSize: 16 })

    await still(page, testInfo, 'collective-settings')
    writeFileSync(testInfo.outputPath('settings-aria.yml'), await page.locator('.cmg-settings').ariaSnapshot())
    await testInfo.attach('settings-aria.yml', { path: testInfo.outputPath('settings-aria.yml'), contentType: 'text/yaml' })
    const axe = await scanAxe(page)
    writeFileSync(testInfo.outputPath('axe.json'), JSON.stringify(axe, null, 2))
    await testInfo.attach('axe.json', { path: testInfo.outputPath('axe.json'), contentType: 'application/json' })
    const blocking = seriousViolations(axe)
    expect(blocking, JSON.stringify(blocking, null, 2)).toEqual([])
  })

  test('edits a text field only after edit, and saves it', async ({ page, theme }, testInfo) => {
    await page.goto(SETTINGS)
    await expectTheme(page, theme)

    const name = page.getByRole('textbox', { name: 'name', exact: true })
    await expect(name).toHaveCount(0)
    await page.getByRole('button', { name: 'edit name' }).click()
    await expect(name).toBeFocused()
    await name.fill('Acme Platform Team')
    await still(page, testInfo, 'settings-text-in-edit', page.locator('details', { hasText: 'general' }).first())

    const saved = page.waitForResponse((r) => r.request().method() === 'PATCH' && r.ok())
    await page.getByRole('button', { name: 'save', exact: true }).click()
    expect((await saved).request().postDataJSON()).toEqual({ name: 'Acme Platform Team' })
    // The row settles, and the name the server now stores reaches the heading.
    await expect(page.locator('.srow[data-status="settled"]', { hasText: 'Acme Platform Team' })).toContainText('saved')
    await expect(page.getByRole('heading', { name: 'Acme Platform Team · settings' })).toBeVisible()
  })

  test('settles a switch the moment it changes', async ({ page, theme }, testInfo) => {
    await page.goto(SETTINGS)
    await expectTheme(page, theme)

    const toggle = page.getByRole('switch', { name: 'show transcripts on pull requests' })
    const saved = page.waitForResponse((r) => r.request().method() === 'PATCH' && r.ok())
    await toggle.click()
    expect((await saved).request().postDataJSON()).toEqual({ post_prompts_check: false })
    await expect(toggle).toHaveAttribute('aria-checked', 'false')

    const row = page.locator('.srow', { has: toggle })
    await expect(row).toHaveAttribute('data-status', 'settled')
    await expect(row).toContainText('saved')
    await still(page, testInfo, 'settings-switch-settled', page.locator('details', { hasText: 'pull requests' }).first())
  })

  test('says github connected after the install handshake, and drops the parameter', async ({ page, theme }, testInfo) => {
    await page.goto(`${SETTINGS}?github_installed=1`)
    await expectTheme(page, theme)

    await expect(page.getByRole('status').filter({ hasText: 'github connected' })).toBeVisible()
    await expect(page).toHaveURL(new RegExp(`${SETTINGS}$`))
    const github = page.locator('details', { hasText: 'github orgs' }).first()
    await expect(github).toContainText('2 of 14 repos linked')
    await still(page, testInfo, 'settings-github-connected')
  })
  test('retains github user search and confirms the selected invitation', async ({ page, theme }, testInfo) => {
    const entry = loadCollectiveInviteSearchFixtures().find((row) => row.name === 'invite-search-preserves-the-selected-github-handle')
    const searches = []
    await page.route('https://api.github.com/search/users*', (route) => {
      searches.push(new URL(route.request().url()).searchParams.get('q'))
      return route.fulfill({ json: { items: [{ login: entry.handle, avatar_url: '' }] } })
    })
    await page.goto(SETTINGS)
    await expectTheme(page, theme)
    await page.getByRole('button', { name: 'find a github user to invite' }).click()
    const dialog = page.getByRole('dialog', { name: 'invite a github user' })
    await dialog.getByRole('combobox', { name: 'github username' }).fill(entry.query)
    const choice = dialog.getByRole('menuitem', { name: entry.handle })
    await expect(choice).toBeVisible()
    expect(searches).toEqual([entry.query])
    const renderedHandle = await choice.locator('.menu-text').evaluate((element) => {
      const style = getComputedStyle(element)
      return { size: parseFloat(style.fontSize), transform: style.textTransform, text: element.textContent }
    })
    expect(renderedHandle.size).toBeGreaterThanOrEqual(14)
    expect(renderedHandle.transform).toBe('none')
    expect(renderedHandle.text).toBe(entry.handle)
    await page.evaluate(() => window.scrollTo(0, 0))
    await still(page, testInfo, 'settings-github-user-search')
    const axe = await scanAxe(page)
    expect(seriousViolations(axe), JSON.stringify(seriousViolations(axe))).toEqual([])
    await choice.click()
    const invited = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/members'))
    await dialog.getByRole('button', { name: 'invite', exact: true }).click()
    expect((await invited).request().postDataJSON()).toEqual({ username: entry.handle })
    await expect(dialog).not.toBeVisible()
    await expect(page.getByTestId('collective-notice')).toContainText(`@${entry.handle} can now publish here.`)
  })

})
