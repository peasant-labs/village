/* Screenshot the real village Manage surfaces for parity against the fairtrade in-use demo.

   Captures:
     - manage-collectives: /groups list surface (shared CollectivesView)
      - manage-detail:      /groups/:id detail surface (shared manage surface + local page),
                             captured as manage-detail for an OWNER viewer and as
                             manage-detail-member for a MEMBER viewer, so both header
                             contribute actions are on the record
      - manage-contribute:  /groups/:id/contribute -- the project > branch > session tree +
                             preview column (village#66), at a wide (two-column) and a narrow
                             (single-column, below the 880px container breakpoint) viewport,
                             plus a progress/receipt-state capture of one project's run
      - manage-settings:    /groups/:id/settings settings form

   usage: CHROME_PATH=/path/to/chrome VILLAGE_URL=http://localhost:3000 node scripts/visual/manage-shoot.mjs <theme> <outdir>

   MOCK_ROLE (owner | member, default owner) must be set to the SAME value the REST mock was
   started with; it decides the detail capture's name and which header contribute action this
   script requires to be present. A mismatch fails the shot instead of writing a capture that
   silently shows the other role's surface. */
import { mkdirSync } from 'node:fs'
import puppeteer from 'puppeteer-core'
import { SurfaceGate } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'

const CHROME = process.env.CHROME_PATH
const ORIGIN = (process.env.VILLAGE_URL || 'http://localhost:3000').replace(/\/$/, '')
const THEME = process.argv[2] || 'dark'
const OUT = process.argv[3] || `/tmp/manage-village-${THEME}`
const GROUP_ID = process.env.VILLAGE_GROUP_ID || 'demo-group'
const VIEWER_ROLES = ['owner', 'member']
const VIEWER_ROLE = process.env.MOCK_ROLE || 'owner'

if (!VIEWER_ROLES.includes(VIEWER_ROLE)) {
  console.error(
    `ERROR [manage-shoot.mjs] MOCK_ROLE="${VIEWER_ROLE}" is not a role this shoot can capture. ` +
    `Valid values are ${VIEWER_ROLES.join(' | ')} (default owner), and the value must match the ` +
    `MOCK_ROLE the REST mock was started with. Restart both with the same role and retry.`,
  )
  process.exit(1)
}

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
// Fail closed on a role mismatch, on BOTH axes. The page must state the role this shoot claims
// to be capturing (otherwise the REST mock is serving the other role), and it must offer exactly
// one contribute action -- an owner reaches the route through the action village renders itself,
// a member through the shared manage surface's own action, and nobody should ever see two.
const served = await page.evaluate(() => {
  const label = [...document.querySelectorAll('*')].find(
    (element) => element.children.length === 0 && element.textContent.trim() === 'your role',
  )
  const tile = label?.parentElement
  const role = tile ? tile.textContent.replace('your role', '').trim() : null
  const actions = [...document.querySelectorAll('button')].filter(
    (button) => button.textContent.trim().toLowerCase() === 'contribute',
  ).length
  return { role, actions }
})
if (served.role !== VIEWER_ROLE) {
  console.error(
    `ERROR [manage-shoot.mjs] /groups/${GROUP_ID} reports the viewer's role as ` +
    `${JSON.stringify(served.role)} while this shoot was asked for MOCK_ROLE=${VIEWER_ROLE}. ` +
    `The REST mock is serving the other role, so the capture would be named after a surface it ` +
    `does not show. Restart the mock with MOCK_ROLE=${VIEWER_ROLE} and retry.`,
  )
  process.exit(1)
}
if (served.actions !== 1) {
  console.error(
    `ERROR [manage-shoot.mjs] /groups/${GROUP_ID} offered ${served.actions} contribute actions ` +
    `for MOCK_ROLE=${VIEWER_ROLE}; exactly one is expected (an owner sees village's own action, a ` +
    `member sees the shared manage surface's). Check the served build is the one you just built, ` +
    `then retry.`,
  )
  process.exit(1)
}
await shot(VIEWER_ROLE === 'member' ? 'manage-detail-member' : 'manage-detail', '.cmg-d-main')

