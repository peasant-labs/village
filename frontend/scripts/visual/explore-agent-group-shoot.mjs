/* Screenshot the collapsed group of agent-driven sessions on the real Explore route.

   Two captures per theme, taken from the same live page:
     cex-explore-agent-collapsed  the browse list ending in "+ N agent sessions"
     cex-explore-agent-expanded   the same list with the group open and its rows badged

   Build provenance is asserted before either capture: the served page must
   actually contain the collapsed group's control, so a stale server or a build
   from another worktree cannot pass this gate silently.

   env:
     VILLAGE_URL     app URL (default http://localhost:3000/)
     CHROME_PATH     Chrome/Chromium binary (required)
     PUPPETEER_CORE  explicit module path to puppeteer-core (optional)
   usage: VILLAGE_URL=... CHROME_PATH=... node explore-agent-group-shoot.mjs <theme> <outdir>
*/
import { mkdirSync, statSync } from 'node:fs'
import { SurfaceGate } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const CHROME = process.env.CHROME_PATH
const URL = process.env.VILLAGE_URL || 'http://localhost:3000/'
const theme = process.argv[2] || 'dark'
const out = process.argv[3] || `/tmp/explore-agent-${theme}`
mkdirSync(out, { recursive: true })

const BASE_VP = { width: 1396, height: 939, deviceScaleFactor: 1 }

if (!CHROME) {
  console.error('ERROR [explore-agent-group-shoot.mjs] CHROME_PATH is unset — set it to your Chrome/Chromium binary.')
  process.exit(1)
}

const die = (code, what, why, means, fix) => {
  console.error(
    `ERROR [explore-agent-group-shoot.mjs] ${what}
  Why: ${why}
  Where: explore-agent-group-shoot.mjs, theme=${theme}, url=${URL}.
  Means: ${means}
  Fix: ${fix}`,
  )
  process.exit(code)
}

const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', defaultViewport: { ...BASE_VP } })
const page = await browser.newPage()
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
  die(3, `the requested theme did not apply: [data-theme]="${actualTheme}" after requesting "${theme}".`,
    'the theme toggle / localStorage handshake did not settle.',
    'the capture would be the wrong theme.',
    'confirm the root layout uses the shared theme hook and retry.')
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

if (!(await waitFor('.cex-tcard'))) {
  await browser.close()
  die(2, 'the Explore browse cards never mounted.',
    'the browse payload is missing, or NEXT_PUBLIC_API_URL points at a backend that does not serve the explore fixtures.',
    'the capture would be blank or a loading skeleton.',
    'start the explore mock REST server, point NEXT_PUBLIC_API_URL at it, and retry.')
}

// Build provenance. The collapsed group's control exists only in this change,
// so a served build that lacks it is not the build under test.
const toggle = await waitFor('[data-testid="agent-session-group-toggle"]')
if (!toggle) {
  await browser.close()
  die(2, 'the collapsed agent-session group never rendered.',
    'the served build predates the group, or the mock did not report agent_total.',
    'the capture would prove nothing about this change.',
    'rebuild and restart the app from THIS worktree, confirm the mock serves agent_total, and retry.')
}
const collapsedLabel = (await page.evaluate((el) => el.textContent.trim(), toggle)).replace(/\s+/g, ' ')
if (!/^\+ \d+ agent session/.test(collapsedLabel)) {
  await browser.close()
  die(1, `the collapsed label reads ${JSON.stringify(collapsedLabel)}.`,
    'the group rendered without its count, so the served bytes are not the expected build.',
    'the capture would show the wrong chrome.',
    'rebuild from this worktree and retry.')
}

await page.evaluate((el) => el.scrollIntoView({ block: 'center' }), toggle)
await pause(250)

const shoot = async (name) => {
  const el = await page.$('body')
  const box = await el.boundingBox()
  if (!box || box.width < 4 || box.height < 4) {
    await browser.close()
    die(1, `body resolved to a blank or zero-size box: ${JSON.stringify(box)}.`,
      'the app shell or the Explore surface did not lay out.',
      'the PNG would be empty.',
      'confirm the route is reachable and the browse fixtures loaded.')
  }
  const file = `${out}/${name}.png`
  await el.screenshot({ path: file, captureBeyondViewport: true })
  const r = await gate.assert(name, file, { sel: 'body', where: 'explore-agent-group-shoot.mjs' })
  const bytes = statSync(file).size
  console.log('shot', name.padEnd(30), `${Math.round(box.width)}x${Math.round(box.height)}`.padEnd(11), `nonbg=${(r.nonbgRatio * 100).toFixed(1)}% colors=${r.distinctColors} ${(bytes / 1024).toFixed(1)}KB`)
  return file
}

await shoot('cex-explore-agent-collapsed')

await toggle.click()
const rows = await waitFor('[data-testid="agent-session-group-rows"] a[href^="/transcripts/"]')
if (!rows) {
  await browser.close()
  die(1, 'expanding the group produced no rows.',
    'the origin=agent request failed or returned nothing.',
    'the expanded capture would show an empty group.',
    'confirm the mock serves the origin=agent scope and retry.')
}
const badges = await page.$$eval('[data-testid="agent-session-group-rows"] [data-testid="agent-session-badge"]', (els) => els.length)
if (badges < 1) {
  await browser.close()
  die(1, 'the expanded rows carry no agent-session label.',
    'the served build renders the rows without the badge.',
    'the capture would not show what the group promises.',
    'rebuild from this worktree and retry.')
}
await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight))
await pause(400)
await shoot('cex-explore-agent-expanded')

console.log('provenance', `collapsed-label=${JSON.stringify(collapsedLabel)}`, `expanded-badges=${badges}`)
console.log('console errors:', errs.length ? errs.slice(0, 6) : 'none')
await browser.close()
