/* Screenshot the signed-in home surface on the REAL root route (`/`).

   Build provenance is asserted BEFORE any PNG is written: the served page must
   carry the home surface, the two section headings in order, and project rows
   whose links are keyed on the 64-character project hash. All of that exists
   only in this change, so a stale server, or one serving another worktree,
   fails with a nonzero exit instead of producing a misleading capture.

   Computed styles are read from the live DOM as well, because a scaled PNG
   cannot tell two close token values apart: the run reports the session-count
   text's computed font-family, font-variant-numeric (tabular figures are
   load-bearing beside a name of any length) and border-radius (the design
   system is square everywhere).

   The `failure` mode captures the OTHER answer the page owes: the owner-scoped
   list request failed, so the page must show the failure surface and a retry,
   never the teaching empty state. Its provenance check is that surface and its
   retry control, plus the absence of the empty state, so a build that still
   renders "nothing published yet" on a failed request cannot produce a PNG.

   env:
     HOME_SHOOT_MODE surface arm: `home` (default), `failure`, or `no-handle`
     VILLAGE_URL     app URL (default http://localhost:3000/)
     CHROME_PATH     Chrome/Chromium binary (required)
     PUPPETEER_CORE  explicit module path to puppeteer-core (optional)
   usage: VILLAGE_URL=... CHROME_PATH=... node home-shoot.mjs <theme> <outdir>
*/
import { mkdirSync, statSync } from 'node:fs'
import { SurfaceGate } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const CHROME = process.env.CHROME_PATH
const URL = process.env.VILLAGE_URL || 'http://localhost:3000/'
const theme = process.argv[2] || 'dark'
const MODE = process.env.HOME_SHOOT_MODE || 'home'
const MODES = ['home', 'failure', 'no-handle']
if (!MODES.includes(MODE)) {
  console.error(`ERROR [home-shoot.mjs] HOME_SHOOT_MODE=${MODE} is not one of ${MODES.join(', ')}.`)
  process.exit(1)
}
const out = process.argv[3] || `/tmp/village-home-${theme}`
mkdirSync(out, { recursive: true })

const BASE_VP = { width: 1396, height: 939, deviceScaleFactor: 1 }

if (!CHROME) {
  console.error('ERROR [home-shoot.mjs] CHROME_PATH is unset — set it to your Chrome/Chromium binary.')
  process.exit(1)
}

const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', defaultViewport: { ...BASE_VP } })
const page = await browser.newPage()
// The root route serves home only to a signed-in visitor; without the cookie
// the capture would be the discovery list, which is a different surface.
await page.setCookie({ name: 'peasant_token', value: 'demo-token', domain: 'localhost', path: '/' })
await applyDeterminism(page)
const errs = []
page.on('console', (m) => { if (m.type() === 'error' && !/favicon|404|hydrat/.test(m.text())) errs.push(m.text()) })
page.on('pageerror', (e) => errs.push('pageerr: ' + e.message))

await page.goto(URL, { waitUntil: 'networkidle0' })
await page.evaluate((nextTheme) => localStorage.setItem('peasant-theme', nextTheme), theme)
await page.reload({ waitUntil: 'networkidle0' })
await new Promise((r) => setTimeout(r, 900))

const fail = async (message, code) => {
  await browser.close()
  console.error(message)
  process.exit(code)
}

const actualTheme = await page.evaluate(() => document.documentElement.getAttribute('data-theme'))
if (actualTheme !== theme) {
  await fail(
    `ERROR [home-shoot.mjs] the requested theme did not apply.
  What failed: [data-theme]="${actualTheme}" after requesting theme="${theme}".
  Why: the theme toggle/localStorage handshake did not settle.
  Where: home-shoot.mjs theme preflight.
  Means: the capture would be the wrong theme.
  Fix: confirm the root layout is using the shared theme hook and retry.`,
    3,
  )
}

