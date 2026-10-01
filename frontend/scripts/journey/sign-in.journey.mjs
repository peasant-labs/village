/* Journey: the village front door.
 *
 * A signed-out `/` is one GitHub sign-in page; a signed-in visitor gets the
 * `home | collectives` nav and an account menu holding profile and sign out.
 * Each test ends on the state its evidence screenshot shows, in both themes:
 * the sign-in page (desktop and phone), the signed-in nav, and the account menu
 * open. Assertions are on roles, names and computed tokens, not class strings,
 * and the sign-in page's ARIA tree and axe report are kept as artifacts.
 */
import { writeFileSync } from 'node:fs'
import { test, expect } from './lib/fixtures.mjs'
import { setScenario } from './lib/scenario.mjs'
import { scanAxe, seriousViolations, expectTheme, expectComputedTokens } from './lib/assertions.mjs'

const HEADLINE = "the agent sessions behind your team's pull requests"

async function attachJSON(testInfo, name, value) {
  writeFileSync(testInfo.outputPath(name), JSON.stringify(value, null, 2))
  await testInfo.attach(name, { path: testInfo.outputPath(name), contentType: 'application/json' })
}

test.describe('sign-in', () => {
  test('a signed-out root is one github sign-in', async ({ page, request, theme }, testInfo) => {
    await setScenario(request, 'signed-out')
    try {
      // Leaving for GitHub is answered 204 so the browser stays on the page:
      // the journey proves where the button leads without leaving the app.
      // Every provider start route is `/api/v1/auth/<provider>`; `/auth/me`
      // is the session read and passes through to the mock.
      const oauthStarts = []
      await page.route(
        (url) => /\/api\/v1\/auth\/[a-z]+$/.test(url.pathname) && url.pathname !== '/api/v1/auth/me',
        (route) => {
          oauthStarts.push(new URL(route.request().url()).pathname)
          return route.fulfill({ status: 204 })
        },
      )

      await page.goto('/')
      await expectTheme(page, theme)

      const signIn = page.getByTestId('sign-in-page')
      await expect(signIn.locator('.cmg-signin-brand')).toHaveText('village')
      await expectComputedTokens(page, '.cmg-signin-card', { borderRadius: '0px' })
      expect(await signIn.locator('.cmg-signin-card').evaluate((card) => getComputedStyle(card).borderTopWidth)).toBe('1px')
      await expect(signIn.getByRole('heading', { level: 1, name: HEADLINE })).toBeVisible()

      // One front door: one GitHub button, nothing behind a chevron, and no
      // second button in the header.
      const button = signIn.getByRole('button', { name: /continue with github/i })
      await expect(button).toBeVisible()
      await expect(page.getByRole('button', { name: 'more sign-in providers' })).toHaveCount(0)
      await expect(page.locator('header').getByRole('button', { name: /continue with/i })).toHaveCount(0)
      // A signed-out visitor is offered no nav entries.
      await expect(page.getByRole('navigation', { name: 'main navigation' })).toHaveCount(0)

      // The handle note and the CLI note are the two things a newcomer asks next.
      await expect(signIn.getByText('first time here? your handle is your github login.', { exact: false })).toBeVisible()
      await expect(signIn.getByText('peasant village login')).toBeVisible()

      await expectComputedTokens(page, '[data-testid="sign-in-page"] p', {
        fontFamilyIncludes: 'atkinson',
        minFontSize: 16,
      })
      await expectComputedTokens(page, '[data-testid="sign-in-page"] .si-split-primary', {
        borderRadius: '0px',
      })

      const aria = await signIn.ariaSnapshot()
      writeFileSync(testInfo.outputPath('sign-in-aria.yml'), aria)
      await testInfo.attach('sign-in-aria.yml', {
        path: testInfo.outputPath('sign-in-aria.yml'),
        contentType: 'text/yaml',
      })
      const axe = await scanAxe(page)
      await attachJSON(testInfo, 'axe.json', axe)
      const blocking = seriousViolations(axe)
      expect(blocking, JSON.stringify(blocking, null, 2)).toEqual([])

      await button.click()
      await expect.poll(() => oauthStarts).toEqual(['/api/v1/auth/github'])
      await expect(signIn.getByRole('heading', { level: 1, name: HEADLINE })).toBeVisible()
    } finally {
      await setScenario(request, 'default')
    }
  })

  test('the sign-in page fits a phone', async ({ page, request, theme }) => {
    await setScenario(request, 'signed-out')
    try {
      await page.setViewportSize({ width: 390, height: 844 })
      await page.goto('/')
      await expectTheme(page, theme)
      const signIn = page.getByTestId('sign-in-page')
      await expect(signIn.getByRole('heading', { level: 1, name: HEADLINE })).toBeVisible()
      await expect(signIn.getByRole('button', { name: /continue with github/i })).toBeVisible()
      // No horizontal scroll at phone width.
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      )
      expect(overflow).toBeLessThanOrEqual(0)
    } finally {
      await setScenario(request, 'default')
    }
  })

  test('a signed-in nav is home and collectives', async ({ page, theme }) => {
    await page.goto('/')
    await expectTheme(page, theme)

    const nav = page.getByRole('navigation', { name: 'main navigation' })
    await expect(nav.getByRole('link')).toHaveText(['home', 'collectives'])
    await expect(nav.getByRole('link', { name: 'home' })).toHaveAttribute('aria-current', 'page')
    await expect(nav.getByRole('link', { name: 'collectives' })).not.toHaveAttribute('aria-current', 'page')
    // Explore and publish keep their routes but leave the nav.
    await expect(nav.getByRole('link', { name: 'explore' })).toHaveCount(0)
    await expect(nav.getByRole('link', { name: 'publish' })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'account menu for @alice-dev' })).toBeVisible()
    // Signed in, the page never offers sign-in.
    await expect(page.getByRole('button', { name: /continue with/i })).toHaveCount(0)
  })

  test('the account menu holds profile, settings and sign out', async ({ page, theme }) => {
    await page.goto('/')
    await expectTheme(page, theme)

    const trigger = page.getByRole('button', { name: 'account menu for @alice-dev' })
    await trigger.click()
    const menu = page.getByRole('menu')
    await expect(menu.getByRole('menuitem')).toHaveText(['profile', 'settings', 'sign out'])
    await expect(trigger).toHaveAttribute('aria-expanded', 'true')
    // Opening moves focus to the first row one frame later (fairtrade's Menu
    // defers it to the next animation frame). Keys go to the row, not the
    // trigger, only once it has arrived.
    await expect(menu.getByRole('menuitem').first()).toBeFocused()

    // Escape closes it and gives focus back to the trigger.
    await page.keyboard.press('Escape')
    await expect(trigger).toHaveAttribute('aria-expanded', 'false')
    await expect(trigger).toBeFocused()

    // Reopen from the keyboard for the evidence frame.
    await page.keyboard.press('ArrowDown')
    await expect(menu.getByRole('menuitem').first()).toBeFocused()
  })
  test('header sign-out announces pending and failed requests with retry', async ({ page, theme }, testInfo) => {
    let posts = 0
    let pendingRoute
    await page.route(/\/api\/v1\/auth\/logout$/, async (route) => {
      posts += 1
      pendingRoute = route
    })
    await page.goto('/')
    await expectTheme(page, theme)
    await page.getByRole('button', { name: 'account menu for @alice-dev' }).click()
    await page.getByRole('menuitem', { name: 'sign out' }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByText('waiting for the sign-out request.')).toBeVisible()
    await expect.poll(() => posts).toBe(1)
    await expect(dialog.getByRole('button', { name: 'close dialog' })).toBeDisabled()
    await page.evaluate(() => window.scrollTo(0, 0))
    const pending = testInfo.outputPath(`header-sign-out-pending-${theme}.png`)
    await page.screenshot({ path: pending, fullPage: false })
    await testInfo.attach('header-sign-out-pending', { path: pending, contentType: 'image/png' })
    await pendingRoute.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'sign-out service unavailable' }) })
    await expect(dialog.getByRole('alert')).toContainText('sign-out service unavailable')
    await expect(dialog.getByRole('button', { name: 'try again' })).toBeEnabled()
    const failed = testInfo.outputPath(`header-sign-out-failed-${theme}.png`)
    await page.screenshot({ path: failed, fullPage: false })
    await testInfo.attach('header-sign-out-failed', { path: failed, contentType: 'image/png' })
    await expect(page.getByRole('button', { name: 'account menu for @alice-dev', includeHidden: true })).toBeVisible()
    const axe = await scanAxe(page)
    await attachJSON(testInfo, 'header-sign-out-axe.json', axe)
    expect(seriousViolations(axe)).toEqual([])
    await dialog.getByRole('button', { name: 'try again' }).click()
    await expect.poll(() => posts).toBe(2)
    await pendingRoute.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'sign-out service unavailable' }) })
    await expect(dialog.getByRole('alert')).toContainText('sign-out service unavailable')
  })

})