await page.goto(`${ORIGIN}/groups/${encodeURIComponent(GROUP_ID)}/contribute`, { waitUntil: 'domcontentloaded' })
await pause(900)
await shot('manage-contribute', '.cmg-root')

// Click a session's title to drive the preview column, and tick a project's
// checkbox so the sticky footer's "N selected" + contribute button have real
// state -- otherwise the wide capture would only ever show the tree's own
// empty preview state.
const previewRow = await page.$('[data-testid^="contribute-session-row-"] button')
if (previewRow) await previewRow.click()
const projectCheckbox = await page.$('[data-testid^="contribute-group-row-"] input[type="checkbox"]')
if (projectCheckbox) await projectCheckbox.click()
await pause(400)
await shot('manage-contribute-preview', '.cmg-root')

// Fire the run and capture the progress bar mid-flight, then the settled
// receipt -- the mock's /shares handler answers immediately, so the
// progress-bar window is real but brief; screenshot right after the click
// rather than waiting, so the bar has a chance to still be visible.
const buttons = await page.$$('button')
let contributeBtn = null
for (const b of buttons) {
  const text = await page.evaluate((el) => el.textContent, b)
  if (text && /^contribute \d+ transcript/.test(text.trim())) { contributeBtn = b; break }
}
if (contributeBtn) {
  await contributeBtn.click()
  await pause(300)
  // The selected row is private, so this opens the confirm dialog first
  // (production behaviour) -- tick consent and confirm to actually start
  // the run before capturing progress/receipt.
  const dialogButtons = await page.$$('button')
  let consentCheckbox = null
  for (const b of await page.$$('input[type="checkbox"]')) {
    const label = await page.evaluate((el) => el.closest('label')?.textContent || '', b)
    if (/i understand and consent/i.test(label)) { consentCheckbox = b; break }
  }
  if (consentCheckbox) await consentCheckbox.click()
  let confirmBtn = null
  for (const b of dialogButtons) {
    const text = await page.evaluate((el) => el.textContent, b)
    if (text && /contribute & make visible/i.test(text)) { confirmBtn = b; break }
  }
  if (confirmBtn) {
    await confirmBtn.click()
    await pause(300)
    await shot('manage-contribute-progress', '.cmg-root')
    await pause(1600)
    await shot('manage-contribute-receipt', '.cmg-root')
  } else {
    console.warn('manage-contribute-progress/receipt skipped: confirm dialog button not found')
  }
}

// Narrow viewport: below the page's 880px @container breakpoint, the tree
// and preview columns must stack into ONE column instead of the wide
// two-column grid.
await page.setViewport({ width: 700, height: 1000, deviceScaleFactor: 1 })
await page.goto(`${ORIGIN}/groups/${encodeURIComponent(GROUP_ID)}/contribute`, { waitUntil: 'domcontentloaded' })
await pause(900)
await shot('manage-contribute-narrow', '.cmg-root')
await page.setViewport({ width: 1460, height: 1000, deviceScaleFactor: 1 })

// full page: the settings route's DangerZone sits below the form as a sibling (not nested inside
// it), and below the fold of the default 1000px viewport -- a viewport-only (fullPage:false)
// capture would omit the danger zone entirely. Only an owner can open settings at all, so a
// member run stops here rather than failing on a form that route correctly refuses to render.
if (VIEWER_ROLE === 'owner') {
  await page.goto(`${ORIGIN}/groups/${encodeURIComponent(GROUP_ID)}/settings`, { waitUntil: 'domcontentloaded' })
  await pause(900)
  await shot('manage-settings', '.cmg-settings form', { fullPage: true })
} else {
  console.log('skip manage-settings: the settings route is owner-only and MOCK_ROLE=' + VIEWER_ROLE)
}

console.log('console errors:', errs.length ? errs.slice(0, 5) : 'none')
await browser.close()
