/* Journey: the front door.
 *
 * A visitor with no session at `/` is sent to the marketing site; signing in
 * starts at `/login`, one GitHub button, and a page that asked a signed-out
 * reader to sign in is where they come back to. A signed-in visitor gets the
 * sidebar and an account menu holding profile, settings and sign out. Each test
 * ends on the state its evidence screenshot shows, in both themes. Assertions
 * are on roles, names and computed tokens, and the sign-in page's ARIA tree and
 * axe report are kept as artifacts.
 */
import { writeFileSync } from 'node:fs'
import { test, expect } from './lib/fixtures.mjs'
import { setScenario } from './lib/scenario.mjs'
import { scanAxe, seriousViolations, expectTheme, expectComputedTokens } from './lib/assertions.mjs'

const HEADLINE = 'log in to peasant'
const MARKETING = 'https://peasantlabs.org/'

async function attachJSON(testInfo, name, value) {
  writeFileSync(testInfo.outputPath(name), JSON.stringify(value, null, 2))
  await testInfo.attach(name, { path: testInfo.outputPath(name), contentType: 'application/json' })
}

/** Answers every OAuth start route with 204 so the browser stays on the page,
 *  and records which provider each one was. `/auth/me` passes through. */
async function recordOAuthStarts(page) {
  const starts = []
  await page.route(
    (url) => /\/api\/v1\/auth\/[a-z]+$/.test(url.pathname) && url.pathname !== '/api/v1/auth/me',
    (route) => {
      starts.push(new URL(route.request().url()).pathname)
      return route.fulfill({ status: 204 })
    },
  )
  return starts
}

