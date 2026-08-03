/* Screenshot the REAL assembled village frontend transcript view (the dev visual-harness route):
     wire SessionDetailPayload -> the shared @peasant-labs/transcript-browser <SessionDetail> composer,
     with the package's @xyflow TrajectoryGraph plugged into its graph slot.

   Mirrors the surface set of the canonical fairtrade demo (scripts/shootdemo.mjs) so the shots pair
   1:1 with the DEMO captures (the output filenames are the canonical `txn-*` surface names, even though
   the village composer renders them with `.tb-*` classes — the harness compares the same SURFACES, not
   the same class strings). The harness route bundles the SAME session the demo renders (sess_demo_0001)
   and mounts the composer with no backend/auth, so a plain `next dev` is enough.

   Scroll model: the village `<SessionDetail>` composer scrolls the PAGE and reveals a sticky condensed
   header (`.tb-stickyhead`) on scroll — it is NOT a height-bounded inner-scroller. The whole transcript
   is in normal document flow, so a full-surface shot captures the target element's entire bounds with
   captureBeyondViewport:true at the base viewport (no viewport-grow — several surfaces are
   viewport-relative, so growing the viewport would just grow the content); the sticky scrubber is
   captured by scrolling the window to reveal it. Theme is toggled via the harness route's own
   .theme-btn (which sets [data-theme] on the document element, the way the real app's theme control
   does), not a URL param.

   Every successful capture is run through the non-empty-surface gate (SurfaceGate.assert) BEFORE it is
   accepted, so a surface that paints blank (e.g. an empty graph) fails LOUD and is recorded as a gap
   instead of silently passing a vacuous side-by-side. Each surface is wrapped so one failure records a
   gap and the run continues — maximising artifacts + an honest gap list for the manifest.

   env:
     VILLAGE_URL     dev-server URL of the harness route   (default http://localhost:3000/dev/visual-harness)
     CHROME_PATH     Chrome/Chromium binary puppeteer drives    (required)
     PUPPETEER_CORE  explicit module path to puppeteer-core     (optional; only if a bare import won't resolve)
   usage: VILLAGE_URL=http://localhost:3000/dev/visual-harness CHROME_PATH=/path/to/chrome node village-shoot.mjs <theme> <outdir>
*/
import { mkdirSync, statSync } from 'node:fs'
import { SurfaceGate } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const CHROME = process.env.CHROME_PATH
const URL = process.env.VILLAGE_URL || 'http://localhost:3000/dev/visual-harness'
const theme = process.argv[2] || 'dark'
const out = process.argv[3] || `/tmp/village-${theme}`
mkdirSync(out, { recursive: true })

const BASE_VP = { width: 1460, height: 1000, deviceScaleFactor: 1 }
const CAP = 8000

const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', defaultViewport: { ...BASE_VP } })
const page = await browser.newPage()
await applyDeterminism(page) // reduced-motion + frozen clock/PRNG (set BEFORE goto) so each capture is deterministic for imgdiff
const errs = []
page.on('console', (m) => { if (m.type() === 'error' && !/favicon|404|hydrat/.test(m.text())) errs.push(m.text()) })
page.on('pageerror', (e) => errs.push('pageerr: ' + e.message))
await page.goto(URL, { waitUntil: 'networkidle0' })
await new Promise((r) => setTimeout(r, 900))

const pause = (ms) => new Promise((r) => setTimeout(r, ms))
const results = [] // { name, status, info }
const gate = new SurfaceGate(page) // non-empty-surface assertion, per run (tracks duplicates too)

/* TWO-TIER FAILURE CONTRACT
   - STRUCTURAL gates (the run cannot produce valid output) → HARD non-zero exit, ABORT immediately,
     with a DISTINCT code so "harness broke" is separable from "a surface regressed":
       * code 3 — theme-didn't-flip       (every capture would be the wrong theme)
       * code 4 — composite-not-rendered   (`.tb-detail` never mounted)
       * code 5 — page-not-scrolling       (`document.scrollHeight <= innerHeight`; the page-scroll
                  analog of "the internal stream never overflows" — nothing to capture)
   - PER-SURFACE issues → RECORDED + the run CONTINUES, then exits non-zero with code 1 (so CI flags it,
     distinct from the structural 3/4/5), or 0 if there are none:
       * GAP     — a surface failed (selector never mounted, a popover didn't open, a single
                   blank/duplicate SurfaceGate rejection); a placeholder is drawn in the side-by-side.
       * partial — a surface taller than the CAP raster ceiling; captured IN FULL (never silently
                   clipped or dropped) and flagged for review. */
