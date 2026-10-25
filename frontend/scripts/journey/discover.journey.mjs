/* Journey: finding a collective to join, and starting a new one.
 *
 * Opens /discover (the collectives the viewer may see and has not joined),
 * searches it, joins an open collective and is refused by a verified one, then
 * creates a collective from /collectives through the new-collective dialog,
 * adding one person the server knows and one it refuses. Everything runs
 * through the real routes against the composed mock (lib/collective-fixtures.mjs);
 * it asserts roles, names and computed tokens, and records the accessible tree
 * and the axe report.
 */
import { writeFileSync } from 'node:fs'
import { test, expect } from './lib/fixtures.mjs'
import { setScenario } from './lib/scenario.mjs'
import { scanAxe, seriousViolations, expectTheme, expectComputedTokens } from './lib/assertions.mjs'
import { DISCOVER_COLLECTIVES, JOURNEY_COLLECTIVES } from './lib/collective-fixtures.mjs'

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

test.describe('discover and new collective', () => {
  test.afterEach(async ({ request }) => {
    await setScenario(request, 'default')
  })

  test('discovers, searches and joins collectives', async ({ page, request, theme }, testInfo) => {
    await setScenario(request, 'default')
    await page.goto('/discover')
    await expectTheme(page, theme)

    const list = page.getByRole('list', { name: 'collectives you can join' })
    await expect(list).toBeVisible()
    const { open, verified, curated } = DISCOVER_COLLECTIVES
    for (const { name } of [JOURNEY_COLLECTIVES.visitor, open, verified, curated]) {
      await expect(list.getByRole('link', { name, exact: true })).toBeVisible()
    }
    // A collective the viewer belongs to is on /collectives, not here.
    await expect(list.getByRole('link', { name: JOURNEY_COLLECTIVES.owner.name, exact: true })).toHaveCount(0)
    // Invite only: no join, a view link instead.
    await expect(page.getByRole('button', { name: `join ${curated.name}` })).toHaveCount(0)
    await expect(page.getByRole('link', { name: `view ${curated.name}` })).toHaveAttribute('href', `/collectives/${curated.id}`)
    await expectComputedTokens(page, '[data-testid="discover-page"]', { fontFamilyIncludes: 'atkinson', borderRadius: '0px', minFontSize: 16 })
    await still(page, testInfo, 'discover')
    await record(page, testInfo, list, 'discover')

    const joined = page.waitForResponse((r) => r.request().method() === 'POST' && r.url().endsWith(`/collectives/${open.id}/join`))
    await page.getByRole('button', { name: `join ${open.name}` }).click()
    expect((await joined).ok()).toBe(true)
    await expect(page.getByRole('link', { name: `open ${open.name}` })).toHaveAttribute('href', `/collectives/${open.id}`)

    await page.getByRole('button', { name: `join ${verified.name}` }).click()
    await expect(page.getByTestId('discover-page').getByRole('alert')).toContainText(`This collective requires membership of @${verified.org}`)
    await still(page, testInfo, 'discover-joined')

    await page.getByRole('searchbox', { name: 'find a collective' }).fill('acme')
    const results = page.getByRole('list', { name: 'collectives matching acme' })
    await expect(results.getByRole('link', { name: JOURNEY_COLLECTIVES.owner.name, exact: true })).toBeVisible()
    await expect(results.getByRole('link', { name: `open ${JOURNEY_COLLECTIVES.owner.name}` })).toBeVisible()
    await page.getByRole('button', { name: 'clear search' }).click()
    await expect(list).toBeVisible()
  })

  test('creates a collective and reports who was invited', async ({ page, request, theme }, testInfo) => {
    await setScenario(request, 'viewer-in-org')
    await page.goto('/collectives')
    await expectTheme(page, theme)
    await page.getByRole('button', { name: 'new collective' }).click()
    const dialog = page.getByRole('dialog', { name: 'new collective' })
    await expect(dialog).toBeVisible()
    await expect(dialog.getByRole('textbox', { name: 'name' })).toBeFocused()

    await dialog.getByRole('textbox', { name: 'name' }).fill('Eval Crowd')
    await dialog.getByRole('textbox', { name: 'purpose' }).fill('Agent evaluation runs for the spring term.')
    await dialog.getByRole('button', { name: 'next', exact: true }).click()

    const publish = dialog.getByRole('radiogroup', { name: 'who can publish' })
    await expect(publish.getByRole('radio', { name: 'anyone who joins' })).toBeFocused()
    // Arrow keys move the choice, as in any radio group.
    await page.keyboard.press('ArrowRight')
    await expect(publish.getByRole('radio', { name: 'your github org' })).toBeChecked()
    await expect(dialog.getByRole('combobox', { name: 'which github org' })).toHaveValue('acme')
    await publish.getByRole('radio', { name: 'sessions you approve' }).click()
    await dialog.getByRole('radiogroup', { name: 'who can read' }).getByRole('radio', { name: 'members and contributors' }).click()
    await still(page, testInfo, 'new-collective-access')
    await dialog.getByRole('button', { name: 'next', exact: true }).click()

    const invite = dialog.getByRole('textbox', { name: 'invite by username' })
    for (const username of ['tomas-r', 'nobody-here']) {
      await invite.fill(username)
      await invite.press('Enter')
    }
    await expect(dialog.getByRole('list', { name: 'people to invite' }).getByRole('listitem')).toHaveCount(2)
    const created = page.waitForResponse((r) => r.request().method() === 'POST' && r.url().endsWith('/collectives'))
    await dialog.getByRole('button', { name: 'create collective' }).click()
    const body = (await created).request().postDataJSON()
    expect(body).toEqual({ name: 'Eval Crowd', description: 'Agent evaluation runs for the spring term.', acceptance_mode: 'curated', data_access: 'contributors' })

    await expect(dialog.getByRole('heading', { name: 'Eval Crowd is ready' })).toBeVisible()
    // Every name is invited alike, whether or not an account holds it.
    await expect(dialog.getByRole('status')).toContainText('2 invitations sent.')
    await expect(dialog.getByRole('list', { name: 'invitations not sent' })).toHaveCount(0)
    const id = (await (await created).json()).id
    await expect(dialog.getByRole('link', { name: 'open Eval Crowd' })).toHaveAttribute('href', `/collectives/${id}`)
    await expect(dialog.getByRole('link', { name: 'invite more people' })).toHaveAttribute('href', `/collectives/${id}/settings?section=members`)
    await still(page, testInfo, 'new-collective-done')
    await record(page, testInfo, dialog, 'new-collective-done')

    await page.keyboard.press('Escape')
    await expect(dialog).toHaveCount(0)
    await expect(page.getByRole('table', { name: 'collectives you belong to' }).getByRole('link', { name: 'Eval Crowd', exact: true })).toBeVisible()
  })
})
