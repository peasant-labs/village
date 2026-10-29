/* Screenshot the integrity badge and the flagged-turn highlight on the REAL transcript route.

   Captures, per viewer (MOCK_INTEGRITY_VIEWER = public | member | owner, which must match the value the
   REST mock was started with):
     - integrity-<viewer>-header:  the flagged header with the (?) panel open
     - integrity-<viewer>-turn:    a highlighted turn
   and, for the owner only:
     - integrity-owner-manage-access: the manage access dialog with its disclosure line
   and for any run:
     - integrity-privacy: the /privacy integrity section

   Fails closed: a missing element, a viewer mismatch, a computed font-family that is not the design
   system's, or a served build without this change's strings exits non-zero.

   usage: CHROME_PATH=/path/to/chrome VILLAGE_URL=http://localhost:3000 \
          MOCK_INTEGRITY_VIEWER=owner node scripts/visual/integrity-shoot.mjs <theme> <outdir>
   Write captures to frontend/review-capture/integrity/ (untracked) or a scratch directory. */
import { mkdirSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { join, resolve } from 'node:path'
import puppeteer from 'puppeteer-core'
import { SurfaceGate } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'

const CHROME = process.env.CHROME_PATH
const ORIGIN = (process.env.VILLAGE_URL || 'http://localhost:3000').replace(/\/$/, '')
const VIEWER = process.env.MOCK_INTEGRITY_VIEWER || 'public'
const THEME = process.argv[2] || 'dark'
const OUT = process.argv[3] || `review-capture/integrity/${THEME}`
const ID = process.env.MOCK_TRANSCRIPT_ID || 'integrity-demo'
const NEXT_DIR = resolve(process.env.NEXT_DIST_DIR || '.next')
const WHERE = 'integrity-shoot.mjs'

const fail = (message, code = 1) => {
  console.error(`ERROR [${WHERE}] ${message}`)
  process.exit(code)
}
if (!CHROME) fail('CHROME_PATH is unset - set it to your Chrome/Chromium binary.')
if (!['public', 'member', 'owner'].includes(VIEWER)) fail(`MOCK_INTEGRITY_VIEWER=${JSON.stringify(VIEWER)} is not public | member | owner.`)
if (!['dark', 'light'].includes(THEME)) fail(`theme ${JSON.stringify(THEME)} is not dark | light.`)

/* Provenance: the served build must carry strings only this change introduces, or a stale build would
   produce valid-looking captures of a page without the badge. */
function walk(dir) {
  const files = []
  for (const name of readdirSync(dir)) {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) files.push(...walk(path))
    else if (/\.(js|css|html|rsc)$/.test(name)) files.push(path)
  }
  return files
}
const MARKERS = ['txn-integrity-badge', 'Village-Content-Hash']
const built = walk(join(NEXT_DIR, 'static')).concat(walk(join(NEXT_DIR, 'server')))
const text = built.map((f) => readFileSync(f, 'utf8'))
for (const marker of MARKERS) {
  if (!text.some((t) => t.includes(marker))) fail(`the build in ${NEXT_DIR} does not contain ${JSON.stringify(marker)}; it predates this change. Rebuild and retry.`, 2)
}
console.log(`provenance ok: ${MARKERS.join(', ')} found in ${NEXT_DIR}`)

