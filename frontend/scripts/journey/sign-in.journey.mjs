/* Journey: the front door.
 *
 * A visitor with no session is sent to the marketing site's log-in popup, which
 * holds the sign-in itself; `/login` hands the visitor on, keeping the page they
 * asked for so `/auth/start` and `/auth/callback` can bring them back to it
 * after the provider answers. A signed-in visitor gets the sidebar and an
 * account menu holding profile, settings and sign out. Each test ends on the
 * state its evidence screenshot shows, in both themes. Assertions are on roles
 * and names, and the public privacy notice's ARIA tree and axe report are kept
 * as artifacts.
 *
 * Coverage boundary: the GitHub button, the terminal sign-in and their card now
 * live on the marketing site. This harness asserts the app's side of that
 * hand-off -- the exact destination (scheme, host, `login`, `next`), the return
 * address, the provider start and the callback landing -- and the public
 * privacy notice the app still serves. It does not assert the external popup's
 * UI, which this repository does not own.
 */
import { writeFileSync } from 'node:fs'
import { test, expect } from './lib/fixtures.mjs'
import { setScenario } from './lib/scenario.mjs'
import { scanAxe, seriousViolations, expectTheme } from './lib/assertions.mjs'

const MARKETING = 'https://peasantlabs.org/'
const MARKETING_LOGIN = `${MARKETING}?login`

/**
 * The log-in hand-off read as the browser's own navigation: scheme and host are
 * asserted, never assumed, and `next` is the decoded page to come back to.
 */
function marketingDestination(raw) {
  const url = new URL(raw)
  return {
    protocol: url.protocol,
    host: url.host,
    pathname: url.pathname,
    login: url.searchParams.has('login'),
    next: url.searchParams.get('next'),
  }
}

/**
 * Waits for the browser to finish the hand-off (the frame leaves in an effect,
 * after the session answers), then reads the destination it landed on.
 */
async function expectMarketingLogin(page, next) {
  const expected = next === null ? MARKETING_LOGIN : `${MARKETING_LOGIN}&next=${encodeURIComponent(next)}`
  await expect(page).toHaveURL(expected)
  expect(marketingDestination(page.url())).toEqual({
    protocol: 'https:',
    host: 'peasantlabs.org',
    pathname: '/',
    login: true,
    next,
  })
}

