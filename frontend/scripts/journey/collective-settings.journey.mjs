/* Journey: a collective's settings.
 *
 * Opens the owner's collective settings through the real route against the
 * composed mock's collective fixtures (lib/collective-fixtures.mjs), one
 * section at a time (?section=): the section list, a text field in edit, a
 * choice card and a switch that settle, the page the GitHub App install
 * handshake returns to, the invite search, and the delete confirmation. Every
 * change is its own write; the journey asserts what the owner sees and the
 * state each control settles in.
 */
import { writeFileSync } from 'node:fs'
import { test, expect } from './lib/fixtures.mjs'
import { setScenario } from './lib/scenario.mjs'
import { scanAxe, seriousViolations, expectTheme, expectComputedTokens } from './lib/assertions.mjs'
import { loadCollectiveInviteSearchFixtures } from '../../src/test/collectiveInviteSearchFixtures.ts'
import { JOURNEY_COLLECTIVES } from './lib/collective-fixtures.mjs'

const OWNER = JOURNEY_COLLECTIVES.owner
const SETTINGS = `/collectives/${OWNER.id}/settings`
/** The section list, as the page draws it: its label, its address, and its title. */
const SECTIONS = [
  { label: 'general', search: '' },
  { label: 'access', search: '?section=access' },
  { label: 'members', search: '?section=members' },
  { label: 'github', search: '?section=github' },
  { label: 'pull requests', search: '?section=pull-requests' },
  { label: 'danger zone', search: '?section=danger' },
]

async function still(page, testInfo, name, locator) {
  const path = testInfo.outputPath(`${name}.png`)
  if (locator) await locator.screenshot({ path })
  else await page.screenshot({ path, fullPage: true })
  await testInfo.attach(`${name}.png`, { path, contentType: 'image/png' })
}

async function axe(page, testInfo) {
  const report = await scanAxe(page)
  writeFileSync(testInfo.outputPath('axe.json'), JSON.stringify(report, null, 2))
  await testInfo.attach('axe.json', { path: testInfo.outputPath('axe.json'), contentType: 'application/json' })
  const blocking = seriousViolations(report)
  expect(blocking, JSON.stringify(blocking, null, 2)).toEqual([])
}

