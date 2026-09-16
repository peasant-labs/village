/* Capture the canonical Fairtrade grouped-helper demonstration — the REFERENCE
   (left) side of the collective grouped side-by-side arms.

   The live in-use demo is the fidelity oracle, and it mounts no collective
   surface that reads the grouped helper pages (its collective browse is a flat
   table, its contribute tree is flat, its review queue is a flat queue). What it
   does mount is the canonical grouped-helper primitive demonstration,
   `?app=commons&helpers=<case>#inuse`, so that demonstration is the reference for
   every arm. Which case and which state each arm pairs against is the ARM table
   in `collective-sxs.mjs` — read its comment before changing anything here.

   Provenance is asserted BEFORE any PNG is written, from the SERVED build:
     - the live DOM must carry the demonstration marker for the arm's case, the
       mounted demo shell, the disclosure control and its counted label;
     - the served JavaScript chunk must contain the demonstration marker and the
       traced-connector class, and that exact asset URL is printed, so a stale
       dist/ or a dev server (unbundled modules) fails instead of producing a
       capture nobody can attribute. Run this against the built demo
       (`vite preview` over `dist/`), never `pnpm dev`.

   Computed styles are read from the live DOM and printed too, because a scaled
   PNG cannot tell two close token values apart: they are the demo-side readings
   the app-side probe is compared against.

   env:
     CHROME_PATH   Chrome/Chromium binary (required)
     DEMO_ORIGIN   served demo origin (default http://localhost:5180)
     PUPPETEER_CORE explicit module path to puppeteer-core (optional)
   usage: CHROME_PATH=... DEMO_ORIGIN=http://127.0.0.1:5190 \
            node scripts/visual/demo-helper-groups-shoot.mjs <theme> <outdir>
*/
import { mkdirSync, statSync } from 'node:fs'
import { createConnection } from 'node:net'
import { COLLECTIVE_ARMS } from './collective-sxs.mjs'
import { SurfaceGate } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const CHROME = process.env.CHROME_PATH
const ORIGIN = (process.env.DEMO_ORIGIN || 'http://localhost:5180').replace(/\/$/, '')
const THEMES = ['dark', 'light']
const theme = process.argv[2] || 'dark'
const out = process.argv[3] || `/tmp/demo-collective-${theme}`
const VIEWPORT = { width: 1460, height: 1000, deviceScaleFactor: 1 }

const die = async (browser, message) => {
  await browser?.close()
  console.error(message)
  process.exit(1)
}

if (!THEMES.includes(theme)) {
  console.error(`ERROR [demo-helper-groups-shoot.mjs] theme "${theme}" is not one of ${THEMES.join(', ')}.`)
  process.exit(1)
}
if (!CHROME) {
  console.error(
    'ERROR [demo-helper-groups-shoot.mjs] CHROME_PATH is unset — set it to your Chrome/Chromium binary.',
  )
  process.exit(1)
}

/* The demo must be SERVED before the browser starts: an empty page looks like a
   successful capture, which would silently corrupt the reference pane. */
const probePort = (host, port) =>
  new Promise((resolve) => {
    const socket = createConnection({ port, host })
    socket.on('connect', () => { socket.destroy(); resolve(true) })
    socket.on('error', () => resolve(false))
  })
const parsed = new URL(ORIGIN)
const port = parsed.port ? Number(parsed.port) : parsed.protocol === 'https:' ? 443 : 80
if (!(await probePort(parsed.hostname, port))) {
  console.error(
    `ERROR [demo-helper-groups-shoot.mjs] nothing is listening at ${ORIGIN}.\n` +
    `  What failed: TCP connect to ${parsed.hostname}:${port} was refused.\n` +
    `  Why: the demo build is not being served (or DEMO_ORIGIN points elsewhere).\n` +
    `  Where: demo-helper-groups-shoot.mjs startup, liveness check.\n` +
    `  Means: every capture would be of an empty page.\n` +
    `  Fix: serve the built demo from the fairtrade checkout, e.g.\n` +
    `    cd <fairtrade checkout> && node_modules/.bin/vite preview --port 5190 --strictPort --host 127.0.0.1\n` +
    `  then re-run with DEMO_ORIGIN=http://127.0.0.1:5190.`,
  )
  process.exit(1)
}

