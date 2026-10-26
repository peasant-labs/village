/* Screenshot the collective invitation and notification email surfaces.

   Each surface is a whole mounted page (product chrome plus body), taken from
   the served production build against mock-rest-invitations.mjs. Before any
   PNG is written the page must show the surface's own text, which only the
   build under test renders, and report the requested theme.

   surfaces (argv[2]):
     invite-form      the owner's members section with open invitations
                      (mock viewer: owner)
     my-invitations   the invitee's accept/decline block on /collectives
                      (mock viewer: invitee)
     email-settings   the email section of /settings, address verified
                      (mock viewer: invitee)
     unsubscribe      the signed-out one-click unsubscribe page (no cookie)

   env:
     VILLAGE_URL   the origin of the running app (required)
     CHROME_PATH   Chrome/Chromium binary (required)
   usage: VILLAGE_URL=http://localhost:3195 CHROME_PATH=... node invitations-shoot.mjs <surface> <theme> <outdir>
*/
import { mkdirSync, statSync } from 'node:fs'
import { SurfaceGate, MIN_NONBG_RATIO, MIN_DISTINCT_COLORS } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const SURFACES = {
  'invite-form': {
    path: '/collectives/7a000000-0000-4000-8000-0000000000c1/settings?section=members',
    signedIn: true,
    selector: '[data-testid="settings-members"]',
    // The invite form, and a typed handle no account holds, listed like any other.
    texts: ['invite by username', 'not-yet-signed-up', 'invited by'],
    // The invited table's caption is drawn, not only read to a screen reader.
    visible: ['.on-invited-caption'],
  },
  'my-invitations': {
    path: '/collectives',
    signedIn: true,
    selector: '[data-testid="my-invitations"]',
    texts: ['Eval Crowd', 'Night Shift', 'an owner'],
  },
  'email-settings': {
    path: '/settings',
    signedIn: true,
    selector: '[data-testid="settings-email"]',
    texts: ['sam.okafor@example.org'],
  },
  unsubscribe: {
    path: '/email/unsubscribe?token=harness-token',
    signedIn: false,
    selector: '[data-testid="unsubscribe-ask"]',
    texts: ['stop invitation emails'],
  },
}

const CHROME = process.env.CHROME_PATH
const ORIGIN = (process.env.VILLAGE_URL || '').replace(/\/$/, '')
const name = process.argv[2]
const theme = process.argv[3] || 'dark'
const out = process.argv[4] || `/tmp/invitations-${theme}`
const surface = SURFACES[name]
const die = (code, message) => {
  console.error(`ERROR [invitations-shoot.mjs] ${name}/${theme}: ${message}`)
  process.exit(code)
}
if (!CHROME || !ORIGIN) die(1, 'CHROME_PATH and VILLAGE_URL are required.')
if (!surface) die(1, `unknown surface; choose one of ${Object.keys(SURFACES).join(', ')}.`)
mkdirSync(out, { recursive: true })

const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', defaultViewport: { width: 1460, height: 1000, deviceScaleFactor: 1 } })
const page = await browser.newPage()
if (surface.signedIn) await page.setCookie({ name: 'peasant_token', value: 'demo-token', domain: 'localhost', path: '/' })
await applyDeterminism(page)
await page.evaluateOnNewDocument((nextTheme) => localStorage.setItem('peasant-theme', nextTheme), theme)
await page.goto(`${ORIGIN}${surface.path}`, { waitUntil: 'networkidle0' })
const el = await page.waitForSelector(surface.selector, { timeout: 15000 }).catch(() => null)
if (!el) {
  await browser.close()
  die(2, `${surface.selector} did not render at ${surface.path}.`)
}
await new Promise((r) => setTimeout(r, 900))

const actualTheme = await page.evaluate(() => document.documentElement.getAttribute('data-theme'))
if (actualTheme !== theme) {
  await browser.close()
  die(3, `requested theme ${theme} but the page reports ${actualTheme}.`)
}
// Provenance: the surface's own text must be what this build serves.
const text = await page.evaluate((node) => node.textContent || '', el)
const missing = surface.texts.filter((t) => !text.includes(t))
if (missing.length) {
  await browser.close()
  die(2, `the surface does not show ${JSON.stringify(missing)}; this is not the build under test.`)
}
// Elements the surface must draw, measured rather than assumed from the CSS.
for (const sel of surface.visible || []) {
  const box = await page.evaluate((s) => {
    const node = document.querySelector(s)
    if (!node) return null
    const r = node.getBoundingClientRect()
    return { w: r.width, h: r.height }
  }, sel)
  if (!box || box.w <= 1 || box.h <= 1) {
    await browser.close()
    die(2, `${sel} is not drawn (${JSON.stringify(box)}).`)
  }
}
// Chrome plus body: the persistent header must be mounted too.
if (!(await page.$('header')) || !(await page.$('nav'))) {
  await browser.close()
  die(4, 'no product header or navigation is mounted; this capture would not show the shell.')
}

const file = `${out}/${name}-${theme}.png`
await page.screenshot({ path: file, fullPage: true })
const gate = new SurfaceGate(page)
const measured = await gate.measure(file)
if (measured.nonbgRatio < MIN_NONBG_RATIO || measured.distinctColors < MIN_DISTINCT_COLORS) {
  await browser.close()
  die(1, `the capture painted no content: nonbg=${(measured.nonbgRatio * 100).toFixed(2)}% colors=${measured.distinctColors}.`)
}
console.log('shot', name, theme, `nonbg=${(measured.nonbgRatio * 100).toFixed(1)}% colors=${measured.distinctColors} ${(statSync(file).size / 1024).toFixed(1)}KB`)
await browser.close()