test.describe('collective settings', () => {
  test.beforeEach(async ({ request }) => {
    await setScenario(request, 'default')
  })

  test('walks the sections from the section list, and draws no check-mode radios', async ({ page, theme }, testInfo) => {
    await page.goto(SETTINGS)
    await expectTheme(page, theme)

    const nav = page.getByRole('navigation', { name: 'settings sections' })
    await expect(nav.getByRole('link')).toHaveText([`back to ${OWNER.name}`, ...SECTIONS.map((s) => s.label)])
    await expect(nav.getByRole('link', { name: `back to ${OWNER.name}` })).toHaveAttribute('href', `/collectives/${OWNER.id}`)
    await expectComputedTokens(page, '.on-settings', { fontFamilyIncludes: 'atkinson', borderRadius: '0px', minFontSize: 16 })
    await still(page, testInfo, 'collective-settings')
    writeFileSync(testInfo.outputPath('settings-aria.yml'), await page.locator('.on-settings').ariaSnapshot())
    await testInfo.attach('settings-aria.yml', { path: testInfo.outputPath('settings-aria.yml'), contentType: 'text/yaml' })
    await axe(page, testInfo)

    for (const section of SECTIONS) {
      await nav.getByRole('link', { name: section.label, exact: true }).click()
      await expect(page).toHaveURL(new RegExp(`${SETTINGS}${section.search.replace('?', '\\?')}$`))
      await expect(page.getByRole('heading', { level: 1 })).toHaveText(section.label)
      await expect(nav.getByRole('link', { name: section.label, exact: true })).toHaveAttribute('aria-current', 'page')
      await expect(page.getByRole('radio')).toHaveCount(0)
    }

    await page.goto(`${SETTINGS}?section=pull-requests`)
    await expect(page.getByRole('switch', { name: 'show transcripts on pull requests' })).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByRole('link', { name: 'your settings' })).toHaveAttribute('href', '/settings')
    await page.goto(`${SETTINGS}?section=members`)
    await expect(page.getByRole('combobox', { name: 'role for @bob-ai' })).toHaveValue('member')
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('members')
    // An invite in an earlier journey adds to the member list the mock's reset starts from, so the line counts whoever is there.
    await expect(page.locator('.on-split p')).toHaveText(/^\d+ people\. invite by github username\.$/)
  })

  test('edits a text field only after edit, and saves it', async ({ page, theme }, testInfo) => {
    await page.goto(SETTINGS)
    await expectTheme(page, theme)

    const name = page.getByRole('textbox', { name: 'name', exact: true })
    await expect(name).toHaveCount(0)
    await page.getByRole('button', { name: 'edit name' }).click()
    await expect(name).toBeFocused()
    await name.fill('Acme Platform Team')
    await still(page, testInfo, 'settings-text-in-edit', page.locator('.on-settings-body'))

    const saved = page.waitForResponse((r) => r.request().method() === 'PATCH' && r.ok())
    await page.getByRole('button', { name: 'save', exact: true }).click()
    expect((await saved).request().postDataJSON()).toEqual({ name: 'Acme Platform Team' })
    // The row settles, and the name the server now stores reaches the breadcrumb.
    await expect(page.locator('.srow[data-status="settled"]', { hasText: 'Acme Platform Team' })).toContainText('saved')
    await expect(page.getByRole('navigation', { name: 'breadcrumb' })).toContainText('Acme Platform Team')
  })

  test('saves who can publish the moment a card is pressed', async ({ page, theme }, testInfo) => {
    await page.goto(`${SETTINGS}?section=access`)
    await expectTheme(page, theme)

    const publish = page.getByRole('group', { name: 'who can publish' })
    await expect(publish.getByRole('button', { pressed: true })).toContainText('anyone who joins')
    const saved = page.waitForResponse((r) => r.request().method() === 'PATCH' && r.ok())
    await publish.getByRole('button', { name: /^sessions you approve/ }).click()
    expect((await saved).request().postDataJSON()).toEqual({ acceptance_mode: 'curated' })
    await expect(publish.getByRole('button', { pressed: true })).toContainText('sessions you approve')
    await expect(page.locator('.on-choices', { has: publish }).locator('.on-choice-status')).toHaveText('saved')
    await still(page, testInfo, 'settings-card-saved', page.locator('.on-settings-body'))
    await axe(page, testInfo)
  })

  test('settles a switch the moment it changes', async ({ page, theme }, testInfo) => {
    await page.goto(`${SETTINGS}?section=pull-requests`)
    await expectTheme(page, theme)

    const toggle = page.getByRole('switch', { name: 'show transcripts on pull requests' })
    const saved = page.waitForResponse((r) => r.request().method() === 'PATCH' && r.ok())
    await toggle.click()
    expect((await saved).request().postDataJSON()).toEqual({ post_prompts_check: false })
    await expect(toggle).toHaveAttribute('aria-checked', 'false')

    const row = page.locator('.srow', { has: toggle })
    await expect(row).toHaveAttribute('data-status', 'settled')
    await expect(row).toContainText('saved')
    await still(page, testInfo, 'settings-switch-settled', page.locator('.on-settings-body'))
  })

  test('says github connected after the install handshake, and lands on the github section', async ({ page, theme }, testInfo) => {
    await page.goto(`${SETTINGS}?github_installed=1`)
    await expectTheme(page, theme)

    await expect(page.getByRole('status').filter({ hasText: 'github connected' })).toBeVisible()
    await expect(page).toHaveURL(new RegExp(`${SETTINGS}\\?section=github$`))
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('github')
    await expect(page.getByTestId('settings-org-card')).toContainText('2 of 14 repositories linked')
    await expect(page.getByRole('button', { name: 'manage repositories' })).toBeVisible()
    await still(page, testInfo, 'settings-github-connected')
  })

  test('retains github user search and confirms the selected invitation', async ({ page, theme }, testInfo) => {
    const entry = loadCollectiveInviteSearchFixtures().find((row) => row.name === 'invite-search-preserves-the-selected-github-handle')
    const searches = []
    await page.route('https://api.github.com/search/users*', (route) => {
      searches.push(new URL(route.request().url()).searchParams.get('q'))
      return route.fulfill({ json: { items: [{ login: entry.handle, avatar_url: '' }] } })
    })
    await page.goto(`${SETTINGS}?section=members`)
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
    await axe(page, testInfo)
    await choice.click()
    const invited = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/members'))
    await dialog.getByRole('button', { name: 'invite', exact: true }).click()
    expect((await invited).request().postDataJSON()).toEqual({ username: entry.handle })
    await expect(dialog).not.toBeVisible()
    await expect(page.getByTestId('collective-notice')).toContainText(`@${entry.handle} can now publish here.`)
    await expect(page.getByRole('list', { name: 'members' })).toContainText(`@${entry.handle}`)
  })

  test('deletes the collective only after the owner says they understand', async ({ page, theme }, testInfo) => {
    // The delete is answered here rather than by the mock, so the collective
    // every later journey reads stays where it is.
    const deletes = []
    await page.route(`**/api/v1/collectives/${OWNER.id}`, (route) => {
      if (route.request().method() !== 'DELETE') return route.fallback()
      deletes.push(route.request().url())
      return route.fulfill({ json: { status: 'deleted' } })
    })
    await page.goto(`${SETTINGS}?section=danger`)
    await expectTheme(page, theme)

    await page.getByRole('button', { name: 'delete collective' }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toContainText(`delete ${OWNER.name}?`)
    const confirm = dialog.getByRole('button', { name: 'delete collective' })
    await expect(confirm).toBeDisabled()
    await still(page, testInfo, 'settings-delete-confirm')
    await axe(page, testInfo)
    expect(deletes).toEqual([])
    await dialog.getByRole('checkbox', { name: 'i understand this cannot be undone' }).check()
    await confirm.click()
    await expect(page).toHaveURL(/\/collectives$/)
    expect(deletes).toHaveLength(1)
  })
})
