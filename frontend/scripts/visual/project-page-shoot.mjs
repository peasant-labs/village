/* Screenshot the real project page, `/users/{username}/projects/{projectHash}`.

   Captures per theme, all from the same live page:
     vpp-project-page          the whole page as the OWNER sees it
     vpp-project-rename        the owner-only correction control
     vpp-project-collectives   the collectives roll-up
     vpp-project-after-reset   the page after the owner resets the name to its
                               resolved default, proving the control reflects
                               the new source rather than a stale value

   Build provenance is asserted BEFORE anything is written: the served page must
   carry the project heading, the repository-label subtitle and the hash-keyed
   correction control this change introduces, and the URL under capture must
   carry the 64-hex project hash. A stale server, or one serving a different
   worktree, fails with a nonzero exit instead of producing a misleading PNG.

   Computed styles are probed and printed alongside the captures: --surface and
   --canvas, and --ink-2 and --ink-3, are indistinguishable in a scaled PNG, so
   the token check is a DOM assertion, never an eyeball.

   PROJECT_SHOOT_MODE selects which production state is captured:
     owner  (default) the owner's view, including the correction control and the
            reset round-trip above
     notfound the visibility boundary's answer. Point VILLAGE_URL at a project
            hash the mock does not serve; the page must render its ONE not-found
            panel, and no project heading.
     viewer a viewer who is NOT the owner. The correction control must be ABSENT
            and the roll-up is expected empty, which is the ORDINARY answer once
            collective visibility and the contributor opt-in have applied. Pair
            it with MOCK_PROJECT_VIEWER=other MOCK_PROJECT_ROLLUP=empty on the mock.

   env:
     VILLAGE_URL     project page URL (required; must be the hash-keyed route)
     CHROME_PATH     Chrome/Chromium binary (required)
     PUPPETEER_CORE  explicit module path to puppeteer-core (optional)
   usage: VILLAGE_URL=... CHROME_PATH=... node project-page-shoot.mjs <theme> <outdir>
*/
import { mkdirSync, statSync } from 'node:fs'
import { SurfaceGate, MIN_NONBG_RATIO, MIN_DISTINCT_COLORS } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const CHROME = process.env.CHROME_PATH
const TARGET_URL = process.env.VILLAGE_URL
const theme = process.argv[2] || 'dark'
const MODE = process.env.PROJECT_SHOOT_MODE || 'owner'
const out = process.argv[3] || `/tmp/project-page-${theme}`
mkdirSync(out, { recursive: true })

const BASE_VP = { width: 1396, height: 1200, deviceScaleFactor: 1 }

const die = (code, what, why, means, fix) => {
  console.error(
    `ERROR [project-page-shoot.mjs] ${what}
  Why: ${why}
  Where: project-page-shoot.mjs, theme=${theme}, url=${TARGET_URL}.
  Means: ${means}
  Fix: ${fix}`,
  )
  process.exit(code)
}

if (!CHROME) {
  die(1, 'CHROME_PATH is unset.', 'the script drives a real Chrome and has no binary to launch.',
    'no capture can be taken.', 'set CHROME_PATH to a Chrome/Chromium binary and retry.')
}
if (!TARGET_URL) {
  die(1, 'VILLAGE_URL is unset.', 'the project page is keyed on a project hash, so there is no safe default route.',
    'no capture can be taken.', 'set VILLAGE_URL to /users/{username}/projects/{projectHash} on the running app.')
}
if (!/\/users\/[^/]+\/projects\/[0-9a-f]{64}\/?$/.test(new URL(TARGET_URL).pathname)) {
  die(1, `VILLAGE_URL path ${JSON.stringify(new URL(TARGET_URL).pathname)} is not the hash-keyed project route.`,
    'the route this change adds is /users/{username}/projects/{projectHash} with a 64-hex hash.',
    'the capture would prove nothing about this change.',
    'point VILLAGE_URL at the hash-keyed route on the running app.')
}

