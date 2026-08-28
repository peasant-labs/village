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

   env:
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

const waitFor = async (sel, timeoutMs = 12000) => {
  const start = Date.now()
  while (Date.now() - start < timeoutMs) {
    const el = await page.$(sel)
    if (el) return el
    await pause(100)
  }
  return null
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
  const count = rows[0]?.querySelector('span.font-mono')
  const style = count ? getComputedStyle(count) : null
  return {
    order,
    hrefs: rows.map((r) => r.getAttribute('href')),
    countStyle: style && {
      fontFamily: style.fontFamily,
      fontVariantNumeric: style.fontVariantNumeric,
      borderRadius: style.borderRadius,
    },
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
console.log('computed session-count style:', JSON.stringify(provenance.countStyle))
console.log('console errors:', errs.length ? errs.slice(0, 6) : 'none')
await browser.close()