mkdirSync(out, { recursive: true })
const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', defaultViewport: { ...VIEWPORT } })
const page = await browser.newPage()
await applyDeterminism(page)
const errs = []
page.on('console', (m) => { if (m.type() === 'error' && !/favicon/.test(m.text())) errs.push(m.text()) })
page.on('pageerror', (e) => errs.push('pageerr: ' + e.message))

const pause = (ms) => new Promise((r) => setTimeout(r, ms))
const gate = new SurfaceGate(page)

const waitFor = async (sel, timeoutMs = 20000) => {
  const start = Date.now()
  while (Date.now() - start < timeoutMs) {
    const el = await page.$(sel)
    if (el) return el
    await pause(100)
  }
  return null
}

/* Wait on the demonstration's own measure: the connector reports how many mounted
   checkboxes it traced, and that count only settles after the disclosure change. */
const waitForAnchors = (wanted) =>
  page.waitForFunction(
    (target) => [...document.querySelectorAll('.helper-tree-rail')]
      .reduce((total, el) => total + Number(el.dataset.anchorCount), 0) === target,
    { timeout: 10000 },
    wanted,
  )

/* THE SERVED-BUILD PROVENANCE. Both markers exist only in the release that
   rewrote the helper list as a tree, so a stale dist/ or a dev server (which
   serves unbundled modules) fails here rather than producing an unattributable
   capture. The matching asset URL is returned for the run log. */
const servedChunkProvenance = async () => {
  const sources = await page.$$eval('script[src]', (els) => els.map((el) => el.getAttribute('src')).filter(Boolean))
  for (const src of sources) {
    const url = new URL(src, ORIGIN).href
    let body = ''
    try {
      const response = await fetch(url)
      if (!response.ok) continue
      body = await response.text()
    } catch {
      continue
    }
    if (body.includes('data-helper-demo') && body.includes('helper-tree-rail__path')) return url
  }
  return null
}

/* One capture, live-composited: `.iu-stage` is a fixed-height internal scroller,
   so an off-screen raster (`captureBeyondViewport:true`) paints a blank rectangle
   instead of the snap-clipped subtree. Every pixel must therefore be on screen,
   which is asserted rather than assumed. */
const shot = async (name, sel, where) => {
  const el = await waitFor(sel)
  if (!el) {
    await die(browser,
      `ERROR [demo-helper-groups-shoot.mjs] ${where} selector "${sel}" never mounted at ${page.url()}.\n` +
      `  What failed: no element matched "${sel}".\n` +
      `  Why: the demo build predates the grouped-helper demonstration, or the arm's case/state did not apply.\n` +
      `  Where: demo-helper-groups-shoot.mjs shot("${name}").\n` +
      `  Fix: confirm DEMO_ORIGIN serves the built demo, then retry.`)
  }
  const box = await el.boundingBox()
  const viewport = page.viewport()
  if (!box || box.width < 4 || box.height < 4) {
    await die(browser,
      `ERROR [demo-helper-groups-shoot.mjs] ${where} "${sel}" resolved to a blank/zero-size box ${JSON.stringify(box)}.`)
  }
  if (box.y < -0.5 || box.y + box.height > viewport.height + 0.5) {
    await die(browser,
      `ERROR [demo-helper-groups-shoot.mjs] ${where} "${sel}" is not fully on screen (y=${box.y.toFixed(0)}..${(box.y + box.height).toFixed(0)}, viewport ${viewport.height}).\n` +
      `  Why: a live-compositor capture only sees on-screen pixels, so this surface would be clipped.\n` +
      `  Fix: raise the launch viewport height in demo-helper-groups-shoot.mjs, or settle the page before capture.`)
  }
  const file = `${out}/${name}`
  await el.screenshot({ path: file, captureBeyondViewport: false })
  const measured = await gate.assert(name, file, { sel, where: 'demo-helper-groups-shoot.mjs' })
  console.log('shot', name.padEnd(40), `${Math.round(box.width)}x${Math.round(box.height)}`.padEnd(11),
    `nonbg=${(measured.nonbgRatio * 100).toFixed(2)}% colors=${measured.distinctColors} ${(statSync(file).size / 1024).toFixed(1)}KB`)
  return measured
}

