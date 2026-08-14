/* Host-integration boot check for the village Manage surfaces.

   Verifies the real routes mount non-empty through the live app + REST path:
     - /groups                → collectives list
      - /groups/:id            -> collective detail shell (shared manage surface)
      - /groups/:id/settings   -> settings form

   usage:
     MOCK_REST_PORT=8788 node scripts/visual/mock-rest.mjs &
     NEXT_PUBLIC_API_URL=http://localhost:8788/api/v1 pnpm dev &
     CHROME_PATH=... node scripts/visual/manage-boot-village.mjs <theme>
*/
import puppeteer from 'puppeteer-core'
import { SurfaceGate } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'

const CHROME = process.env.CHROME_PATH
const ORIGIN = (process.env.VILLAGE_REAL_ORIGIN || 'http://localhost:3000').replace(/\/$/, '')
const GROUP_ID = process.env.VILLAGE_GROUP_ID || 'demo-group'
const THEME = process.argv[2] || 'dark'

if (!CHROME) {
  console.error('ERROR [manage-boot-village.mjs] CHROME_PATH is unset - set it to your Chrome/Chromium binary.')
  process.exit(1)
}

const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', defaultViewport: { width: 1460, height: 1000, deviceScaleFactor: 1 } })
const page = await browser.newPage()
await page.setCookie({ name: 'peasant_token', value: 'demo-token', domain: 'localhost', path: '/' })
if (THEME === 'light') {
  await page.evaluateOnNewDocument(() => localStorage.setItem('peasant-theme', 'light'))
}
await applyDeterminism(page)
const errs = []
page.on('console', (m) => { if (m.type() === 'error' && !/favicon|404|hydrat/.test(m.text())) errs.push(m.text()) })
page.on('pageerror', (e) => errs.push('pageerr: ' + e.message))
const pause = (ms) => new Promise((r) => setTimeout(r, ms))
const gate = new SurfaceGate(page)
const waitFor = async (sel, timeoutMs = 12000) => {
  const start = Date.now()
  while (Date.now() - start < timeoutMs) {
    const el = await page.$(sel)
    if (el) return el
    await pause(120)
  }
  return null
}
const capture = async (label, url, sel) => {
  await page.goto(url, { waitUntil: 'networkidle0' })
  await pause(800)
  const el = await waitFor(sel)
  if (!el) {
    throw new Error(`ERROR [manage-boot-village.mjs] ${label} never mounted at ${url} - expected selector ${sel}.`)
  }
  const box = await el.boundingBox()
  const tmp = `/tmp/manage-boot-${label}.png`.replace(/[^\w./-]/g, '_')
  await page.screenshot({ path: tmp, fullPage: false })
  const r = await gate.assert(label, tmp, { sel, where: 'manage-boot-village.mjs' })
  console.log(`OK ${label}: ${Math.round(box.width)}x${Math.round(box.height)} nonbg=${(r.nonbgRatio * 100).toFixed(1)}% colors=${r.distinctColors}`)
}

const shotCurrent = async (label, sel) => {
  const el = await waitFor(sel)
  if (!el) {
    throw new Error(`ERROR [manage-boot-village.mjs] ${label} never mounted - expected selector ${sel}.`)
  }
  const box = await el.boundingBox()
  const tmp = `/tmp/manage-boot-${label}.png`.replace(/[^\w./-]/g, '_')
  await page.screenshot({ path: tmp, fullPage: false })
  const r = await gate.assert(label, tmp, { sel, where: 'manage-boot-village.mjs' })
  console.log(`OK ${label}: ${Math.round(box.width)}x${Math.round(box.height)} nonbg=${(r.nonbgRatio * 100).toFixed(1)}% colors=${r.distinctColors}`)
}

await capture('manage-collectives', `${ORIGIN}/groups`, '.cmg-grid')
await page.goto(`${ORIGIN}/groups/${encodeURIComponent(GROUP_ID)}`, { waitUntil: 'networkidle0' })
await pause(800)
await shotCurrent('manage-detail', '.cmg-d-main')
await page.goto(`${ORIGIN}/groups/${encodeURIComponent(GROUP_ID)}/settings`, { waitUntil: 'networkidle0' })
await pause(600)
await shotCurrent('manage-settings', '.cmg-settings form')

console.log(`OK manage boot: all 3 manage surfaces rendered non-empty through the real routes.`)
console.log('console errors:', errs.length ? errs.slice(0, 4) : 'none')
await browser.close()