test.describe('sign-in', () => {
  test('a visitor with no session at the root is sent to the marketing site', async ({ request }) => {
    // The server decides before any page is served: no cookie, no app.
    const response = await request.get('/', { headers: { 'Sec-Fetch-Dest': 'document' }, maxRedirects: 0 })
    expect(response.status()).toBe(307)
    expect(response.headers()['location']).toBe(MARKETING)
  })

  test('a stale session at the root also leaves for the marketing site', async ({ page, request }) => {
    await setScenario(request, 'signed-out')
    try {
      // The browser holds a cookie the API no longer accepts: the page asks
      // /auth/me, hears no session, and leaves.
      await page.route(`${MARKETING}**`, (route) => route.fulfill({ status: 200, contentType: 'text/html', body: '<title>marketing</title>' }))
      await page.goto('/')
      await expect(page).toHaveURL(MARKETING)
    } finally {
      await setScenario(request, 'default')
    }
  })

  test('a signed-out page links home to the marketing site and fetches nothing cross-origin', async ({ page, request, theme }) => {
    await setScenario(request, 'signed-out')
    try {
      const problems = []
      page.on('console', (msg) => { if (msg.type() === 'error' && /peasantlabs\.org|CORS/i.test(msg.text())) problems.push(msg.text()) })
      page.on('requestfailed', (req) => { if (req.url().includes('peasantlabs.org')) problems.push(`${req.url()} ${req.failure()?.errorText}`) })
      // The app's own fetches of a route (prefetches and in-app navigations)
      // must be answered by the app, never redirected to another origin.
      page.on('response', (res) => {
        const req = res.request()
        if (req.resourceType() === 'fetch' && res.status() >= 300 && res.status() < 400 && (res.headers()['location'] ?? '').includes('peasantlabs.org')) {
          problems.push(`${req.url()} answered ${res.status()} to ${res.headers()['location']}`)
        }
      })
      await page.route(`${MARKETING}**`, (route) => route.fulfill({ status: 200, contentType: 'text/html', body: '<title>marketing</title>' }))
      await page.goto('/privacy')
      await expectTheme(page, theme)
      const bar = page.getByTestId('public-top-bar')
      await expect(bar.getByRole('link', { name: 'peasant home' })).toHaveAttribute('href', /^https:\/\/peasantlabs\.org\/?$/)
      // Give Next the time it takes to prefetch the links in view, then follow
      // the page's own link home the way a reader does, inside the app.
      await page.waitForLoadState('networkidle')
      await page.waitForTimeout(500)
      await page.getByRole('navigation', { name: 'breadcrumb' }).getByRole('link', { name: 'peasant' }).click()
      // The root page sends a signed-out reader on to the marketing site itself.
      await expect(page).toHaveURL(/^https:\/\/peasantlabs\.org\/?$/)
      expect(problems).toEqual([])
    } finally {
      await setScenario(request, 'default')
    }
  })

  test("the app's own fetch of the root is served, not sent to another origin", async ({ request }) => {
    // What Next sends to prefetch or navigate to `/` inside the app. A redirect
    // here would point a same-origin fetch at the marketing site, which the
    // browser refuses as a cross-origin fetch.
    const response = await request.get('/', { headers: { RSC: '1', 'Sec-Fetch-Dest': 'empty', 'Sec-Fetch-Mode': 'cors' }, maxRedirects: 0 })
    expect(response.status()).toBe(200)
    expect(response.headers()['location']).toBeUndefined()
  })

  for (const scheme of ['light', 'dark']) {
    test(`with no stored choice the theme follows a ${scheme} system from the first paint`, async ({ browser, request }, testInfo) => {
      await setScenario(request, 'signed-out')
      // A fresh browser with nothing stored (the shared fixture pins a theme),
      // and every change to the theme attribute recorded from the first byte,
      // so a dark frame before the real theme would show.
      const context = await browser.newContext({ colorScheme: scheme, baseURL: testInfo.project.use.baseURL })
      const page = await context.newPage()
      try {
        await page.addInitScript(() => {
          window.__themeChanges = []
          // The document has no element yet when this runs, so watch it whole and
          // keep the root element's changes.
          new MutationObserver((records) => {
            for (const r of records) {
              if (r.target === document.documentElement) window.__themeChanges.push(document.documentElement.getAttribute('data-theme'))
            }
          }).observe(document, { attributes: true, subtree: true, attributeFilter: ['data-theme'] })
        })
        await page.goto('/login')
        await expect(page.getByRole('heading', { level: 1, name: HEADLINE })).toBeVisible()
        await expect(page.locator('html')).toHaveAttribute('data-theme', scheme)
        const changes = await page.evaluate(() => window.__themeChanges)
        // The server writes dark; a light system changes it once, before
        // hydration, and nothing writes it back. A dark system never changes it.
        expect(changes.filter((t) => t !== scheme)).toEqual([])
      } finally {
        await context.close()
        await setScenario(request, 'default')
      }
    })
  }

  test('the sign-in page is one github button', async ({ page, request, theme }, testInfo) => {
    await setScenario(request, 'signed-out')
    try {
      const oauthStarts = await recordOAuthStarts(page)
      await page.goto('/login')
      await expectTheme(page, theme)

      const signIn = page.getByTestId('sign-in-page')
      await expect(signIn.getByRole('heading', { level: 1, name: HEADLINE })).toBeVisible()
      await expect(signIn.getByText('peasant', { exact: true })).toBeVisible()
      await expectComputedTokens(page, '.on-login-card', { borderRadius: '0px' })

      // One front door: one GitHub button, nothing behind a chevron, and no
      // chrome around the page to offer a second.
      const button = signIn.getByRole('button', { name: /continue with github/i })
      await expect(button).toBeVisible()
      await expect(page.getByRole('button', { name: 'more sign-in providers' })).toHaveCount(0)
      await expect(page.getByTestId('public-top-bar')).toHaveCount(0)
      await expect(page.getByRole('navigation', { name: 'peasant sections' })).toHaveCount(0)

      // The terminal sign-in and the privacy notice sit beside it.
      await expect(signIn.getByText('peasant village login')).toBeVisible()
      await expect(signIn.getByRole('link', { name: 'privacy notice' })).toHaveAttribute('href', '/privacy')

      await expectComputedTokens(page, '[data-testid="sign-in-page"] p', {
        fontFamilyIncludes: 'atkinson',
        minFontSize: 14,
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

  test('signing in from a page comes back to it', async ({ page, request, theme }) => {
    await setScenario(request, 'signed-out')
    try {
      const oauthStarts = await recordOAuthStarts(page)
      // A signed-out reader of a page is offered a way in that returns to it.
      await page.goto('/privacy')
      await expectTheme(page, theme)
      const signInLink = page.getByTestId('public-top-bar').getByRole('link', { name: 'sign in' })
      await expect(signInLink).toHaveAttribute('href', '/login?next=%2Fprivacy')
      await signInLink.click()
      await expect(page).toHaveURL(/\/login\?next=%2Fprivacy$/)
      await page.getByRole('button', { name: /continue with github/i }).click()
      await expect.poll(() => oauthStarts).toEqual(['/api/v1/auth/github'])
    } finally {
      await setScenario(request, 'default')
    }
    // GitHub answers, and the API sends the browser to the callback with a token.
    await page.goto('/auth/callback?token=demo-token')
    await expect(page).toHaveURL(/\/privacy$/)
  })

  test('the sign-in page fits a phone', async ({ page, request, theme }) => {
    await setScenario(request, 'signed-out')
    try {
      await page.setViewportSize({ width: 390, height: 844 })
      await page.goto('/login')
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

  test('a signed-in sidebar is home, discover, pull requests and collectives', async ({ page, theme }) => {
    await page.goto('/')
    await expectTheme(page, theme)

    const nav = page.getByRole('navigation', { name: 'peasant sections' })
    const links = nav.getByRole('link')
    await expect(links.nth(0)).toHaveText('home')
    await expect(links.nth(1)).toHaveText('discover')
    await expect(links.nth(2)).toHaveText('pull requests')
    await expect(links.nth(3)).toHaveText('collectives')
    await expect(nav.getByRole('link', { name: 'home' })).toHaveAttribute('aria-current', 'page')
    await expect(nav.getByRole('link', { name: 'collectives' })).not.toHaveAttribute('aria-current', 'page')
    // The person's collectives follow, each leading to its page.
    await expect(nav.getByRole('link', { name: 'Acme Platform' })).toHaveAttribute('href', /^\/collectives\//)
    // Explore and publish keep their routes but stay out of the sidebar.
    await expect(nav.getByRole('link', { name: 'explore' })).toHaveCount(0)
    await expect(nav.getByRole('link', { name: 'publish' })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'account menu for @alice-dev' })).toBeVisible()
    // Signed in, the page never offers sign-in.
    await expect(page.getByRole('button', { name: /continue with/i })).toHaveCount(0)
    await expect(page.getByRole('link', { name: 'sign in' })).toHaveCount(0)
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
    // The menu opens upward from the foot of the sidebar and stays on screen.
    const box = await menu.boundingBox()
    const viewport = page.viewportSize()
    expect(box.y).toBeGreaterThanOrEqual(0)
    expect(box.y + box.height).toBeLessThanOrEqual(viewport.height)

    // Escape closes it and gives focus back to the trigger.
    await page.keyboard.press('Escape')
    await expect(trigger).toHaveAttribute('aria-expanded', 'false')
    await expect(trigger).toBeFocused()

    // Reopen from the keyboard for the evidence frame.
    await page.keyboard.press('ArrowDown')
    await expect(menu.getByRole('menuitem').first()).toBeFocused()
  })
  test('sign-out announces pending and failed requests with retry', async ({ page, theme }, testInfo) => {
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
