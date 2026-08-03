/* Screenshot the real village Explore surface (the app root):
     wire TranscriptListResponse + collective search + popular tags → the shared <Explore> composer.

   The capture is theme-stable and includes the app chrome plus representative body content:
   fixed navbar, search / facet rail, browse cards, and pagination when present.

   env:
     VILLAGE_URL     app URL (default http://localhost:3000/)
     CHROME_PATH     Chrome/Chromium binary (required)
     PUPPETEER_CORE  explicit module path to puppeteer-core (optional)
   usage: VILLAGE_URL=http://localhost:3000/ CHROME_PATH=/path/to/chrome node explore-shoot.mjs <theme> <outdir>
*/
import { mkdirSync, statSync } from 'node:fs'
import { SurfaceGate } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const CHROME = process.env.CHROME_PATH
const URL = process.env.VILLAGE_URL || 'http://localhost:3000/'
const theme = process.argv[2] || 'dark'
const out = process.argv[3] || `/tmp/explore-${theme}`
mkdirSync(out, { recursive: true })

const BASE_VP = { width: 1396, height: 939, deviceScaleFactor: 1 }

if (!CHROME) {
  console.error('ERROR [explore-shoot.mjs] CHROME_PATH is unset — set it to your Chrome/Chromium binary.')
  process.exit(1)
}

const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', defaultViewport: { ...BASE_VP } })
const page = await browser.newPage()
// Authenticate, matching manage-shoot.mjs's cookie -- otherwise the capture is a
// SIGNED-OUT view, which correctly (per real app auth-gating) shows only the "explore"
// nav item instead of the full explore/collectives/publish/profile set the demo shows
// unconditionally (it has no auth to model against). Captured logged-in for parity with
// manage and to match the demo's full nav.
await page.setCookie({ name: 'peasant_token', value: 'demo-token', domain: 'localhost', path: '/' })
await applyDeterminism(page)
const errs = []
page.on('console', (m) => { if (m.type() === 'error' && !/favicon|404|hydrat/.test(m.text())) errs.push(m.text()) })
page.on('pageerror', (e) => errs.push('pageerr: ' + e.message))

await page.goto(URL, { waitUntil: 'networkidle0' })
await page.evaluate((nextTheme) => localStorage.setItem('peasant-theme', nextTheme), theme)
await page.reload({ waitUntil: 'networkidle0' })
await new Promise((r) => setTimeout(r, 900))

const actualTheme = await page.evaluate(() => document.documentElement.getAttribute('data-theme'))
if (actualTheme !== theme) {
  await browser.close()
  console.error(
    `ERROR [explore-shoot.mjs] the requested theme did not apply.
  What failed: [data-theme]="${actualTheme}" after requesting theme="${theme}".
  Why: the theme toggle/localStorage handshake did not settle.
  Where: explore-shoot.mjs theme preflight.
  Means: the capture would be the wrong theme.
  Fix: confirm the root layout is using the shared theme hook and retry.`
  )
  process.exit(3)
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

const ready = await waitFor('.cex-tcard')
if (!ready) {
  await browser.close()
  console.error(
    `ERROR [explore-shoot.mjs] the Explore browse cards never mounted at ${URL}.
  What failed: no .cex-tcard element appeared after the app loaded.
  Why: the browse payload is missing, the adapter is broken, or NEXT_PUBLIC_API_URL points at a backend that does not serve Explore fixtures.
  Where: explore-shoot.mjs surface readiness wait.
  Means: the capture would be blank or stuck on a loading skeleton.
  Fix: start the explore mock REST server, point NEXT_PUBLIC_API_URL at it, and confirm the root route renders the shared Explore surface.`
  )
  process.exit(2)
}

await page.evaluate(() => window.scrollTo(0, 0))
await pause(150)

const el = await page.$('body')
const box = await el.boundingBox()
if (!box || box.width < 4 || box.height < 4) {
  await browser.close()
  console.error(
    `ERROR [explore-shoot.mjs] body resolved to a blank or zero-size box at ${URL}.
  What failed: ${JSON.stringify(box)}.
  Why: the app shell or Explore surface did not lay out.
  Where: explore-shoot.mjs, full-page body capture.
  Means: the PNG would be empty or near-empty.
  Fix: confirm the route is reachable and that the browse fixtures loaded.`
  )
  process.exit(1)
}

const file = `${out}/cex-explore.png`
await el.screenshot({ path: file, captureBeyondViewport: true })
const r = await gate.assert('cex-explore', file, { sel: 'body', where: 'explore-shoot.mjs' })
const bytes = statSync(file).size

console.log('shot', 'cex-explore'.padEnd(22), `${Math.round(box.width)}x${Math.round(box.height)}`.padEnd(11), `nonbg=${(r.nonbgRatio * 100).toFixed(1)}% colors=${r.distinctColors} ${(bytes / 1024).toFixed(1)}KB`)
console.log('console errors:', errs.length ? errs.slice(0, 6) : 'none')
await browser.close()
