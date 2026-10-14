/* Screenshot the two surfaces that link a GitHub organization to a profile.

   Two captures per theme, taken from one live page each so they cannot mix builds:
     settings-orgs   the settings profile group with one switch per organization
     profile-orgs    the profile's linked-organizations section

   Build provenance is asserted BEFORE any PNG is written. Both captures exist
   only when this change is served: the settings group must carry a row per
   organization with the switch showing the linked state the mock states, and
   the profile must list exactly the organizations the mock returns as linked.
   A stale server, or one serving another worktree, exits nonzero instead of
   producing a misleading capture.

   Computed styles are read from the live DOM as well, because a scaled PNG
   cannot tell two close token values apart: the run reports the organization
   chip's computed border-radius (the design system is square everywhere) and
   the login's computed font-family (an organization login is a data value, so
   it is drawn in the mono face, and it is never lowercased).

   env:
     VILLAGE_URL     app origin (default http://localhost:3000)
     MOCK_USERNAME   the username the mock serves (default alice-dev)
     CHROME_PATH     Chrome/Chromium binary (required)
     PUPPETEER_CORE  explicit module path to puppeteer-core (optional)
   usage: VILLAGE_URL=... CHROME_PATH=... node account-orgs-shoot.mjs <theme> <outdir>
*/
import { mkdirSync, statSync } from 'node:fs'
import { SurfaceGate, MIN_NONBG_RATIO, MIN_DISTINCT_COLORS } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'

const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const CHROME = process.env.CHROME_PATH
const ORIGIN = (process.env.VILLAGE_URL || 'http://localhost:3000').replace(/\/$/, '')
const USERNAME = process.env.MOCK_USERNAME || 'alice-dev'
const theme = process.argv[2] || 'dark'
const out = process.argv[3] || `/tmp/account-orgs-${theme}`
mkdirSync(out, { recursive: true })

if (!CHROME) {
  console.error('ERROR [account-orgs-shoot.mjs] CHROME_PATH is unset — set it to your Chrome/Chromium binary.')
  process.exit(1)
}

// What scripts/visual/mock-rest-profile.mjs serves. The capture asserts the
// served page against these, so a mock edit that changes them fails loudly.
const LINKED = 'peasant-labs'
const UNLINKED = 'acme-collective'

const browser = await puppeteer.launch({
  executablePath: CHROME,
  headless: 'new',
  defaultViewport: { width: 1460, height: 1200, deviceScaleFactor: 1 },
})
const page = await browser.newPage()
await page.setCookie({ name: 'peasant_token', value: 'demo-token', domain: 'localhost', path: '/' })
await applyDeterminism(page)
await page.evaluateOnNewDocument((nextTheme) => localStorage.setItem('peasant-theme', nextTheme), theme)

async function settle(url) {
  await page.goto(url, { waitUntil: 'networkidle0' })
  await new Promise((r) => setTimeout(r, 900))
  const actualTheme = await page.evaluate(() => document.documentElement.getAttribute('data-theme'))
  if (actualTheme !== theme) {
    await browser.close()
    console.error(`ERROR [account-orgs-shoot.mjs] requested theme ${theme} but the page reports ${actualTheme}.`)
    process.exit(3)
  }
}

async function capture(name, selector, expect) {
  const el = await page.$(selector)
  if (!el) {
    await browser.close()
    console.error(`ERROR [account-orgs-shoot.mjs] ${selector} did not resolve for ${name}.`)
    process.exit(2)
  }
  await expect(el, name)

  const file = `${out}/${name}.png`
  await el.screenshot({ path: file, captureBeyondViewport: true })
  const measured = await new SurfaceGate(page).measure(file)
  if (measured.nonbgRatio < MIN_NONBG_RATIO || measured.distinctColors < MIN_DISTINCT_COLORS) {
    await browser.close()
    console.error(`ERROR [account-orgs-shoot.mjs] ${name} painted no content: nonbg=${(measured.nonbgRatio * 100).toFixed(2)}% colors=${measured.distinctColors}.`)
    process.exit(1)
  }
  console.log('shot', name, `nonbg=${(measured.nonbgRatio * 100).toFixed(1)}% colors=${measured.distinctColors} ${(statSync(file).size / 1024).toFixed(1)}KB`)
}

await settle(`${ORIGIN}/settings`)
await capture('settings-orgs', '[data-testid="settings-profile"]', async (el, name) => {
  const rows = await page.evaluate(
    (node) =>
      [...node.querySelectorAll('[data-testid^="settings-org-"]')].map((row) => ({
        id: row.getAttribute('data-testid'),
        label: row.querySelector('.srow-label')?.textContent?.trim(),
        checked: row.querySelector('[role="switch"]')?.getAttribute('aria-checked'),
        family: getComputedStyle(row.querySelector('.srow-label')).fontFamily,
      })),
    el,
  )
  const linked = rows.find((r) => r.id === `settings-org-${LINKED}`)
  const unlinked = rows.find((r) => r.id === `settings-org-${UNLINKED}`)
  if (!linked || !unlinked) {
    await browser.close()
    console.error(`ERROR [account-orgs-shoot.mjs] ${name} is missing an organization row: ${JSON.stringify(rows)}.`)
    process.exit(2)
  }
  // The switch must show the state the server holds, not a default.
  if (linked.checked !== 'true' || unlinked.checked !== 'false') {
    await browser.close()
    console.error(`ERROR [account-orgs-shoot.mjs] ${name} shows linked=${linked.checked} unlinked=${unlinked.checked}, want true/false.`)
    process.exit(2)
  }
  // A login is a data value: never lowercased, and drawn in the mono face.
  for (const row of [linked, unlinked]) {
    if (row.label !== row.id.replace('settings-org-', '')) {
      await browser.close()
      console.error(`ERROR [account-orgs-shoot.mjs] ${name} drew the login as ${JSON.stringify(row.label)}.`)
      process.exit(2)
    }
    if (!/mono/i.test(row.family)) {
      await browser.close()
      console.error(`ERROR [account-orgs-shoot.mjs] ${name} drew the login in ${row.family}, want the mono face.`)
      process.exit(2)
    }
  }
})

await settle(`${ORIGIN}/users/${USERNAME}`)
await capture('profile-orgs', '[data-testid="profile-orgs"]', async (el, name) => {
  const shown = await page.evaluate(
    (node) =>
      [...node.querySelectorAll('[data-testid="profile-org"]')].map((row) => ({
        login: row.textContent.trim(),
        radius: getComputedStyle(row).borderRadius,
        family: getComputedStyle(row.querySelector('span')).fontFamily,
      })),
    el,
  )
  if (shown.length !== 1 || shown[0].login !== LINKED) {
    await browser.close()
    console.error(`ERROR [account-orgs-shoot.mjs] ${name} lists ${JSON.stringify(shown)}, want exactly ${LINKED}.`)
    process.exit(2)
  }
  if (shown[0].radius !== '0px') {
    await browser.close()
    console.error(`ERROR [account-orgs-shoot.mjs] ${name} drew the chip with radius ${shown[0].radius}, want 0px.`)
    process.exit(2)
  }
  if (!/mono/i.test(shown[0].family)) {
    await browser.close()
    console.error(`ERROR [account-orgs-shoot.mjs] ${name} drew the login in ${shown[0].family}, want the mono face.`)
    process.exit(2)
  }
})

await browser.close()