const die = async (code, what) => {
  console.error(`\nSTRUCTURAL FAILURE [village-shoot.mjs] — ${what}\n  the run is aborted; the captures would be invalid. Exiting ${code}.`)
  try { await browser.close() } catch { /* already closing */ }
  process.exit(code)
}

/* ── wait for the composer to MOUNT + hydrate before driving it. This also serves the structural
   "composite-not-rendered" gate (die 4): a cold dev compile can take seconds, and the theme control
   must be hydrated (its onClick attached) before a click does anything. ── */
{
  let ready = false
  const s = Date.now()
  while (Date.now() - s < 12000) { if ((await page.$('.tb-detail')) && (await page.$('.theme-btn'))) { ready = true; break } await pause(150) }
  if (!ready) await die(4, 'composite-not-rendered: ".tb-detail" / ".theme-btn" never mounted on the harness route — the <SessionDetail> composer did not render the fixture')
  await pause(300) // let hydration settle so the theme control's handler is attached
}

/* ── theme: default is dark; for light, click .theme-btn then poll [data-theme]. Re-read BEFORE each
   click (so a click that hasn't registered yet — cold hydration — is retried without ever
   double-toggling past the target), and die(3) only after the retries are exhausted. ── */
const readTheme = () => page.evaluate(() => document.querySelector('[data-theme]')?.getAttribute('data-theme'))
let activeTheme = await readTheme()
if (theme === 'light') {
  for (let attempt = 0; attempt < 6; attempt++) {
    activeTheme = await readTheme()
    if (activeTheme === 'light') break
    await page.evaluate(() => document.querySelector('.theme-btn')?.click())
    const s = Date.now()
    while (Date.now() - s < 1200) { activeTheme = await readTheme(); if (activeTheme === 'light') break; await pause(120) }
  }
}
activeTheme = await readTheme()
if (activeTheme !== theme) {
  await die(3, `theme-didn't-flip: requested theme="${theme}" but [data-theme]="${activeTheme}" after clicking .theme-btn`)
}

/* ── structural: the composer must have rendered a full, page-overflowing transcript. The village
   <SessionDetail> composer scrolls the PAGE (no internal stream), so the analog of the reference
   harness's "stream not scrolling internally" gate is "the document overflows the viewport". ── */
{
  const st = await page.evaluate(() => ({ scrollHeight: document.documentElement.scrollHeight, innerHeight: window.innerHeight }))
  if (st.scrollHeight <= st.innerHeight) await die(5, `page-not-scrolling: document.scrollHeight (${st.scrollHeight}) <= innerHeight (${st.innerHeight}) — the transcript did not render a full, overflowing page (the page-scroll analog of an internal stream that never overflows)`)
}

/* ── nav helpers (the village composer's tab strip + segmented list/graph toggle + tool switch) ── */
const waitFor = async (sel, timeoutMs = 3000) => {
  const start = Date.now()
  while (Date.now() - start < timeoutMs) { const el = await page.$(sel); if (el) return el; await pause(80) }
  throw new Error(`selector "${sel}" never mounted (${timeoutMs}ms)`)
}
const resetView = async () => {
  await page.setViewport({ ...BASE_VP })
  await page.evaluate(() => window.scrollTo(0, 0))
  await pause(150)
}
const txnTab = async (label) => {
  const ok = await page.evaluate((label) => {
    const b = [...document.querySelectorAll('.txn-tab, .tb-tabstrip-tab')].find((x) => x.textContent.toLowerCase().includes(label))
    if (!b) return false; b.click(); return true
  }, label)
  if (!ok) throw new Error(`tab "${label}" not found`)
  await pause(450)
}
const viewMode = async (mode) => {
  const ok = await page.evaluate((mode) => {
    const b = [...document.querySelectorAll('.tb-segmented-btn')].find((x) => x.textContent.toLowerCase().includes(mode))
    if (!b) return false; b.click(); return true
  }, mode)
  if (!ok) throw new Error(`view-mode "${mode}" not found`)
  await pause(550)
}
// Best-effort: expanding tool calls enriches the trace shot but is not essential; a miss warns, never aborts.
const expandAllTools = async () => {
  const ok = await page.evaluate(() => {
    const sw = [...document.querySelectorAll('.txn-viewsw')].find((x) => /expand all tool calls/i.test(x.textContent))
    const btn = sw?.querySelector('button[role="switch"], button') || document.querySelector('button[aria-label="expand all tool calls"]')
    if (!btn) return false; btn.click(); return true
  })
  if (!ok) console.error('note: "expand all tool calls" switch not found — trace captured with tools collapsed')
  await pause(500)
}