const pause = (ms) => new Promise((r) => setTimeout(r, ms))
const gate = new SurfaceGate(page)

/* Assert what a selector's COMPUTED style actually is, and fail closed when it
   is not. A scaled PNG cannot tell two close token values apart, and a whole
   surface can ship unstyled while every mount-and-served check stays green, so
   each arm names the properties its surface is judged on and this refuses the
   capture when one of them is wrong.

   `expect` maps a CSS property to a predicate on the computed value; `label`
   names the element in the error so the fix is obvious. */
const assertComputed = async (sel, expect, label) => {
  const got = await page.evaluate(
    (selector, props) => {
      const el = document.querySelector(selector)
      if (!el) return null
      const style = getComputedStyle(el)
      return Object.fromEntries(props.map((p) => [p, style[p]]))
    },
    sel,
    Object.keys(expect),
  )
  if (!got) {
    await fail(
      `ERROR [home-shoot.mjs] ${label} is not present, so its computed style cannot be checked.
  What failed: no element matched "${sel}".
  Why: the served build does not render the element the capture is judged on.
  Where: home-shoot.mjs computed-style probe.
  Means: the PNG would be of a different surface than the one named.
  Fix: rebuild and restart the server from this worktree, then retry.`,
      2,
    )
  }
  const wrong = Object.entries(expect).filter(([prop, ok]) => !ok(got[prop]))
  if (wrong.length > 0) {
    await fail(
      `ERROR [home-shoot.mjs] ${label} does not carry the design system's computed styles.
  What failed: ${wrong.map(([prop]) => `${prop}=${JSON.stringify(got[prop])}`).join(', ')}.
  Why: the stylesheet did not reach the element, or a token was replaced.
  Where: home-shoot.mjs computed-style probe on "${sel}".
  Means: the PNG would look plausible while the surface ships unstyled or off-token.
  Fix: confirm the served build includes the app stylesheet, then retry.`,
      2,
    )
  }
  return got
}

/* The design system is square everywhere, and its chrome is Atkinson mono. */
const isSquare = (value) => value === '0px'
const isMono = (value) => /atkinson/i.test(value ?? '')

const waitFor = async (sel, timeoutMs = 12000) => {
  const start = Date.now()
  while (Date.now() - start < timeoutMs) {
    const el = await page.$(sel)
    if (el) return el
    await pause(100)
  }
  return null
}