/* The demo-side design-system readings the app-side probe is compared against. */
const COMPUTED_PROBE = {
  trigger: ['.helper-group-trigger', ['fontFamily', 'fontSize', 'borderRadius', 'minHeight', 'textTransform', 'color']],
  count: ['.helper-group-count', ['fontFamily', 'fontSize', 'fontVariantNumeric', 'textTransform', 'borderRadius']],
  facts: ['.helper-thread-facts', ['fontFamily', 'fontSize', 'fontVariantNumeric']],
  title: ['.helper-thread-open, .helper-thread-title', ['fontFamily', 'fontSize', 'textTransform', 'whiteSpace', 'textOverflow']],
  rail: ['.helper-tree-rail__path', ['stroke', 'strokeWidth']],
}
const computedProbe = () =>
  page.evaluate((probe) => {
    const read = (sel, props) => {
      const el = document.querySelector(sel)
      if (!el) return null
      const style = getComputedStyle(el)
      return Object.fromEntries(props.map((p) => [p, style[p]]))
    }
    return Object.fromEntries(Object.entries(probe).map(([key, [sel, props]]) => [key, read(sel, props)]))
  }, COMPUTED_PROBE)

const provenance = []
for (const arm of COLLECTIVE_ARMS) {
  const url = `${ORIGIN}/?app=commons&helpers=${encodeURIComponent(arm.demoCase)}${theme === 'light' ? '&theme=light' : ''}#inuse`
  await page.goto(url, { waitUntil: 'networkidle2' })
  await page.evaluate(() => document.getElementById('inuse-stage')?.scrollIntoView({ block: 'center' }))
  await page.evaluate(() => document.fonts.ready)
  await pause(400)

  const demo = await waitFor(`[data-helper-demo="${arm.demoCase}"]`)
  if (!demo) {
    await die(browser,
      `ERROR [demo-helper-groups-shoot.mjs] the demo mounts no "${arm.demoCase}" demonstration at ${url}.\n` +
      `  What failed: no [data-helper-demo="${arm.demoCase}"] appeared within 20s.\n` +
      `  Why: the demo case was renamed or removed, or this is not the built demo.\n` +
      `  Where: demo-helper-groups-shoot.mjs arm "${arm.surface}" readiness wait.\n` +
      `  Means: the reference pane for this arm cannot be attributed to any canonical demonstration.\n` +
      `  Fix: point the arm's demoCase in collective-sxs.mjs at a case the served demo still mounts.`)
  }
  const shell = await page.$eval('.iu-subnav', (el) => el.textContent).catch(() => '')
  if (!shell.includes('explore')) {
    await die(browser,
      `ERROR [demo-helper-groups-shoot.mjs] the in-use demo shell did not mount for arm "${arm.surface}" (url ${url}).`)
  }
  const actualTheme = await page.evaluate(() => document.documentElement.getAttribute('data-theme'))
  if ((theme === 'light') !== (actualTheme === 'light')) {
    await die(browser,
      `ERROR [demo-helper-groups-shoot.mjs] the requested theme did not apply for arm "${arm.surface}".\n` +
      `  What failed: [data-theme]="${actualTheme}" after requesting "${theme}".\n` +
      `  Why: the demo reads the theme from ?theme=light; a dark reference paired with a light app (or vice versa) is not comparable.\n` +
      `  Where: demo-helper-groups-shoot.mjs theme preflight.\n` +
      `  Fix: confirm the demo build applies ?theme=light, then retry.`)
  }

  // Normalize the disclosure state first: `page.goto` to the same URL (as two
  // arms sharing one case + theme do) is a same-document no-op in Chrome, so an
  // arm could otherwise inherit the previous arm's open group and collapse it
  // instead of opening it.
  await page.evaluate(() => {
    for (const trigger of document.querySelectorAll('.helper-group-trigger')) {
      if (trigger.getAttribute('aria-expanded') === 'true') trigger.click()
    }
  })
  await pause(200)
  const controls = await page.$$('.helper-group-trigger')
  if (controls.length < 1) {
    await die(browser,
      `ERROR [demo-helper-groups-shoot.mjs] the "${arm.demoCase}" demonstration mounted no disclosure control.`)
  }
  for (const control of controls) await control.click()
  await page.waitForSelector('.helper-group-members .helper-thread-row', { timeout: 10000 }).catch(async () => {
    await die(browser,
      `ERROR [demo-helper-groups-shoot.mjs] opening the "${arm.demoCase}" disclosure revealed no member row.\n` +
      `  What failed: no ".helper-group-members .helper-thread-row" appeared after the control was pressed.\n` +
      `  Why: the disclosure did not open, or the case mounts no members.\n` +
      `  Where: demo-helper-groups-shoot.mjs arm "${arm.surface}" expansion wait.\n` +
      `  Fix: confirm the served demo mounts this case's members, then retry.`)
  })
  if (arm.demoState === 'selected') {
    const thread = await page.$(`.helper-group-members [data-thread-id="${arm.demoSelectThread}"] input[type="checkbox"]`)
    if (!thread) {
      await die(browser,
        `ERROR [demo-helper-groups-shoot.mjs] arm "${arm.surface}" cannot tick member "${arm.demoSelectThread}".\n` +
        `  What failed: no checkbox for [data-thread-id="${arm.demoSelectThread}"] in the open "${arm.demoCase}" group.\n` +
        `  Fix: update demoSelectThread in collective-sxs.mjs to a member this case mounts.`)
    }
    await thread.click()
    await page.waitForFunction(
      (id) => document.querySelector(`.helper-group-members [data-thread-id="${id}"] input[type="checkbox"]`)?.checked === true,
      { timeout: 10000 },
      arm.demoSelectThread,
    )
  }
  const label = await page.$eval('.helper-group-count', (el) => el.textContent.trim())
  await waitForAnchors(await page.$$eval('.helper-tree input[type="checkbox"]', (els) => els.length))

  const chunk = await servedChunkProvenance()
  if (!chunk) {
    await die(browser,
      `ERROR [demo-helper-groups-shoot.mjs] the served page carries no attributable bundle for arm "${arm.surface}".\n` +
      `  What failed: no <script src> body contained both "data-helper-demo" and "helper-tree-rail__path".\n` +
      `  Why: DEMO_ORIGIN is serving the unbundled dev server (or a stale dist with no helper demo).\n` +
      `  Where: demo-helper-groups-shoot.mjs served-build provenance check at ${ORIGIN}.\n` +
      `  Means: the reference pane could not be tied to a build someone can inspect.\n` +
      `  Fix: serve the built demo (vite preview over dist/) and re-run.`)
  }

  await shot(arm.demo, '#inuse', `arm "${arm.surface}"`)
  await shot(arm.demoGrouped, '.helper-demo', `arm "${arm.surface}" grouped region`)
  const styles = await computedProbe()
  await waitForAnchors(await page.$$eval('.helper-tree input[type="checkbox"]', (els) => els.length))

  provenance.push({ arm: arm.surface, case: arm.demoCase, state: arm.demoState, theme, label, chunk, styles })
  console.log('arm', arm.surface.padEnd(34), `case=${arm.demoCase}`, `state=${arm.demoState}`, `label="${label}"`)
}

console.log(`\nserved-build provenance (${theme}):`)
for (const entry of provenance) console.log(`  ${entry.arm}: ${entry.chunk}`)
console.log('demo computed styles:', JSON.stringify(Object.fromEntries(provenance.map((p) => [p.arm, p.styles])), null, 2))
console.log(`PROVENANCE_JSON=${JSON.stringify(provenance)}`)
console.log('console errors:', errs.length ? errs.slice(0, 6) : 'none')
await browser.close()
