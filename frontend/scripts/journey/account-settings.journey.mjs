/* Journey: account settings.
 *
 * Reaches `/settings` the way a person does, from the account menu, and checks
 * the page's rows: the handle, the discoverable switch, the automatic pull
 * request row (shown, and not yet available), the account village signs in
 * with, where peasant is signed in, and the danger zone. Then it opens the page
 * for a person whose peasant is signed in nowhere, and tries a handle somebody
 * else holds. Each test records its still frame, the accessible tree and an axe
 * report.
 */
import { writeFileSync } from 'node:fs'
import { test, expect } from './lib/fixtures.mjs'
import { setScenario } from './lib/scenario.mjs'
import { scanAxe, seriousViolations, expectTheme, expectComputedTokens } from './lib/assertions.mjs'
import { TAKEN_HANDLE } from './lib/home-fixtures.mjs'

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

test.describe('account settings', () => {
  test('opens from the account menu and shows every row', async ({ page, theme }, testInfo) => {
    await page.goto('/')
    await expect(page.getByTestId('home-page')).toBeVisible()
    await page.getByRole('button', { name: /account menu/ }).click()
    await page.getByRole('link', { name: 'settings' }).click()
    await expect(page).toHaveURL(/\/settings$/)

    const settings = page.getByTestId('settings-page')
    await expect(settings).toBeVisible()
    await expectTheme(page, theme)
    await expect(page.getByRole('heading', { level: 1, name: 'your settings' })).toBeVisible()

    await expect(page.getByTestId('settings-handle')).toContainText('alice-dev')
    await expect(page.getByRole('switch', { name: 'discoverable profile' })).toHaveAttribute('aria-checked', 'true')
    const autoLink = page.getByRole('switch', { name: 'link my transcripts to my pull requests automatically' })
    await expect(autoLink).toBeDisabled()
    await expect(page.getByTestId('settings-auto-link')).toContainText('not available yet')
    await expect(page.getByTestId('settings-sign-in-account')).toContainText('alice-dev')
    // Two working sign-ins; the revoked one is not counted.
    await expect(page.getByTestId('settings-peasant')).toContainText('peasant on 2 computers')
    await expect(page.getByTestId('settings-peasant')).toContainText('last used 12m ago')
    await expect(page.getByTestId('settings-delete')).toContainText('this cannot be undone')

    await expectComputedTokens(page, '[data-testid="settings-page"]', {
      fontFamilyIncludes: 'atkinson',
      borderRadius: '0px',
      minFontSize: 16,
    })
    await expectNoSeriousAxe(page, testInfo)
    await evidence(page, testInfo, 'settings-keys', settings)
  })

  test('says peasant is signed in nowhere', async ({ page, request, theme }, testInfo) => {
    await setScenario(request, 'no-keys')
    try {
      await page.goto('/settings')
      const settings = page.getByTestId('settings-page')
      await expect(settings).toBeVisible()
      await expectTheme(page, theme)
      await expect(page.getByTestId('settings-peasant')).toContainText('peasant is not signed in on any computer')
      await expect(page.getByTestId('settings-peasant').getByRole('button')).toHaveCount(0)

      await expectNoSeriousAxe(page, testInfo)
      await evidence(page, testInfo, 'settings-no-keys', settings)
    } finally {
      await setScenario(request, 'default')
    }
  })

  test('refuses a handle somebody else holds', async ({ page, theme }, testInfo) => {
    await page.goto('/settings')
    const settings = page.getByTestId('settings-page')
    await expect(settings).toBeVisible()
    await expectTheme(page, theme)

    await page.getByRole('button', { name: 'edit handle' }).click()
    await page.getByRole('textbox', { name: 'handle' }).fill(TAKEN_HANDLE)
    await page.getByTestId('settings-handle').getByRole('button', { name: 'save' }).click()
    await expect(page.getByTestId('settings-handle').getByRole('alert')).toHaveText(
      `@${TAKEN_HANDLE} is already claimed. try another.`,
    )
    // The handle the person has is kept.
    await expect(page.getByTestId('settings-handle')).toContainText('alice-dev')

    await expectNoSeriousAxe(page, testInfo)
    await evidence(page, testInfo, 'settings-taken-handle', settings)
  })
})