if (MODE === 'no-handle') {
  const panel = await waitFor('[data-testid="home-page-no-handle"]')
  if (!panel) {
    await fail(
      `ERROR [home-shoot.mjs] the blank-handle surface never mounted at ${URL}.
  What failed: no [data-testid="home-page-no-handle"] element appeared after the app loaded.
  Why: the served build predates this branch, or the mock served a handle instead of a blank one.
  Where: home-shoot.mjs no-handle-arm readiness wait.
  Means: the capture would be the ordinary home page or an endless skeleton.
  Fix: restart the mock with MOCK_BLANK_HANDLE=1, rebuild from this worktree, and retry.`,
      2,
    )
  }
  const shape = await page.evaluate(() => {
    const panel = document.querySelector('[data-testid="home-page-no-handle"]')
    const alert = panel?.querySelector('[role="alert"]')
    return {
      alertText: (alert?.textContent ?? '').replace(/\s+/g, ' ').trim(),
      shimmer: document.querySelectorAll('.animate-shimmer').length,
      emptyState: document.querySelector('[data-testid="home-empty-state"]') != null,
      rows: document.querySelectorAll('[data-testid="home-project-row"]').length,
    }
  })
  // The point of the surface: it TERMINATES. A shimmer still on screen would
  // mean the page is waiting for something that is never coming.
  if (
    shape.shimmer !== 0 ||
    shape.rows !== 0 ||
    shape.emptyState ||
    !shape.alertText.includes('your account has no handle')
  ) {
    await fail(
      `ERROR [home-shoot.mjs] the blank-handle surface is not the terminal answer.
  What failed: ${JSON.stringify(shape)}.
  Why: the page is still shimmering, listed rows, or carries no alert naming the missing handle.
  Where: home-shoot.mjs no-handle-arm build-provenance check.
  Means: the capture would not evidence the surface under review.
  Fix: rebuild and restart the server from this worktree, then retry.`,
      2,
    )
  }
  const noHandleStyle = await assertComputed(
    '[data-testid="home-page-no-handle"] [role="alert"]',
    { borderTopWidth: (v) => v !== '0px', borderRadius: isSquare },
    'the blank-handle alert',
  )
  await page.evaluate(() => window.scrollTo(0, 0))
  await pause(150)
  const body = await page.$('body')
  const bodyBox = await body.boundingBox()
  if (!bodyBox || bodyBox.width < 4 || bodyBox.height < 4) {
    await fail(`ERROR [home-shoot.mjs] body resolved to a blank box at ${URL}.`, 1)
  }
  // Gated on the panel for the same reason the failure arm is: this surface is
  // one alert on an otherwise empty page, by design.
  const panelFile = `${out}/village-home-no-handle-panel.png`
  await panel.screenshot({ path: panelFile })
  const panelGate = await gate.assert('village-home-no-handle-panel', panelFile, {
    sel: '[data-testid="home-page-no-handle"]',
    where: 'home-shoot.mjs',
  })
  const pageFile = `${out}/village-home-no-handle.png`
  await body.screenshot({ path: pageFile, captureBeyondViewport: true })
  const pageMeasure = await gate.measure(pageFile)
  console.log('shot', 'village-home-no-handle-panel'.padEnd(30), `nonbg=${(panelGate.nonbgRatio * 100).toFixed(1)}% colors=${panelGate.distinctColors} ${(statSync(panelFile).size / 1024).toFixed(1)}KB`)
  console.log('shot', 'village-home-no-handle'.padEnd(30), `${Math.round(bodyBox.width)}x${Math.round(bodyBox.height)}`.padEnd(11), `nonbg=${(pageMeasure.nonbgRatio * 100).toFixed(2)}% colors=${pageMeasure.distinctColors} ${(statSync(pageFile).size / 1024).toFixed(1)}KB (page measured, not gated)`)
  console.log('blank-handle panel:', JSON.stringify(shape))
  console.log('computed alert style:', JSON.stringify(noHandleStyle))
  console.log('console errors:', errs.length ? errs.slice(0, 6) : 'none')
  await browser.close()
  process.exit(0)
}

