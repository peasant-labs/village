/* Host-integration boot check for the Explore surface.

   This is the real route arm: it boots the village home page against a REST
   backend and asserts the shared Explore surface actually renders real browse
   content through the adapter + React Query path.

   The explore mock server is self-contained (scripts/visual/mock-rest-explore.mjs),
   so no Postgres / MinIO / auth stack is required.

   Exit codes:
     0 = the real route rendered a non-empty Explore surface
     2 = the route or its data-ready selector never mounted
     1 = the route mounted but the rendered surface failed the non-empty gate
*/
import { SurfaceGate } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const CHROME = process.env.CHROME_PATH
const ORIGIN = (process.env.VILLAGE_REAL_ORIGIN || 'http://localhost:3000').replace(/\/$/, '')
const URL = process.env.VILLAGE_REAL_URL || `${ORIGIN}/`
const theme = process.argv[2] || 'dark'

if (!CHROME) {
  console.error('ERROR [boot-explore.mjs] CHROME_PATH is unset — set it to your Chrome/Chromium binary.')
  process.exit(1)
}

const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', defaultViewport: { width: 1396, height: 939, deviceScaleFactor: 1 } })
const page = await browser.newPage()
await applyDeterminism(page)
const errs = []
page.on('console', (m) => { if (m.type() === 'error' && !/favicon|404|hydrat/.test(m.text())) errs.push(m.text()) })
page.on('pageerror', (e) => errs.push('pageerr: ' + e.message))

const pause = (ms) => new Promise((r) => setTimeout(r, ms))
const waitFor = async (sel, timeoutMs = 12000) => {
  const start = Date.now()
  while (Date.now() - start < timeoutMs) {
    const el = await page.$(sel)
    if (el) return el
    await pause(120)
  }
  return null
}

const gate = new SurfaceGate(page)

try {
  await page.goto(URL, { waitUntil: 'networkidle0' })
} catch (e) {
  console.error(
    `ERROR [boot-explore.mjs] host-integration FAILED — could not load the real Explore route ${URL}.
  What failed: ${e.message}
  Why: the app is not reachable at that origin.
  Where: boot-explore.mjs initial navigation.
  Means: the production Explore page cannot be rendered for the gate.
  Fix: serve the app and point VILLAGE_REAL_ORIGIN to its origin.`
  )
  await browser.close()
  process.exit(2)
}

await page.evaluate((nextTheme) => localStorage.setItem('peasant-theme', nextTheme), theme)
await page.reload({ waitUntil: 'networkidle0' })

const actualTheme = await page.evaluate(() => document.documentElement.getAttribute('data-theme'))
if (actualTheme !== theme) {
  console.error(
    `ERROR [boot-explore.mjs] the requested theme did not apply.
  What failed: [data-theme]="${actualTheme}" after requesting theme="${theme}".
  Why: the theme toggle/localStorage handshake did not settle.
  Where: boot-explore.mjs theme preflight.
  Means: the boot-arm would validate the wrong theme.
  Fix: confirm the root layout uses the shared theme hook and retry.`
  )
  await browser.close()
  process.exit(3)
}

const cards = await waitFor('.cex-tcard')
if (!cards) {
  console.error(
    `ERROR [boot-explore.mjs] host-integration FAILED — Explore browse cards never mounted at ${URL}.
  What failed: no .cex-tcard element appeared within 12s.
  Why: the browse payload did not load, the adapter failed, or NEXT_PUBLIC_API_URL points at a backend that does not serve Explore fixtures.
  Where: boot-explore.mjs real-route readiness wait.
  Means: the production route would render a loading shell or blank surface instead of browse content.
  Fix: start scripts/visual/mock-rest-explore.mjs, set NEXT_PUBLIC_API_URL to its /api/v1 base, and retry.`
  )
  await browser.close()
  process.exit(2)
}

const assertRoute = async (selector, expectedPath, note) => {
  const el = await waitFor(selector)
  if (!el) {
    throw new Error(`missing clickable selector ${selector} for ${note}`)
  }
  await el.click()
  await page.waitForFunction((expected) => window.location.pathname === expected, {}, expectedPath)
}

await assertRoute('.cex-tcard', '/transcripts/d41a8e', 'transcript exit')
await page.goto(URL, { waitUntil: 'networkidle0' })
await waitFor('.cex-tcard')

await page.click('.cex-viewseg [aria-label="profile view"]')
await page.waitForFunction(() => window.location.pathname === '/users/alice-dev')
await page.goto(URL, { waitUntil: 'networkidle0' })
await waitFor('.cex-tcard')

await page.type('.cex-searchbar input', 'ai')
await pause(700)
const collective = await waitFor('.cex-ccard')
if (!collective) {
  console.error('ERROR [boot-explore.mjs] collective search did not surface a clickable collective card for route assertion.')
  await browser.close()
  process.exit(2)
}
await collective.click()
await page.waitForFunction(() => window.location.pathname === '/groups/ai-research-team')
await page.goto(URL, { waitUntil: 'networkidle0' })

await pause(700)
await page.evaluate(() => window.scrollTo(0, 0))
await pause(150)

const tmp = `/tmp/village-explore-boot-${theme}.png`.replace(/[^\w./-]/g, '_')
const el = await page.$('body')
const box = await el.boundingBox()
await el.screenshot({ path: tmp, captureBeyondViewport: true })
try {
  const r = await gate.assert('cex-explore', tmp, { sel: 'body', where: 'boot-explore.mjs' })
  console.log(`OK host-integration: Explore route rendered ${Math.round(box.width)}x${Math.round(box.height)} — nonbg=${(r.nonbgRatio * 100).toFixed(1)}% colors=${r.distinctColors}`)
} catch (e) {
  console.error(e.message)
  await browser.close()
  process.exit(1)
}

console.log('console errors:', errs.length ? errs.slice(0, 4) : 'none')
await browser.close()
