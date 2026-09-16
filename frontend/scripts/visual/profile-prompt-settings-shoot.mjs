/* Screenshot the signed-in user's own `preview before attaching` toggle.

   The existing profile-collectives shoot walks the contributed-collectives
   section and asserts that section's fixture values, which this surface does not
   involve. This is the narrow capture for the rail section #113 adds.

   env:
     VILLAGE_URL     the origin of the running app (required)
     CHROME_PATH     Chrome/Chromium binary (required)
     MOCK_USERNAME   the username the mock serves (default alice-dev)
   usage: VILLAGE_URL=http://localhost:3111 CHROME_PATH=... node profile-prompt-settings-shoot.mjs <theme> <outdir>
*/
import { mkdirSync, statSync } from 'node:fs'
import { SurfaceGate, MIN_NONBG_RATIO, MIN_DISTINCT_COLORS } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const CHROME = process.env.CHROME_PATH
const ORIGIN = (process.env.VILLAGE_URL || '').replace(/\/$/, '')
const USERNAME = process.env.MOCK_USERNAME || 'alice-dev'
const theme = process.argv[2] || 'dark'
const out = process.argv[3] || `/tmp/profile-prompt-settings-${theme}`
mkdirSync(out, { recursive: true })

if (!CHROME || !ORIGIN) {
  console.error('ERROR [profile-prompt-settings-shoot.mjs] CHROME_PATH and VILLAGE_URL are required.')
  process.exit(1)
}

const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', defaultViewport: { width: 1460, height: 1000, deviceScaleFactor: 1 } })
const page = await browser.newPage()
await page.setCookie({ name: 'peasant_token', value: 'demo-token', domain: 'localhost', path: '/' })
await applyDeterminism(page)
await page.evaluateOnNewDocument((nextTheme) => localStorage.setItem('peasant-theme', nextTheme), theme)
await page.goto(`${ORIGIN}/users/${USERNAME}`, { waitUntil: 'networkidle0' })
await new Promise((r) => setTimeout(r, 900))

const actualTheme = await page.evaluate(() => document.documentElement.getAttribute('data-theme'))
if (actualTheme !== theme) {
  await browser.close()
  console.error(`ERROR [profile-prompt-settings-shoot.mjs] requested theme ${theme} but the page reports ${actualTheme}.`)
  process.exit(3)
}

const sel = '[data-testid="profile-prompt-settings"]'
const el = await page.$(sel)
if (!el) {
  await browser.close()
  console.error(`ERROR [profile-prompt-settings-shoot.mjs] ${sel} did not resolve; the rail is owner-only, so the signed-in user must be the profile owner.`)
  process.exit(2)
}
// Provenance: the label this change introduces must be the text served.
const label = await page.evaluate((node) => node.querySelector('label')?.textContent?.trim(), el)
if (label !== 'Preview before attaching') {
  await browser.close()
  console.error(`ERROR [profile-prompt-settings-shoot.mjs] the section label reads ${JSON.stringify(label)}; this is not the build under test.`)
  process.exit(2)
}

const file = `${out}/profile-prompt-settings.png`
await el.screenshot({ path: file, captureBeyondViewport: true })
const gate = new SurfaceGate(page)
const measured = await gate.measure(file)
if (measured.nonbgRatio < MIN_NONBG_RATIO || measured.distinctColors < MIN_DISTINCT_COLORS) {
  await browser.close()
  console.error(`ERROR [profile-prompt-settings-shoot.mjs] the capture painted no content: nonbg=${(measured.nonbgRatio * 100).toFixed(2)}% colors=${measured.distinctColors}.`)
  process.exit(1)
}
console.log('shot profile-prompt-settings', `nonbg=${(measured.nonbgRatio * 100).toFixed(1)}% colors=${measured.distinctColors} ${(statSync(file).size / 1024).toFixed(1)}KB`)
await browser.close()