if (MODE === 'failure') {
  const panel = await waitFor('[data-testid="home-page-error"]')
  if (!panel) {
    await fail(
      `ERROR [home-shoot.mjs] the home failure surface never mounted at ${URL}.
  What failed: no [data-testid="home-page-error"] element appeared after the app loaded.
  Why: the served build predates the failure branch, or the mock answered the owner-scoped list instead of failing it.
  Where: home-shoot.mjs failure-arm readiness wait.
  Means: the capture would not show the surface under review.
  Fix: restart the mock with MOCK_OWNER_LIST_FAILS=1, rebuild from this worktree, and retry.`,
      2,
    )
  }
  const shape = await page.evaluate(() => {
    const panel = document.querySelector('[data-testid="home-page-error"]')
    const alert = panel?.querySelector('[role="alert"]')
    const retry = [...(panel?.querySelectorAll('button') ?? [])].map((b) => (b.textContent ?? '').trim())
    return {
      alertText: (alert?.textContent ?? '').replace(/\s+/g, ' ').trim(),
      retry,
      emptyState: document.querySelector('[data-testid="home-empty-state"]') != null,
      rows: document.querySelectorAll('[data-testid="home-project-row"]').length,
      recentList: document.querySelector('[data-testid="home-recent-sessions"]') != null,
    }
  })
  // The whole point of the surface: a failed request is NOT an empty library,
  // and the person is offered a way to ask again. The row and list checks are
  // the other half of it: this arm serves NO rows, so anything still listed
  // would mean the capture is of a different state than the one named.
  if (
    shape.emptyState ||
    shape.rows !== 0 ||
    shape.recentList ||
    !shape.alertText.includes('Failed to load your sessions') ||
    !shape.alertText.includes('not an empty library') ||
    shape.retry.length === 0
  ) {
    await fail(
      `ERROR [home-shoot.mjs] the served build does not distinguish a failed request from an empty library.
  What failed: ${JSON.stringify(shape)}.
  Why: the page rendered the teaching empty state or a session list, or the failure panel carries no heading, no reassurance, or no retry control.
  Where: home-shoot.mjs failure-arm build-provenance check.
  Means: the capture would evidence the very bug this change removes.
  Fix: rebuild and restart the server from this worktree, then retry.`,
      2,
    )
  }
  const st = {
    panel: await assertComputed(
      '[data-testid="home-page-error"] [role="alert"]',
      { borderTopWidth: (v) => v !== '0px', borderRadius: isSquare },
      'the failure panel',
    ),
    retry: await assertComputed(
      '[data-testid="home-page-error"] button',
      { fontFamily: isMono, borderRadius: isSquare },
      "the failure panel's retry control",
    ),
  }
  await page.evaluate(() => window.scrollTo(0, 0))
  await pause(150)
  const body = await page.$('body')
  const bodyBox = await body.boundingBox()
  if (!bodyBox || bodyBox.width < 4 || bodyBox.height < 4) {
    await fail(`ERROR [home-shoot.mjs] body resolved to a blank box at ${URL}.`, 1)
  }
  // The non-empty gate runs on the PANEL, not on the page. This surface is
  // deliberately sparse: the whole point is that the page shows one calm panel
  // instead of a library, so most of the page is intentionally background and a
  // whole-page non-background floor would be measuring the emptiness the design
  // intends. The panel is what must have painted, so that is what is asserted;
  // the full page is captured beside it for review, with its own measurement
  // reported.
  const panelEl = await page.$('[data-testid="home-page-error"]')
  const panelFile = `${out}/village-home-failure-panel.png`
  await panelEl.screenshot({ path: panelFile })
  const panelGate = await gate.assert('village-home-failure-panel', panelFile, {
    sel: '[data-testid="home-page-error"]',
    where: 'home-shoot.mjs',
  })
  const failFile = `${out}/village-home-failure.png`
  await body.screenshot({ path: failFile, captureBeyondViewport: true })
  const pageMeasure = await gate.measure(failFile)
  console.log('shot', 'village-home-failure-panel'.padEnd(28), `nonbg=${(panelGate.nonbgRatio * 100).toFixed(1)}% colors=${panelGate.distinctColors} ${(statSync(panelFile).size / 1024).toFixed(1)}KB`)
  console.log('shot', 'village-home-failure'.padEnd(28), `${Math.round(bodyBox.width)}x${Math.round(bodyBox.height)}`.padEnd(11), `nonbg=${(pageMeasure.nonbgRatio * 100).toFixed(2)}% colors=${pageMeasure.distinctColors} ${(statSync(failFile).size / 1024).toFixed(1)}KB (page measured, not gated: see note above)`)
  console.log('failure panel:', JSON.stringify(shape))
  console.log('computed panel style:', JSON.stringify(st))
  console.log('console errors:', errs.length ? errs.slice(0, 6) : 'none')
  await browser.close()
  process.exit(0)
}

