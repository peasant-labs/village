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
     profile the PROFILE page, whose project cards link into the project page.
            Point VILLAGE_URL at /users/{username}. The card heading renders a
            project's display name, which is USER CONTENT, so the capture is
            gated on the name surviving as typed: the served name must carry a
            capital and the heading's computed text-transform must be `none`.
            The design system lowercases h1/h2/h3 as chrome, so a lowercase-only
            fixture could not tell a fixed page from a broken one.
     viewer a viewer who is NOT the owner. The correction control must be ABSENT
            and the roll-up is expected empty, which is the ORDINARY answer once
            collective visibility and the contributor opt-in have applied. Pair
            it with MOCK_PROJECT_VIEWER=other MOCK_PROJECT_ROLLUP=empty on the mock.

   env:
     VILLAGE_URL     page URL (required; the hash-keyed project route, or the
                     profile route /users/{username} in profile mode)
     CHROME_PATH     Chrome/Chromium binary (required)
     PUPPETEER_CORE  explicit module path to puppeteer-core (optional)
     NARROW          when "1", captures at a ~390px mobile viewport instead of
                      the default desktop one — nobody had looked at this page
                      below ~1040px before this flag existed.
   usage: VILLAGE_URL=... CHROME_PATH=... node project-page-shoot.mjs <theme> <outdir>
          VILLAGE_URL=... CHROME_PATH=... NARROW=1 node project-page-shoot.mjs <theme> <outdir>
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