/* capture the FULL target element. The village composer lays the whole transcript out in normal
   document flow (page-scrolled, no virtualized inner-scroller), so every surface is fully in the DOM at
   the base viewport — captureBeyondViewport:true rasters the element's entire bounds (incl. below the
   fold) in one frame WITHOUT growing the viewport. (Growing the viewport is wrong here: several surfaces
   are viewport-relative — the rail/graph fill the viewport height — so a taller viewport just grows the
   content, never catching up.) Asserts the capture is non-empty before accepting it; warns if a surface
   exceeds the CAP-px raster guard. */
const shotFull = async (name, sel) => {
  await waitFor(sel)
  await resetView()
  const el = await page.$(sel)
  const box = await el.boundingBox()
  if (!box || box.width < 4 || box.height < 4) throw new Error(`"${sel}" blank/zero-size: ${JSON.stringify(box)}`)
  // Over the raster ceiling → record as "partial" (never silently clip/drop). captureBeyondViewport
  // still rasters the element IN FULL; we flag it so a deep transcript is visibly surfaced for review.
  const overCap = box.height > CAP
  await el.screenshot({ path: `${out}/${name}.png`, captureBeyondViewport: true })
  await gate.assert(name, `${out}/${name}.png`, { sel, where: 'village-shoot.mjs' })
  const bytes = statSync(`${out}/${name}.png`).size
  const dims = `${Math.round(box.width)}x${Math.round(box.height)}`
  if (overCap) {
    results.push({ name, status: 'partial', info: `${dims} ${(bytes / 1024).toFixed(1)}KB — exceeds ${CAP}px ceiling; captured in full, flagged for review` })
    console.error(`PARTIAL ${name.padEnd(22)} ${dims} > ${CAP}px ceiling — captured in full (not clipped), flagged`)
  } else {
    results.push({ name, status: 'ok', info: `${dims} ${(bytes / 1024).toFixed(1)}KB` })
    console.log('shot', name.padEnd(22), `${dims}`.padEnd(11), `${(bytes / 1024).toFixed(1)}KB`)
  }
}

/* capture an element in place (no scroll/viewport reset) — used for transient on-screen overlays (the
   revealed sticky condensed header, the label popover) where the caller has already set up the view.
   Asserts non-empty before accepting. */
const shotFold = async (name, sel) => {
  const el = await waitFor(sel)
  const box = await el.boundingBox()
  if (!box || box.width < 4 || box.height < 4) throw new Error(`"${sel}" blank/zero-size: ${JSON.stringify(box)}`)
  await el.screenshot({ path: `${out}/${name}.png`, captureBeyondViewport: true })
  await gate.assert(name, `${out}/${name}.png`, { sel, where: 'village-shoot.mjs' })
  const bytes = statSync(`${out}/${name}.png`).size
  results.push({ name, status: 'ok', info: `${Math.round(box.width)}x${Math.round(box.height)} ${(bytes / 1024).toFixed(1)}KB` })
  console.log('shot', name.padEnd(22), `${Math.round(box.width)}x${Math.round(box.height)}`.padEnd(11), `${(bytes / 1024).toFixed(1)}KB`)
}

const surface = async (name, fn) => {
  try { await fn() } catch (e) {
    results.push({ name, status: 'GAP', info: e.message })
    console.error('GAP ', name.padEnd(22), e.message)
  }
}

/* ── deep walk: every tab + sub-surface, mirroring the demo's surface set ── */

// highlights tab — carries the scorecard + the highlights outline
await surface('txn-highlights', async () => { await resetView(); await txnTab('highlights'); await shotFull('txn-highlights', '.tb-detail') })
await surface('txn-scorecard', async () => { await shotFull('txn-scorecard', '.tb-scorecard') })

// full trace — list canvas: subagent nesting + thinking + per-kind tool renderers + markers.
// shotFull grows the viewport to the whole document so the entire trace column rasters in one frame,
// pairing with the demo's full-stream shot.
await surface('txn-trace-canvas', async () => {
  await txnTab('full trace'); await viewMode('list'); await expandAllTools()
  await shotFull('txn-trace-canvas', '.tb-canvas')
})

