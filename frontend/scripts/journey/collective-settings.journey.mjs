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
})
