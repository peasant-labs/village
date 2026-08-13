/* Screenshot the real village Manage surfaces for parity against the fairtrade in-use demo.

   Captures:
     - manage-collectives: /groups list surface (shared CollectivesView)
      - manage-detail:      /groups/:id detail surface (shared manage surface + local page)
      - manage-settings:    /groups/:id/settings settings form

   usage: CHROME_PATH=/path/to/chrome VILLAGE_URL=http://localhost:3000 node scripts/visual/manage-shoot.mjs <theme> <outdir>
*/
import { mkdirSync } from 'node:fs'
import puppeteer from 'puppeteer-core'
import { SurfaceGate } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'

const CHROME = process.env.CHROME_PATH
const ORIGIN = (process.env.VILLAGE_URL || 'http://localhost:3000').replace(/\/$/, '')
const THEME = process.argv[2] || 'dark'
const OUT = process.argv[3] || `/tmp/manage-village-${THEME}`
const GROUP_ID = process.env.VILLAGE_GROUP_ID || 'demo-group'

if (!CHROME) {
  console.error('ERROR [manage-shoot.mjs] CHROME_PATH is unset - set it to your Chrome/Chromium binary.')
  process.exit(1)
}

mkdirSync(OUT, { recursive: true })

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
  throw new Error(`selector "${sel}" never mounted`)
}
const shot = async (name, sel, { fullPage = false } = {}) => {
  const el = await waitFor(sel)
  const box = await el.boundingBox()
  if (!box || box.width < 4 || box.height < 4) throw new Error(`"${sel}" blank/zero-size: ${JSON.stringify(box)}`)
  const path = `${OUT}/${name}.png`
  await page.screenshot({ path, fullPage })
  const r = await gate.assert(name, path, { sel, where: 'manage-shoot.mjs' })
  console.log('shot', name.padEnd(24), `${Math.round(box.width)}x${Math.round(box.height)}`.padEnd(11), `nonbg=${(r.nonbgRatio * 100).toFixed(1)}% colors=${r.distinctColors}`)
}

await page.goto(`${ORIGIN}/groups`, { waitUntil: 'domcontentloaded' })
await pause(900)
await shot('manage-collectives', '.cmg-grid')

await page.goto(`${ORIGIN}/groups/${encodeURIComponent(GROUP_ID)}`, { waitUntil: 'domcontentloaded' })
await pause(900)
await shot('manage-detail', '.cmg-d-main')

// full page: the settings route's DangerZone sits below the form as a sibling (not nested inside
// it), and below the fold of the default 1000px viewport -- a viewport-only (fullPage:false)
// capture never included it (M12: "danger zone is below-fold in the current capture").
await page.goto(`${ORIGIN}/groups/${encodeURIComponent(GROUP_ID)}/settings`, { waitUntil: 'domcontentloaded' })
await pause(900)
await shot('manage-settings', '.cmg-settings form', { fullPage: true })

console.log('console errors:', errs.length ? errs.slice(0, 5) : 'none')
await browser.close()