/** Answers the marketing site so a hand-off can finish inside the test. */
async function stubMarketing(page) {
  await page.route(`${MARKETING}**`, (route) =>
    route.fulfill({ status: 200, contentType: 'text/html', body: '<title>marketing</title>' }),
  )
}

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
  test('a visitor with no session at the root is sent to the marketing log-in popup', async ({ request }) => {
    // The server decides before any page is served: no cookie, no app.
    const response = await request.get('/', { headers: { 'Sec-Fetch-Dest': 'document' }, maxRedirects: 0 })
    expect(response.status()).toBe(307)
    expect(marketingDestination(response.headers()['location'])).toEqual({
      protocol: 'https:',
      host: 'peasantlabs.org',
      pathname: '/',
      login: true,
      next: null,
    })
  })

  test('a stale session at the root also leaves for the marketing log-in popup', async ({ page, request }) => {
    await setScenario(request, 'signed-out')
    try {
      // The browser holds a cookie the API no longer accepts: the page asks
      // /auth/me, hears no session, and leaves.
      await stubMarketing(page)
      await page.goto('/')
      await expectMarketingLogin(page, null)
      // The app's own home never mounts for a session the server refused.
      await expect(page.getByTestId('home-page')).toHaveCount(0)
    } finally {
      await setScenario(request, 'default')
    }
  })

  test('a signed-out deep link comes back to its page, query and all', async ({ page, request }) => {
    await setScenario(request, 'signed-out')
    try {
      await stubMarketing(page)
      await page.goto('/collectives/abc?tab=members')
      await expectMarketingLogin(page, '/collectives/abc?tab=members')
      await expect(page.getByTestId('collective-page')).toHaveCount(0)
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
      await stubMarketing(page)
      await page.goto('/privacy')
      await expectTheme(page, theme)
      const bar = page.getByTestId('public-top-bar')
      // The product's home is the marketing site: https, its own host, the root
      // path, and nothing extra carried in the address.
      const homeHref = await bar.getByRole('link', { name: 'peasant home' }).getAttribute('href')
      const homeUrl = new URL(homeHref)
      expect(homeUrl.protocol).toBe('https:')
      expect(homeUrl.host).toBe('peasantlabs.org')
      expect(homeUrl.pathname).toBe('/')
      expect(homeUrl.search).toBe('')
      expect(homeUrl.hash).toBe('')
      // Give Next the time it takes to prefetch the links in view, then follow
      // the page's own link home the way a reader does, inside the app.
      await page.waitForLoadState('networkidle')
      await page.waitForTimeout(500)
      await page.getByRole('navigation', { name: 'breadcrumb' }).getByRole('link', { name: 'peasant' }).click()
      // The root page sends a signed-out reader on to the marketing log-in popup.
      await expectMarketingLogin(page, null)
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
        // The privacy notice is the signed-out page the app still serves; the
        // theme must be right on its first paint like every other page.
        await page.goto('/privacy')
        await expect(page.getByTestId('privacy-notice')).toBeVisible()
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

  for (const scheme of ['light', 'dark']) {
    test(`the public privacy notice hydrates without an error on a ${scheme} system`, async ({ browser, request }, testInfo) => {
      await setScenario(request, 'signed-out')
      // Every load in a fresh browser with nothing stored and nothing cached,
      // so the theme comes from the system and the scripts arrive cold. A
      // mismatch between the served markup and the first client render shows
      // up as a page error; it was intermittent, so the page is loaded several
      // times at both widths. The privacy notice is the only signed-out page
      // that serves content, so it carries this check.
      const errors = []
      try {
        for (let round = 0; round < 3; round += 1) {
          for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
            const context = await browser.newContext({ colorScheme: scheme, viewport, baseURL: testInfo.project.use.baseURL })
            const page = await context.newPage()
            page.on('pageerror', (error) => errors.push(`/privacy at ${viewport.width}: ${error.message.split('\n')[0]}`))
            await page.goto('/privacy')
            await page.waitForLoadState('networkidle')
            await expect(page.locator('html')).toHaveAttribute('data-theme', scheme)
            await expect(page.getByTestId('privacy-notice')).toBeVisible()
            await context.close()
          }
        }
        expect(errors).toEqual([])
      } finally {
        await setScenario(request, 'default')
      }
    })
  }

  test('the sign-in page hands off to the marketing log-in popup, keeping where to return', async ({ page, request }) => {
    await setScenario(request, 'signed-out')
    try {
      await stubMarketing(page)
      // /login is the address every sign-in link leads to; the sign-in itself,
      // with its GitHub button and terminal command, is the marketing popup's.
      await page.goto('/login?next=%2Fprivacy')
      await expectMarketingLogin(page, '/privacy')
      // Signing in with nothing to come back to still opens the popup.
      await page.goto('/login')
      await expectMarketingLogin(page, null)
    } finally {
      await setScenario(request, 'default')
    }
  })

  test('the sign-in page drops a foreign return address', async ({ page, request }) => {
    await setScenario(request, 'signed-out')
    try {
      await stubMarketing(page)
      // An address on another host would turn the sign-in into an open
      // redirect, so the hand-off carries no `next` at all.
      await page.goto('/login?next=%2F%2Fexample.com%2Fsteal')
      await expectMarketingLogin(page, null)
    } finally {
      await setScenario(request, 'default')
    }
  })

  test('signing in from a page comes back to it', async ({ page, request }) => {
    await setScenario(request, 'signed-out')
    try {
      const oauthStarts = await recordOAuthStarts(page)
      // The marketing popup's GitHub button lands on /auth/start with the page
      // to return to; that step remembers it and leaves for the provider.
      await page.goto('/auth/start?provider=github&next=%2Fprivacy')
      await expect.poll(() => oauthStarts).toEqual(['/api/v1/auth/github'])
    } finally {
      await setScenario(request, 'default')
    }
    // GitHub answers, and the API sends the browser to the callback with a token.
    await page.goto('/auth/callback?token=demo-token')
    await expect(page).toHaveURL(/\/privacy$/)
    await expect(page.getByTestId('privacy-notice')).toBeVisible()
  })

  test('a provider hand-off never follows a foreign return address', async ({ page, request }) => {
    const offOrigin = []
    page.on('request', (request) => {
      const host = new URL(request.url()).host
      if (host === 'example.com' || host.endsWith('.example.com')) offOrigin.push(request.url())
    })
    await setScenario(request, 'signed-out')
    try {
      const oauthStarts = await recordOAuthStarts(page)
      await page.goto('/auth/start?provider=github&next=%2F%2Fexample.com%2Fsteal')
      await expect.poll(() => oauthStarts).toEqual(['/api/v1/auth/github'])
    } finally {
      await setScenario(request, 'default')
    }
    // With no address to come back to, the callback lands on the signed-in home.
    await page.goto('/auth/callback?token=demo-token')
    await expect(page.getByTestId('home-page')).toBeVisible()
    expect(new URL(page.url()).pathname).toBe('/')
    expect(offOrigin).toEqual([])
  })

  test('the public privacy notice fits a phone', async ({ page, request, theme }) => {
    await setScenario(request, 'signed-out')
    try {
      await page.setViewportSize({ width: 390, height: 844 })
      // The privacy notice is the signed-out page the app still serves.
      await page.goto('/privacy')
      await expectTheme(page, theme)
      await expect(page.getByTestId('privacy-notice')).toBeVisible()
      // No horizontal scroll at phone width.
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      )
      expect(overflow).toBeLessThanOrEqual(0)
    } finally {
      await setScenario(request, 'default')
    }
  })

  test('the public privacy notice is accessible signed out', async ({ page, request, theme }, testInfo) => {
    await setScenario(request, 'signed-out')
    try {
      await page.goto('/privacy')
      await expectTheme(page, theme)
      // The notice is the signed-out page the app owns, so its accessibility is
      // this repository's to assert; the marketing popup's card is not.
      const notice = page.getByTestId('privacy-notice')
      await expect(notice).toBeVisible()
      const aria = await notice.ariaSnapshot()
      writeFileSync(testInfo.outputPath('privacy-notice-aria.yml'), aria)
      await testInfo.attach('privacy-notice-aria.yml', {
        path: testInfo.outputPath('privacy-notice-aria.yml'),
        contentType: 'text/yaml',
      })
      const axe = await scanAxe(page)
      await attachJSON(testInfo, 'privacy-notice-axe.json', axe)
      const blocking = seriousViolations(axe)
      expect(blocking, JSON.stringify(blocking, null, 2)).toEqual([])
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
    // fairtrade's account row, named by the handle, is the menu's trigger.
    const account = page.getByRole('button', { name: '@alice-dev' })
    await expect(account).toBeVisible()
    await expect(account).toHaveAttribute('aria-haspopup', 'menu')
    // The chrome's settings gear is its own link, current only on /settings.
    await expect(page.getByRole('link', { name: 'settings' })).toHaveAttribute('href', '/settings')
    // Signed in, the page never offers sign-in.
    await expect(page.getByRole('button', { name: /continue with/i })).toHaveCount(0)
    await expect(page.getByRole('link', { name: 'sign in' })).toHaveCount(0)
  })

  test('the account menu holds profile, settings and sign out', async ({ page, theme }) => {
    await page.goto('/')
    await expectTheme(page, theme)

    const trigger = page.getByRole('button', { name: '@alice-dev' })
    await expect(trigger).toHaveAttribute('aria-haspopup', 'menu')
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
    await page.getByRole('button', { name: '@alice-dev' }).click()
    await page.getByRole('menuitem', { name: 'sign out' }).click()
    const dialog = page.getByRole('dialog')
    await expect(page.getByRole('dialog', { name: 'signing out' })).toBeVisible()
    await expect(dialog.getByRole('status')).toHaveText('waiting for the sign-out request.')
    await expect.poll(() => posts).toBe(1)
    await expect(dialog.getByRole('button', { name: 'close dialog' })).toBeDisabled()
    await page.evaluate(() => window.scrollTo(0, 0))
    const pending = testInfo.outputPath(`header-sign-out-pending-${theme}.png`)
    await page.screenshot({ path: pending, fullPage: false })
    await testInfo.attach('header-sign-out-pending', { path: pending, contentType: 'image/png' })
    await pendingRoute.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'sign-out service unavailable' }) })
    await expect(page.getByRole('dialog', { name: 'could not sign out' })).toBeVisible()
    await expect(dialog.getByRole('alert')).toContainText('sign-out service unavailable')
    await expect(dialog.getByRole('button', { name: 'try again' })).toBeEnabled()
    const failed = testInfo.outputPath(`header-sign-out-failed-${theme}.png`)
    await page.screenshot({ path: failed, fullPage: false })
    await testInfo.attach('header-sign-out-failed', { path: failed, contentType: 'image/png' })
    await expect(page.getByRole('button', { name: '@alice-dev', includeHidden: true })).toBeVisible()
    const axe = await scanAxe(page)
    await attachJSON(testInfo, 'header-sign-out-axe.json', axe)
    expect(seriousViolations(axe)).toEqual([])
    await dialog.getByRole('button', { name: 'try again' }).click()
    await expect.poll(() => posts).toBe(2)
    await pendingRoute.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'sign-out service unavailable' }) })
    await expect(dialog.getByRole('alert')).toContainText('sign-out service unavailable')
  })

})