const NARROW = process.env.NARROW === '1'
const BASE_VP = NARROW
  ? { width: 390, height: 1600, deviceScaleFactor: 1 }
  : { width: 1396, height: 1200, deviceScaleFactor: 1 }

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
const targetPath = new URL(TARGET_URL).pathname
if (MODE === 'profile') {
  if (!/^\/users\/[^/]+\/?$/.test(targetPath)) {
    die(1, `VILLAGE_URL path ${JSON.stringify(targetPath)} is not the profile route.`,
      'the profile mode captures /users/{username}, the surface whose project cards link into the project page.',
      'the capture would prove nothing about the profile card this change changes.',
      'point VILLAGE_URL at /users/{username} on the running app.')
  }
} else if (!/\/users\/[^/]+\/projects\/[0-9a-f]{64}\/?$/.test(targetPath)) {
  die(1, `VILLAGE_URL path ${JSON.stringify(targetPath)} is not the hash-keyed project route.`,
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
// RailShell renders its `rail` content TWICE — once into the desktop aside
// (display:none below 880px) and once into the mobile bottom sheet — so a
// selector inside the rail (only `project-rename-control` here) can match a
// hidden copy first. This picks the one actually laid out, needed only in
// NARROW mode; the plain single-copy `waitFor`/`page.$` above stay correct
// everywhere else (including `project-collectives`, which is NOT rail
// content and is never duplicated).
const waitForVisible = async (sel, timeoutMs = 12000) => {
  const start = Date.now()
  while (Date.now() - start < timeoutMs) {
    const handles = await page.$$(sel)
    for (const h of handles) {
      const box = await h.boundingBox()
      if (box && box.width > 0 && box.height > 0) return h
    }
    await pause(100)
  }
  return null
}

// ── Build provenance ────────────────────────────────────────────────────────
// Each marker below exists ONLY in this change. A served build that lacks any
// of them is not the build under test, so nothing is captured.
// The profile and not-found modes capture surfaces that carry no project
// header, so the header markers below are asserted only for the project page.
const isProjectSurface = MODE !== 'notfound' && MODE !== 'profile'
const heading = isProjectSurface ? await waitFor('[data-testid="project-display-name"]') : null
if (isProjectSurface && !heading) {
  await browser.close()
  die(2, 'the project heading never rendered.',
    'the served build predates the project page, or the page fetch failed.',
    'the capture would be blank or a not-found panel.',
    'rebuild and restart the app from THIS worktree against the project mock, and retry.')
}
const headingText = heading ? await page.evaluate((el) => el.textContent.trim(), heading) : ''

const subtitleEl = isProjectSurface ? await page.$('[data-testid="project-remote-label"]') : null
if (isProjectSurface && !subtitleEl) {
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

// Fairtrade's RailShell collapses the rail (the correction control, project
// settings) into a fixed bottom sheet below 880px — a deliberate, working
// responsive behavior, not a defect (see RailShell's own doc comment). Below
// that breakpoint the rail's content is not in the accessibility tree or the
// layout until its toggle (aria-expanded, labelled by `sheetTitle`) is
// opened, so a narrow capture has to drive that interaction itself before
// any rail-only selector (the rename control, the collectives roll-up) can
// resolve — otherwise every narrow owner/viewer capture would spuriously
// report the control missing.
if (NARROW && isProjectSurface) {
  const sheetToggle = await page.$('button[aria-expanded="false"][aria-controls^="rs-sheet-"]')
  if (sheetToggle) {
    await sheetToggle.click()
    await pause(300)
  }
}

const control = NARROW
  ? await waitForVisible('[data-testid="project-rename-control"]')
  : (MODE === 'owner' ? await waitFor('[data-testid="project-rename-control"]') : await page.$('[data-testid="project-rename-control"]'))
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

const rollup = isProjectSurface ? await waitFor('[data-testid="project-collectives"]') : null
if (isProjectSurface && !rollup) {
  await browser.close()
  die(2, 'the collectives roll-up never rendered.',
    'the served build predates the roll-up panel.',
    'the capture would not show the roll-up this change adds.',
    'rebuild from this worktree and retry.')
}

// ── Computed-style probe ────────────────────────────────────────────────────
// Close token pairs cannot be told apart in a scaled PNG, so they are asserted
// on the live DOM instead of judged by eye.
const probe = await page.evaluate((narrow) => {
  // RailShell renders `rail` content twice (a hidden desktop aside + the
  // mobile sheet) below 880px, so a bare querySelector on rename-control
  // content can hit the hidden copy in narrow mode — pick the laid-out one.
  const cs = (sel) => {
    const candidates = narrow ? [...document.querySelectorAll(sel)] : [document.querySelector(sel)]
    const el = narrow
      ? candidates.find((c) => c && c.getClientRects().length > 0)
      : candidates[0]
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
      const controls = narrow
        ? [...document.querySelectorAll('[data-testid="project-rename-control"]')].filter((c) => c.getClientRects().length > 0)
        : [...document.querySelectorAll('[data-testid="project-rename-control"]')].slice(0, 1)
      const btns = controls.flatMap((c) => [...c.querySelectorAll('button')])
      return btns.map((b) => Math.round(b.getBoundingClientRect().height))
    })(),
    rollupRows: document.querySelectorAll('[data-testid="project-collectives"] li').length,
  }
}, NARROW)

/* Capture one surface.

   Dense full-page surfaces go through the vendored non-empty gate unchanged. A
   surface that is legitimately sparse — a sub-element such as the correction
   control or the roll-up panel, or the deliberately bare not-found page — would
   be rejected by the gate's full-size byte floor for being exactly what it is
   meant to be. Its real content signal is the non-background ratio and the
   colour count, asserted here at the same thresholds the gate uses. The vendored
   gate file is left byte-faithful to upstream rather than gaining a local floor
   table. */
const shoot = async (rawName, selOrHandle, { sparse = false } = {}) => {
  // Auto-suffixed rather than left to the caller so a narrow run can never
  // silently overwrite the desktop capture it shares an outdir with.
  const name = NARROW ? `${rawName}-narrow` : rawName
  // Accepts an already-resolved element handle (used for rail content in
  // NARROW mode, where a bare selector could resolve RailShell's hidden
  // desktop copy — see waitForVisible above) as well as a plain selector.
  const sel = typeof selOrHandle === 'string' ? selOrHandle : '(resolved element)'
  const el = typeof selOrHandle === 'string' ? await page.$(selOrHandle) : selOrHandle
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

// ── The profile page ────────────────────────────────────────────────────────
// The card heading is USER CONTENT rendered in an h2, and the design system
// lowercases h1/h2/h3 as chrome. The capture is refused unless the served name
// carries a capital, because a lowercase-only name cannot show the difference
// between a page that overrides the rule and one that does not.
if (MODE === 'profile') {
  const card = await waitFor('a[href*="/projects/"] h2')
  if (!card) {
    await browser.close()
    die(2, 'no project card heading rendered on the profile page.',
      'the served build predates the card link into the project page, or the transcript list was empty.',
      'the capture would not show the heading this change fixes.',
      'rebuild from this worktree against the project mock, and retry.')
  }
  const cardProbe = await page.evaluate(() => {
    const link = document.querySelector('a[href*="/projects/"]')
    const h = link ? link.querySelector('h2') : null
    if (!h) return null
    const s = getComputedStyle(h)
    return {
      href: link.getAttribute('href'),
      rendered: h.textContent.trim(),
      textTransform: s.textTransform,
      fontFamily: s.fontFamily,
      fontSize: s.fontSize,
      profileHeading: (() => {
        const h1 = document.querySelector('h1')
        if (!h1) return null
        const cs = getComputedStyle(h1)
        return { rendered: h1.textContent.trim(), textTransform: cs.textTransform }
      })(),
    }
  })
  if (!/[A-Z]/.test(cardProbe.rendered)) {
    await browser.close()
    die(1, `the project card heading reads ${JSON.stringify(cardProbe.rendered)} and carries no capital.`,
      'the fixture served an all-lowercase project name, so the capture cannot distinguish a heading that preserves user content from one the design system lowercased.',
      'the capture would prove nothing about the casing this change fixes.',
      'serve a project display name containing capitals from the mock and retry.')
  }
  if (cardProbe.textTransform !== 'none') {
    await browser.close()
    die(1, `the project card heading computes text-transform: ${cardProbe.textTransform}.`,
      "the design system lowercases h1/h2/h3 as UI chrome and this heading renders a project's display name, which is user content.",
      "the capture would document a page that silently rewrites a person's project name.",
      'add `normal-case` to the heading and retry.')
  }
  if (cardProbe.profileHeading && cardProbe.profileHeading.textTransform !== 'none') {
    await browser.close()
    die(1, `the profile display-name heading computes text-transform: ${cardProbe.profileHeading.textTransform}.`,
      "a person's display name is user content and the design system lowercases h1 as chrome.",
      'the capture would document a page that silently rewrites a person\'s name.',
      'add `normal-case` to the profile h1 and retry.')
  }
  await shoot('vpp-profile-projects', 'body')
  console.log('provenance', JSON.stringify({ url: TARGET_URL, mode: MODE, card: cardProbe }))
  console.log('console errors:', errs.length ? errs.slice(0, 6) : 'none')
  await browser.close()
  process.exit(0)
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
// `control` was already resolved to the LAID-OUT copy above (waitForVisible
// in NARROW mode); reuse the handle instead of re-querying by selector so
// this capture cannot silently grab RailShell's hidden desktop copy.
await shoot('vpp-project-rename', control, { sparse: true })
await shoot('vpp-project-collectives', '[data-testid="project-collectives"]', { sparse: true })

// ── The clear, exercised ────────────────────────────────────────────────────
// The control reflects the SOURCE it now reads from, so the reset capture is
// taken after a real round-trip rather than from a second frozen fixture.
const nameBefore = headingText
const resetClicked = await page.evaluate((narrow) => {
  const controls = narrow
    ? [...document.querySelectorAll('[data-testid="project-rename-control"]')].filter((c) => c.getClientRects().length > 0)
    : [...document.querySelectorAll('[data-testid="project-rename-control"]')].slice(0, 1)
  const btns = controls.flatMap((c) => [...c.querySelectorAll('button')])
  const reset = btns.find((b) => b.textContent.trim() === 'reset to default')
  if (!reset || reset.disabled) return false
  reset.click()
  return true
}, NARROW)
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