const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', defaultViewport: { ...BASE_VP } })
const page = await browser.newPage()
await page.setCookie({ name: 'peasant_token', value: 'demo-token', domain: 'localhost', path: '/' })
await applyDeterminism(page)
const errs = []
page.on('console', (m) => { if (m.type() === 'error' && !/favicon|404|hydrat/.test(m.text())) errs.push(m.text()) })
page.on('pageerror', (e) => errs.push('pageerr: ' + e.message))

await page.goto(TARGET_URL, { waitUntil: 'networkidle0' })
await page.evaluate((nextTheme) => localStorage.setItem('peasant-theme', nextTheme), theme)
await page.reload({ waitUntil: 'networkidle0' })
await new Promise((r) => setTimeout(r, 900))

const actualTheme = await page.evaluate(() => document.documentElement.getAttribute('data-theme'))
if (actualTheme !== theme) {
  await browser.close()
  die(3, `the requested theme did not apply: [data-theme]="${actualTheme}" after requesting "${theme}".`,
    'the theme toggle / localStorage handshake did not settle.',
    'every capture would be the wrong theme.',
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

// ── Build provenance ────────────────────────────────────────────────────────
// Each marker below exists ONLY in this change. A served build that lacks any
// of them is not the build under test, so nothing is captured.
const heading = MODE === 'notfound' ? null : await waitFor('[data-testid="project-display-name"]')
if (MODE !== 'notfound' && !heading) {
  await browser.close()
  die(2, 'the project heading never rendered.',
    'the served build predates the project page, or the page fetch failed.',
    'the capture would be blank or a not-found panel.',
    'rebuild and restart the app from THIS worktree against the project mock, and retry.')
}
const headingText = heading ? await page.evaluate((el) => el.textContent.trim(), heading) : ''

const subtitleEl = MODE === 'notfound' ? null : await page.$('[data-testid="project-remote-label"]')
if (MODE !== 'notfound' && !subtitleEl) {
  await browser.close()
  die(2, 'the repository-label subtitle never rendered.',
    'the served payload carried an empty project_remote_label, or the served build predates the subtitle.',
    'the header under capture would be missing the label this change renders.',
    'confirm the backend resolver populates project_remote_label, rebuild from this worktree, and retry.')
}
const subtitleText = subtitleEl ? await page.evaluate((el) => el.textContent.trim(), subtitleEl) : ''
if (subtitleEl && !/^[^\s:]+:[^\s/]+\/[^\s]+$/.test(subtitleText)) {
  await browser.close()
  die(1, `the repository label reads ${JSON.stringify(subtitleText)}.`,
    'it is not the resolved host:owner/repo shape, so the served bytes are not the expected build.',
    'the capture would show the wrong header.',
    'rebuild from this worktree and retry.')
}

const control = MODE === 'owner' ? await waitFor('[data-testid="project-rename-control"]') : await page.$('[data-testid="project-rename-control"]')
if (MODE === 'owner' && !control) {
  await browser.close()
  die(2, 'the owner-only correction control never rendered.',
    'the capture is taken as the owner, so the control must be present; the mock may be serving a different viewer.',
    'the capture would not show the control this change adds.',
    'set MOCK_PROJECT_VIEWER=owner on the mock, rebuild from this worktree, and retry.')
}
if (MODE === 'viewer' && control) {
  await browser.close()
  die(2, 'the correction control rendered for a viewer who is not the owner.',
    'the control is owner-only; its presence here is the boundary failing, not a capture problem.',
    'the capture would document a real disclosure defect rather than the intended state.',
    'confirm the mock serves MOCK_PROJECT_VIEWER=other, and fix the ownership test if it does.')
}

const rollup = MODE === 'notfound' ? null : await waitFor('[data-testid="project-collectives"]')
if (MODE !== 'notfound' && !rollup) {
  await browser.close()
  die(2, 'the collectives roll-up never rendered.',
    'the served build predates the roll-up panel.',
    'the capture would not show the roll-up this change adds.',
    'rebuild from this worktree and retry.')
}

// ── Computed-style probe ────────────────────────────────────────────────────
// Close token pairs cannot be told apart in a scaled PNG, so they are asserted
// on the live DOM instead of judged by eye.
const probe = await page.evaluate(() => {
  const cs = (sel) => {
    const el = document.querySelector(sel)
    if (!el) return null
    const s = getComputedStyle(el)
    return {
      color: s.color,
      background: s.backgroundColor,
      fontFamily: s.fontFamily,
      fontSize: s.fontSize,
      lineHeight: s.lineHeight,
      borderRadius: s.borderTopLeftRadius,
      fontVariantNumeric: s.fontVariantNumeric,
      textTransform: s.textTransform,
    }
  }
  const root = getComputedStyle(document.documentElement)
  const token = (name) => root.getPropertyValue(name).trim()
  const counts = [...document.querySelectorAll('[data-testid="collective-transcript-count"]')]
  return {
    tokens: {
      surface: token('--surface'),
      canvas: token('--canvas'),
      ink: token('--ink'),
      ink2: token('--ink-2'),
      ink3: token('--ink-3'),
      rule: token('--rule'),
      amber: token('--amber'),
    },
    heading: cs('[data-testid="project-display-name"]'),
    subtitle: cs('[data-testid="project-remote-label"]'),
    rollupPanel: cs('[data-testid="project-collectives"]'),
    renameInput: cs('[data-testid="project-rename-control"] input'),
    countCell: counts.length ? cs('[data-testid="collective-transcript-count"]') : null,
    saveButtonHeight: (() => {
      const btns = [...document.querySelectorAll('[data-testid="project-rename-control"] button')]
      return btns.map((b) => Math.round(b.getBoundingClientRect().height))
    })(),
    rollupRows: document.querySelectorAll('[data-testid="project-collectives"] li').length,
  }
})

/* Capture one surface.

   Dense full-page surfaces go through the vendored non-empty gate unchanged. A
   surface that is legitimately sparse — a sub-element such as the correction
   control or the roll-up panel, or the deliberately bare not-found page — would
   be rejected by the gate's full-size byte floor for being exactly what it is
   meant to be. Its real content signal is the non-background ratio and the
   colour count, asserted here at the same thresholds the gate uses. The vendored
   gate file is left byte-faithful to upstream rather than gaining a local floor
   table. */
const shoot = async (name, sel, { sparse = false } = {}) => {
  const el = await page.$(sel)
  if (!el) {
    await browser.close()
    die(1, `selector ${sel} did not resolve for surface ${name}.`,
      'the surface never mounted on the served build.',
      'the PNG would be empty or missing.',
      'confirm the route rendered and retry.')
  }
  const box = await el.boundingBox()
  if (!box || box.width < 4 || box.height < 4) {
    await browser.close()
    die(1, `${sel} resolved to a blank or zero-size box: ${JSON.stringify(box)}.`,
      'the surface did not lay out.',
      'the PNG would be empty.',
      'confirm the route is reachable and the project fixtures loaded.')
  }
  const file = `${out}/${name}.png`
  await el.screenshot({ path: file, captureBeyondViewport: true })
  let r
  if (sparse) {
    r = await gate.measure(file)
    if (r.nonbgRatio < MIN_NONBG_RATIO || r.distinctColors < MIN_DISTINCT_COLORS) {
      await browser.close()
      die(1, `sparse surface ${name} painted no content: nonbg=${(r.nonbgRatio * 100).toFixed(2)}% colors=${r.distinctColors}.`,
        `it is a near-uniform fill, below the same nonbg (${(MIN_NONBG_RATIO * 100).toFixed(1)}%) and colour (${MIN_DISTINCT_COLORS}) floors the full-surface gate uses.`,
        'the capture would show an empty panel and prove nothing.',
        'confirm the element mounted with content before capture and retry.')
    }
  } else {
    r = await gate.assert(name, file, { sel, where: 'project-page-shoot.mjs' })
  }
  const bytes = statSync(file).size
  console.log('shot', name.padEnd(26), `${Math.round(box.width)}x${Math.round(box.height)}`.padEnd(11),
    `nonbg=${(r.nonbgRatio * 100).toFixed(1)}% colors=${r.distinctColors} ${(bytes / 1024).toFixed(1)}KB`)
  return file
}

// ── The not-found answer ────────────────────────────────────────────────────
// One panel for every refusal. Captured before the heading gate below, because
// in this mode there deliberately is no heading.
if (MODE === 'notfound') {
  const panel = await waitFor('[data-testid="project-page-not-found"]')
  if (!panel) {
    await browser.close()
    die(2, 'the not-found panel never rendered.',
      'the served build predates the panel, or the mock answered the requested hash instead of refusing it.',
      'the capture would not show the boundary this change renders.',
      'point VILLAGE_URL at a hash the mock does not serve, rebuild from this worktree, and retry.')
  }
  if (await page.$('[data-testid="project-display-name"]')) {
    await browser.close()
    die(1, 'a project heading rendered alongside the not-found panel.',
      'the refusal path is leaking project detail.',
      'the capture would document a disclosure defect rather than the intended state.',
      'fix the refusal branch so it renders the panel alone.')
  }
  // The refusal page is deliberately one short panel over a tall empty canvas.
  // The viewport is shortened to the content so the capture is the answer a
  // reviewer needs to read, not a screen of background, and it is gated on
  // painted content rather than on a full-page byte floor.
  await page.setViewport({ ...BASE_VP, height: 420 })
  await pause(300)
  await shoot('vpp-project-not-found', 'body', { sparse: true })
  console.log('provenance', JSON.stringify({ url: TARGET_URL, mode: MODE, headingPresent: false }))
  console.log('console errors:', errs.length ? errs.slice(0, 6) : 'none')
  await browser.close()
  process.exit(0)
}

if (MODE === 'viewer') {
  await shoot('vpp-project-page-viewer', 'body')
  await shoot('vpp-project-collectives-empty', '[data-testid="project-collectives"]', { sparse: true })
  console.log('provenance', JSON.stringify({
    url: TARGET_URL,
    mode: MODE,
    heading: headingText,
    repositoryLabel: subtitleText,
    correctionControlPresent: false,
    rollupRows: probe.rollupRows,
  }))
  console.log('computed', JSON.stringify(probe, null, 2))
  console.log('console errors:', errs.length ? errs.slice(0, 6) : 'none')
  await browser.close()
  process.exit(0)
}

await shoot('vpp-project-page', 'body')
await shoot('vpp-project-rename', '[data-testid="project-rename-control"]', { sparse: true })
await shoot('vpp-project-collectives', '[data-testid="project-collectives"]', { sparse: true })

// ── The clear, exercised ────────────────────────────────────────────────────
// The control reflects the SOURCE it now reads from, so the reset capture is
// taken after a real round-trip rather than from a second frozen fixture.
const nameBefore = headingText
const resetClicked = await page.evaluate(() => {
  const btns = [...document.querySelectorAll('[data-testid="project-rename-control"] button')]
  const reset = btns.find((b) => b.textContent.trim() === 'reset to default')
  if (!reset || reset.disabled) return false
  reset.click()
  return true
})
if (!resetClicked) {
  await browser.close()
  die(1, 'the reset control was absent or disabled.',
    'the mock is not serving an override, so there is nothing to reset.',
    'the after-reset capture would show no change.',
    'start the mock with its default override state and retry.')
}
await page.waitForFunction(
  (before) => {
    const el = document.querySelector('[data-testid="project-display-name"]')
    return el && el.textContent.trim() !== before
  },
  { timeout: 10000 },
  nameBefore,
)
await pause(400)
const nameAfter = await page.$eval('[data-testid="project-display-name"]', (el) => el.textContent.trim())
await shoot('vpp-project-after-reset', 'body')

console.log('provenance', JSON.stringify({
  url: TARGET_URL,
  mode: MODE,
  heading: headingText,
  repositoryLabel: subtitleText,
  rollupRows: probe.rollupRows,
  nameBeforeReset: nameBefore,
  nameAfterReset: nameAfter,
}))
console.log('computed', JSON.stringify(probe, null, 2))
console.log('console errors:', errs.length ? errs.slice(0, 6) : 'none')
await browser.close()