const ready = await waitFor('[data-testid="home-page"]')
if (!ready) {
  await fail(
    `ERROR [home-shoot.mjs] the home surface never mounted at ${URL}.
  What failed: no [data-testid="home-page"] element appeared after the app loaded.
  Why: the served build predates this change, the visitor is not signed in, or NEXT_PUBLIC_API_URL points at a backend that does not answer /auth/me.
  Where: home-shoot.mjs surface readiness wait.
  Means: the capture would be the discovery list or a loading skeleton.
  Fix: start the home mock REST server, point NEXT_PUBLIC_API_URL at it, set the auth cookie, and retry.`,
    2,
  )
}

// Build provenance: the two sections, in order, and hash-keyed project links.
const provenance = await page.evaluate(() => {
  const home = document.querySelector('[data-testid="home-page"]')
  const order = [...home.querySelectorAll('[data-testid]')]
    .map((e) => e.getAttribute('data-testid'))
    .filter((id) => id === 'home-recent-sessions' || id === 'home-projects')
  const rows = [...document.querySelectorAll('[data-testid="home-project-row"]')]
  return {
    order,
    hrefs: rows.map((r) => r.getAttribute('href')),
  }
})

if (provenance.order.join(',') !== 'home-recent-sessions,home-projects') {
  await fail(
    `ERROR [home-shoot.mjs] the home sections are missing or out of order.
  What failed: found [${provenance.order.join(', ')}].
  Why: the served build does not render recent sessions above projects.
  Where: home-shoot.mjs build-provenance check.
  Means: the capture would not show the surface under review.
  Fix: rebuild and restart the server from this worktree, then retry.`,
    2,
  )
}
const hashKeyed = provenance.hrefs.length > 0 && provenance.hrefs.every((h) => /\/projects\/[0-9a-f]{64}$/.test(h ?? ''))
if (!hashKeyed) {
  await fail(
    `ERROR [home-shoot.mjs] the project rows are absent or not keyed on a project hash.
  What failed: hrefs ${JSON.stringify(provenance.hrefs)}.
  Why: the fixture served no projects, or the rows regressed to a name-keyed link.
  Where: home-shoot.mjs build-provenance check.
  Means: the capture would not evidence the project list the change is about.
  Fix: confirm the mock serves rows carrying project_hash, then retry.`,
    2,
  )
}

// The session count beside a project name is the surface's own token claim:
// mono, tabular figures (so counts line up under names of any length), square.
// Read from the live DOM and ASSERTED, not merely reported: a count that had
// lost its font would be invisible in a scaled PNG.
const countStyle = await assertComputed(
  '[data-testid="home-project-row"] span.font-mono',
  {
    fontFamily: isMono,
    fontVariantNumeric: (v) => v === 'tabular-nums',
    borderRadius: isSquare,
  },
  "the project row's session count",
)

await page.evaluate(() => window.scrollTo(0, 0))
await pause(150)

const el = await page.$('body')
const box = await el.boundingBox()
if (!box || box.width < 4 || box.height < 4) {
  await fail(
    `ERROR [home-shoot.mjs] body resolved to a blank or zero-size box at ${URL}.
  What failed: ${JSON.stringify(box)}.
  Why: the app shell or home surface did not lay out.
  Where: home-shoot.mjs, full-page body capture.
  Means: the PNG would be empty or near-empty.
  Fix: confirm the route is reachable and that the home fixtures loaded.`,
    1,
  )
}

const file = `${out}/village-home.png`
await el.screenshot({ path: file, captureBeyondViewport: true })
const r = await gate.assert('village-home', file, { sel: 'body', where: 'home-shoot.mjs' })
const bytes = statSync(file).size

console.log('shot', 'village-home'.padEnd(22), `${Math.round(box.width)}x${Math.round(box.height)}`.padEnd(11), `nonbg=${(r.nonbgRatio * 100).toFixed(1)}% colors=${r.distinctColors} ${(bytes / 1024).toFixed(1)}KB`)
console.log('computed session-count style:', JSON.stringify(countStyle))
console.log('console errors:', errs.length ? errs.slice(0, 6) : 'none')
await browser.close()