// scrubber — the scrub tick bar lives in the sticky condensed header, which reveals once the page
// scrolls past the hero/tabs. Capture the scrub bar itself (pairs 1:1 with the demo's scrubber);
// records an actionable gap if it never reveals.
await surface('txn-scrubber', async () => {
  await resetView()
  await txnTab('full trace'); await pause(300)
  await page.evaluate(() => window.scrollTo(0, 900)); await pause(600)
  const scrub = (await page.$('.tb-scrubber')) || (await page.$('.tb-stickyhead-scrubber'))
  if (!scrub) {
    await page.evaluate(() => window.scrollTo(0, 0))
    throw new Error('.tb-scrubber did not reveal after scrolling the window — the scrub bar lives in the sticky condensed header, which appears once the page scrolls past the hero/tabs; verify the page actually overflows the viewport (document.scrollHeight > innerHeight)')
  }
  const sel = (await page.$('.tb-scrubber')) ? '.tb-scrubber' : '.tb-stickyhead-scrubber'
  await shotFold('txn-scrubber', sel)
  await page.evaluate(() => window.scrollTo(0, 0)); await pause(200)
})

// rails — the right filters/outline rail alongside the trace
await surface('txn-rails', async () => {
  await txnTab('full trace'); await resetView()
  await shotFull('txn-rails', '.tb-detail-railwrap')
})

// per-turn label popover overlay — village mounts its own label control into the composer's
// renderTurnActions slot (the harness wires the real one). Click a turn's label trigger, then capture
// the opened popover dialog.
await surface('txn-label-popover', async () => {
  await resetView()
  await txnTab('full trace'); await pause(300)
  const opened = await page.evaluate(() => {
    const b = document.querySelector('button[aria-label="Add label"]')
      || document.querySelector('button[aria-label="Label this turn"]')
      || document.querySelector('.txn-labelbtn')
      || document.querySelector('button[aria-label*="label" i]')
    if (!b) return false
    b.scrollIntoView({ block: 'center' }); b.click(); return true
  })
  if (!opened) throw new Error('no per-turn label trigger found (button[aria-label="Add label"] / "Label this turn" / .txn-labelbtn)')
  await pause(300)
  const popSel = (await page.$('.pop-card[role="dialog"]')) ? '.pop-card[role="dialog"]'
    : ((await page.$('.txn-label-pop')) ? '.txn-label-pop'
      : ((await page.$('[role="dialog"]')) ? '[role="dialog"]' : null))
  if (!popSel) throw new Error('label trigger clicked but no popover (.pop-card[role=dialog] / .txn-label-pop / [role=dialog]) mounted')
  await shotFold('txn-label-popover', popSel)
  await page.keyboard.press('Escape'); await pause(250)
})

// trajectory graph view-mode (the package's @xyflow engine in the composite's graph slot)
await surface('txn-graph', async () => {
  await txnTab('full trace'); await resetView()
  await viewMode('graph'); await pause(600)
  await shotFull('txn-graph', '.tb-detail-graphwrap')
  await viewMode('list')
})

// remaining tabs
await surface('txn-diffs', async () => { await resetView(); await txnTab('diffs'); await shotFull('txn-diffs', '.tb-detail') })
await surface('txn-files', async () => { await resetView(); await txnTab('files'); await shotFull('txn-files', '.tb-detail') })
await surface('txn-annotations', async () => { await resetView(); await txnTab('annotations'); await shotFull('txn-annotations', '.tb-detail') })

const okCount = results.filter((r) => r.status === 'ok').length
const partialCount = results.filter((r) => r.status === 'partial').length
const gapCount = results.filter((r) => r.status === 'GAP').length
console.log(`\nTHEME=${theme} active=[data-theme]=${activeTheme}`)
console.log('captured:', okCount + partialCount, `(partial: ${partialCount})`, 'gaps:', gapCount)
console.log('console errors:', errs.length ? errs.slice(0, 6) : 'none')
// emit a machine-readable summary for the manifest builder
console.log('RESULTS_JSON=' + JSON.stringify(results))
await browser.close()
// The structural gates (theme-didn't-flip / composite-not-rendered / page-not-scrolling) have already
// aborted via die(3/4/5) if they tripped. Here only PER-SURFACE outcomes remain: any recorded gap or
// over-cap partial exits 1 so CI flags it (distinct from the structural 3/4/5); a clean run exits 0.
if (gapCount + partialCount > 0) {
  console.error(`\nEXIT 1: ${gapCount} recorded gap(s) + ${partialCount} over-cap partial(s) — surfaces to inspect (distinct from a structural 3/4/5 abort).`)
  process.exit(1)
}
