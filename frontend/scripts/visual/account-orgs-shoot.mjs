/* Screenshot the two surfaces that link a GitHub organization to a profile.

   Two captures per theme, taken from one live page each so they cannot mix builds:
     settings-orgs   the organizations section, one switch per organization
     profile-orgs    the profile's linked-organizations section

   Build provenance is asserted BEFORE any PNG is written. Both captures exist
   only when this change is served: the organizations must be their OWN settings
   section (never rows inside "profile"), each row must carry the avatar GitHub
   sent and the switch must show the state the mock states, and the profile must
   list exactly the organizations the mock returns as linked.

   An avatar is asserted by its LOADED pixels, not by its presence in the
   payload: an `img` whose src never resolves still has the right attribute, and
   a capture that showed a broken image would otherwise pass.

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

async function fail(message) {
  await browser.close()
  console.error(`ERROR [account-orgs-shoot.mjs] ${message}`)
  process.exit(2)
}

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
  if (!el) await fail(`${selector} did not resolve for ${name}.`)
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
await capture('settings-orgs', '[data-testid="settings-orgs"]', async (el, name) => {
  const rows = await page.evaluate(
    (node) =>
      [...node.querySelectorAll('[data-testid^="settings-org-"]')].map((row) => {
        const img = row.querySelector('img[data-testid="settings-avatar"]')
        const label = row.querySelector('.srow-label')
        const track = row.querySelector('.sw')
        return {
          id: row.getAttribute('data-testid'),
          label: label?.textContent?.trim(),
          checked: row.querySelector('[role="switch"]')?.getAttribute('aria-checked'),
          family: getComputedStyle(label).fontFamily,
          hasAvatar: img != null,
          avatarLoaded: img != null && img.naturalWidth > 0,
          switchLeft: track ? Math.round(track.getBoundingClientRect().left) : null,
          switchTop: track ? Math.round(track.getBoundingClientRect().top) : null,
          avatarTop: img ? Math.round(img.getBoundingClientRect().top) : null,
        }
      }),
    el,
  )

  if (await page.$('[data-testid="settings-profile"] [data-testid^="settings-org-"]')) {
    await fail(`${name} drew the organizations inside the profile group; they are their own section.`)
  }
  if (rows.length !== 2) await fail(`${name} drew ${rows.length} organization rows, want 2: ${JSON.stringify(rows)}.`)

  const linked = rows.find((r) => r.id === `settings-org-${LINKED}`)
  const unlinked = rows.find((r) => r.id === `settings-org-${UNLINKED}`)
  if (!linked || !unlinked) await fail(`${name} is missing an organization row: ${JSON.stringify(rows)}.`)
  // The switch must show the state the server holds, not a default.
  if (linked.checked !== 'true' || unlinked.checked !== 'false') {
    await fail(`${name} shows linked=${linked.checked} unlinked=${unlinked.checked}, want true/false.`)
  }
  // A login is a data value: never lowercased, drawn in the mono face, beside
  // the avatar GitHub sent — asserted by loaded pixels, not by the attribute.
  for (const row of [linked, unlinked]) {
    if (row.label !== row.id.replace('settings-org-', '')) {
      await fail(`${name} drew the login as ${JSON.stringify(row.label)}.`)
    }
    if (!/mono/i.test(row.family)) await fail(`${name} drew the login in ${row.family}, want the mono face.`)
    if (!row.hasAvatar || !row.avatarLoaded) {
      await fail(`${name} did not draw ${row.id}'s avatar (present=${row.hasAvatar} loaded=${row.avatarLoaded}).`)
    }
  }
  // Every track in the section sits at the same x. A switch is as wide as its
  // own state word, and the control is right-aligned, so a row reading "off"
  // otherwise pulls its track a few pixels left of a row reading "on".
  const lefts = [...new Set(rows.map((r) => r.switchLeft))]
  if (lefts.length !== 1) {
    await fail(`${name} drew the switches at different x positions: ${JSON.stringify(rows.map((r) => [r.id, r.switchLeft]))}.`)
  }
  // ...and each track starts level with its own label's first line, which is the
  // avatar's top. A centred control floats in the gap between the label and its
  // help line, which is the defect this pins.
  for (const row of rows) {
    if (row.switchTop !== row.avatarTop) {
      await fail(`${name} drew ${row.id}'s switch at top ${row.switchTop}, but its avatar starts at ${row.avatarTop}.`)
    }
  }
})

await settle(`${ORIGIN}/users/${USERNAME}`)
await capture('profile-orgs', '[data-testid="profile-orgs"]', async (el, name) => {
  const shown = await page.evaluate(
    (node) =>
      [...node.querySelectorAll('[data-testid="profile-org"]')].map((row) => {
        const img = row.querySelector('img[data-testid="profile-org-avatar"]')
        return {
          login: row.textContent.trim(),
          radius: getComputedStyle(row).borderRadius,
          family: getComputedStyle(row.querySelector('span')).fontFamily,
          hasAvatar: img != null,
          avatarLoaded: img != null && img.naturalWidth > 0,
        }
      }),
    el,
  )
  if (shown.length !== 1 || shown[0].login !== LINKED) {
    await fail(`${name} lists ${JSON.stringify(shown)}, want exactly ${LINKED}.`)
  }
  if (shown[0].radius !== '0px') await fail(`${name} drew the chip with radius ${shown[0].radius}, want 0px.`)
  if (!/mono/i.test(shown[0].family)) await fail(`${name} drew the login in ${shown[0].family}, want the mono face.`)
  if (!shown[0].hasAvatar || !shown[0].avatarLoaded) {
    await fail(`${name} did not draw the organization's avatar (present=${shown[0].hasAvatar} loaded=${shown[0].avatarLoaded}).`)
  }
})

await browser.close()