mkdirSync(OUT, { recursive: true })
const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', defaultViewport: { width: 1460, height: 1000, deviceScaleFactor: 1 } })
const page = await browser.newPage()
if (VIEWER !== 'public') await page.setCookie({ name: 'peasant_token', value: 'demo-token', domain: 'localhost', path: '/' })
if (THEME === 'light') await page.evaluateOnNewDocument(() => localStorage.setItem('peasant-theme', 'light'))
await applyDeterminism(page)
const errs = []
page.on('console', (m) => { if (m.type() === 'error' && !/favicon|404|hydrat|401/.test(m.text())) errs.push(m.text()) })
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
const shot = async (name, sel) => {
  const el = await waitFor(sel)
  const box = await el.boundingBox()
  if (!box || box.width < 4 || box.height < 4) throw new Error(`"${sel}" blank/zero-size: ${JSON.stringify(box)}`)
  const path = `${OUT}/${name}-${THEME}.png`
  await page.screenshot({ path })
  const r = await gate.assert(`${name}-${THEME}`, path, { sel, where: WHERE })
  console.log('shot', name.padEnd(34), `${Math.round(box.width)}x${Math.round(box.height)}`.padEnd(11), `nonbg=${(r.nonbgRatio * 100).toFixed(1)}%`)
}
const fontOf = (sel) => page.$eval(sel, (el) => getComputedStyle(el).fontFamily)
const assertFont = async (label, sel) => {
  const family = await fontOf(sel)
  if (!/atkinson/i.test(family)) fail(`${label} computes font-family ${JSON.stringify(family)}; the design system's Atkinson Hyperlegible is expected.`)
  console.log(`font ok: ${label} -> ${family.split(',')[0]}`)
}

try {
  await page.goto(`${ORIGIN}/transcripts/${encodeURIComponent(ID)}`, { waitUntil: 'domcontentloaded' })
  await waitFor('.txn-app')
  await waitFor('.txn-integrity-badge')
  // The viewer must be who the shoot says, or the capture is named after a surface it does not show.
  const signedIn = await page.evaluate(() => document.cookie.includes('peasant_token'))
  if (signedIn !== (VIEWER !== 'public')) fail(`cookie state does not match MOCK_INTEGRITY_VIEWER=${VIEWER}.`)
  await assertFont('badge', '.txn-integrity-badge')

  // The marks only draw when the rendered hash equals the row hash: the matching header is proven here.
  await pause(400)
  const marks = await page.$$eval('.txn-integrity-mark', (els) => els.length)
  if (marks < 1) fail('no .txn-integrity-mark drew; the content route header did not reach the viewer or does not match the row hash.')
  console.log(`marks drawn: ${marks}`)

  await page.click('.txn-integrity-badge')
  await waitFor('.txn-integrity-panel:not([hidden])')
  await shot(`integrity-${VIEWER}-header`, '.txn-integrity-panel')
  await page.keyboard.press('Escape')
  await pause(200)

  const mark = await waitFor('.txn-integrity-mark')
  await mark.evaluate((el) => el.scrollIntoView({ block: 'center' }))
  await pause(300)
  await shot(`integrity-${VIEWER}-turn`, '.txn-integrity-mark')

  if (VIEWER === 'owner') {
    const more = await page.evaluateHandle(() => [...document.querySelectorAll('button')].find((b) => b.getAttribute('aria-label') === 'more' || b.textContent.trim() === 'more'))
    if (!more.asElement()) fail('the owner has no "more" menu on the flagged transcript.')
    await more.asElement().click()
    const item = await page.evaluateHandle(() => [...document.querySelectorAll('[role="menuitem"]')].find((i) => i.textContent.trim() === 'manage access'))
    if (!item.asElement()) fail('the "more" menu offers no "manage access" row.')
    await item.asElement().click()
    await waitFor('[role="dialog"]')
    const line = await page.evaluateHandle(() => {
      const dialog = document.querySelector('[role="dialog"]')
      return [...dialog.querySelectorAll('p')].find((p) => p.textContent.includes('an automated check reads it'))
    })
    if (!line.asElement()) fail('the manage access dialog shows no disclosure line.')
    const family = await line.asElement().evaluate((el) => getComputedStyle(el).fontFamily)
    if (!/atkinson/i.test(family)) fail(`the disclosure line computes font-family ${JSON.stringify(family)}; Atkinson Hyperlegible is expected.`)
    console.log(`font ok: disclosure line -> ${family.split(',')[0]}`)
    await shot('integrity-owner-manage-access', '[role="dialog"]')
  }

  await page.goto(`${ORIGIN}/privacy#privacy-integrity`, { waitUntil: 'domcontentloaded' })
  await waitFor('#privacy-integrity')
  await pause(300)
  await shot('integrity-privacy', '#privacy-integrity')
} catch (e) {
  fail(e.message)
} finally {
  console.log('console errors:', errs.length ? errs.slice(0, 5) : 'none')
  await browser.close()
}
