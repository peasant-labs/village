/* Screenshot the collective review route, /groups/{id}/review.

   One surface, both themes. The page reads the collective's pending queue as
   the same project > branch > session tree the contribute page uses, with a
   transcript preview column and a bottom bar that decides the whole selection
   in one request.

   Build provenance is asserted against the LIVE DOM before any PNG is written:
   the route must render the review panel, the tree must be grouped into the
   mock's TWO projects, the child disclosure must be present, and the bottom
   bar's own actions must exist. A stale server, or a build from another
   worktree, fails here rather than producing a plausible-looking image.

   Capture geometry is asserted too. Every image is taken with the whole
   document inside one viewport at scroll offset zero, so the fixed app header
   sits at the top of the raster and the bottom bar is in frame.

   env:
     VILLAGE_URL   app origin (default http://localhost:3000)
     CHROME_PATH   Chrome/Chromium binary (required)
     REVIEW_GROUP  the collective id in the route (default demo-group)
     PUPPETEER_CORE  explicit module path to puppeteer-core (optional)
   usage: VILLAGE_URL=... CHROME_PATH=... node review-shoot.mjs <theme> <outdir>
*/
import { mkdirSync, statSync } from 'node:fs'
import { SurfaceGate } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const theme = process.argv[2] || 'dark'
const out = process.argv[3] || `/tmp/review-${theme}`
const CHROME = process.env.CHROME_PATH
const GROUP = process.env.REVIEW_GROUP || 'demo-group'
const ORIGIN = (process.env.VILLAGE_URL || 'http://localhost:3000').replace(/\/$/, '')

const die = (code, what, why, means, fix) => {
  console.error(
    `ERROR [review-shoot.mjs] ${what}
  Why: ${why}
  Where: review-shoot.mjs, theme=${theme}, origin=${ORIGIN}, group=${GROUP}.
  Means: ${means}
  Fix: ${fix}`,
  )
  process.exit(code)
}

if (!CHROME) {
  die(1, 'CHROME_PATH is unset.',
    'the script has no browser to drive.',
    'no capture can be taken.',
    'set CHROME_PATH to your Chrome/Chromium binary.')
}

const URL = `${ORIGIN}/groups/${GROUP}/review`
mkdirSync(out, { recursive: true })

const BASE_VP = { width: 1396, height: 939, deviceScaleFactor: 1 }
const pause = (ms) => new Promise((r) => setTimeout(r, ms))

const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', defaultViewport: { ...BASE_VP } })
const page = await browser.newPage()
await page.setCookie({ name: 'peasant_token', value: 'demo-token', domain: 'localhost', path: '/' })
await applyDeterminism(page)
const errs = []
page.on('console', (m) => { if (m.type() === 'error' && !/favicon|404|hydrat/.test(m.text())) errs.push(m.text()) })
page.on('pageerror', (e) => errs.push('pageerr: ' + e.message))

const stop = async (...args) => { await browser.close(); die(...args) }

await page.goto(URL, { waitUntil: 'networkidle0' })
await page.evaluate((nextTheme) => localStorage.setItem('peasant-theme', nextTheme), theme)
await page.reload({ waitUntil: 'networkidle0' })
await pause(900)

const actualTheme = await page.evaluate(() => document.documentElement.getAttribute('data-theme'))
if (actualTheme !== theme) {
  await stop(3, `the requested theme did not apply: [data-theme]="${actualTheme}" after requesting "${theme}".`,
    'the theme toggle / localStorage handshake did not settle.',
    'the capture would be the wrong theme.',
    'confirm the root layout uses the shared theme hook and retry.')
}

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

/* Grow the viewport to the whole document and park the page at its top.

   The app header is `position: fixed`, so with `captureBeyondViewport` Chrome
   rasters it wherever the viewport currently sits - scrolling a control into
   view before a capture paints the nav across the MIDDLE of the image. Every
   capture is therefore taken with the whole surface inside one viewport at
   scroll offset zero. */
const SETTLE_ATTEMPTS = 5
const MAX_VIEWPORT_HEIGHT = 20000

const settleWholePageInViewport = async () => {
  const measure = () => page.evaluate(() =>
    Math.ceil(Math.max(document.documentElement.scrollHeight, document.body.scrollHeight)))
  await page.evaluate(() => window.scrollTo(0, 0))
  const seen = []
  for (let attempt = 0; attempt < SETTLE_ATTEMPTS; attempt += 1) {
    const docHeight = await measure()
    seen.push(docHeight)
    const want = Math.max(BASE_VP.height, docHeight)
    if (want > MAX_VIEWPORT_HEIGHT) {
      await stop(1, `the document measured ${docHeight}px, past the ${MAX_VIEWPORT_HEIGHT}px capture ceiling.`,
        'the page grew without bound, or the mock served far more rows than this surface expects.',
        'Chrome would refuse or truncate the viewport, and the capture would be silently wrong.',
        'check the mock fixture size, or raise MAX_VIEWPORT_HEIGHT if the surface really is that tall.')
    }
    if (page.viewport().height === want) {
      await pause(250)
      return { attempts: attempt + 1, heights: seen }
    }
    await page.setViewport({ ...BASE_VP, height: want })
    await pause(300)
    await page.evaluate(() => window.scrollTo(0, 0))
  }
  await pause(250)
  return { attempts: SETTLE_ATTEMPTS, heights: seen }
}

/* `fit` decides whether the viewport is GROWN to the document first.

   It is false for exactly one state: the preview column open. The shared
   contribute/review composition puts the preview inside a `min-h-screen`
   shell whose panel stretches to the viewport, so the document measures a
   CONSTANT 202px taller than whatever viewport it is given - growing the
   viewport grows the document by the same amount, forever, and the settle
   loop can never converge. (Measured on the shipped
   `/groups/{id}/contribute` route too, at 939/1400/2400px: doc = viewport +
   202 every time. It is a property of that composition, not of this change.)

   Nothing is given up. `captureBeyondViewport` rasters the whole document
   from the base viewport, and the assertion that the raster COVERS the
   document still runs in both modes - as do the two load-bearing ones,
   parked at scroll 0 and the header at the top of the raster. The only
   waived check is "the document fits inside the viewport", which on this
   surface is unsatisfiable by construction rather than a defect. */
const assertCaptureGeometry = async (name, settled, fit = true) => {
  const geom = await page.evaluate(() => {
    const header = document.querySelector('header')
    if (!header) return null
    const rect = header.getBoundingClientRect()
    return {
      top: Math.round(rect.top),
      scrollY: Math.round(window.scrollY),
      viewportHeight: window.innerHeight,
      docHeight: Math.ceil(Math.max(document.documentElement.scrollHeight, document.body.scrollHeight)),
    }
  })
  if (!geom) {
    await stop(2, 'the app header never rendered, so the capture geometry cannot be checked.',
      'the served build is not the village app shell.',
      'the capture would prove nothing about the chrome.',
      'rebuild from this worktree and retry.')
  }
  if (geom.scrollY !== 0 || geom.top !== 0) {
    await stop(1, `${name} would be captured with the page scrolled to ${geom.scrollY} (header rect top ${geom.top}).`,
      'the page was left at a scroll offset, so the fixed header rasters over the middle of the image.',
      'the capture would show the top nav painted across the list, which is misleading review evidence.',
      'park the page at scroll 0 before capturing; do not scroll an element into view first.')
  }
  if (fit && geom.docHeight > geom.viewportHeight + 1) {
    await stop(1, `${name} would be captured with ${geom.docHeight}px of document in a ${geom.viewportHeight}px viewport.`,
      `the viewport did not converge on the document height in ${settled.attempts} attempt(s); it measured ${settled.heights.join('px, ')}px.`,
      'the capture would cut off the bottom of the page.',
      'find what keeps growing the page, or raise SETTLE_ATTEMPTS / the reflow pause so it can settle.')
  }
  return geom
}

const shoot = async (name, { fit = true } = {}) => {
  const settled = fit
    ? await settleWholePageInViewport()
    : (await page.setViewport({ ...BASE_VP }),
       await pause(400),
       await page.evaluate(() => window.scrollTo(0, 0)),
       await pause(200),
       { attempts: 1, heights: [BASE_VP.height] })
  const geom = await assertCaptureGeometry(name, settled, fit)
  const el = await page.$('body')
  const box = await el.boundingBox()
  if (!box || box.width < 4 || box.height < 4) {
    await stop(1, `body resolved to a blank or zero-size box: ${JSON.stringify(box)}.`,
      'the app shell or the surface did not lay out.',
      'the PNG would be empty.',
      'confirm the route is reachable and the fixtures loaded.')
  }
  if (Math.round(box.y) !== 0 || Math.round(box.height) + 1 < geom.docHeight) {
    await stop(1, `${name} would raster a ${Math.round(box.height)}px box at y=${Math.round(box.y)} over a ${geom.docHeight}px document.`,
      'the captured element does not cover the whole page.',
      'the capture would cut off part of the surface.',
      'capture an element that spans the document, or extend the clip to the document height.')
  }
  const file = `${out}/${name}.png`
  await el.screenshot({ path: file, captureBeyondViewport: true })
  const after = await assertCaptureGeometry(`${name} (after capture)`, settled, fit)
  const r = await gate.assert(name, file, { sel: 'body', where: 'review-shoot.mjs' })
  const bytes = statSync(file).size
  console.log('shot', name.padEnd(34), `${Math.round(box.width)}x${Math.round(box.height)}`.padEnd(11),
    `nonbg=${(r.nonbgRatio * 100).toFixed(1)}% colors=${r.distinctColors} ${(bytes / 1024).toFixed(1)}KB`,
    `doc-h=${geom.docHeight} viewport-h=${geom.viewportHeight} settle-attempts=${settled.attempts} scrollY-after=${after.scrollY}`)
  return file
}

// ── Provenance: the served build must BE this change ─────────────────────────

if (!(await waitFor('[data-testid="review-panel"]'))) {
  await stop(2, 'the review panel never mounted.',
    'the served build predates this route, the viewer is not the collective owner, or the mock served an empty pending queue.',
    'the capture would be a notice or an empty state, not the surface under review.',
    'rebuild and restart the app from THIS worktree, confirm the mock serves /groups/{id}/pending rows, and retry.')
}

// The tree must be GROUPED. A flat list is exactly the surface this change
// replaces, so a capture of one would be evidence for the wrong page.
const projects = await page.$$eval('section[aria-label^="project "]', (els) =>
  els.map((el) => el.getAttribute('aria-label').replace(/^project /, '')))
if (projects.length < 2) {
  await stop(2, `the queue rendered ${projects.length} project grouping(s): ${projects.join(', ') || '(none)'}.`,
    'the pending rows carry no project identity, or the served build reads the queue as one flat list.',
    'the capture would not show the grouping this change exists for.',
    'confirm the backend widened /groups/{id}/pending with project_hash/project_name/branch, rebuild, and retry.')
}

// The shared child-session disclosure: a submission read under the submission
// that started it.
const disclosures = await page.$$eval('[data-parent-transcript-id]', (els) => els.length)
if (disclosures < 1) {
  await stop(2, 'no child-session disclosure rendered in the review tree.',
    'the mock served no submission naming a parent that is also in the queue, or the fold is not wired here.',
    'the capture would not show the fold this page inherits from every other transcript list.',
    'confirm the mock queue carries a parent/child pair and retry.')
}

// The bottom bar's own controls. Both decisions must exist: a bar that could
// only approve would make the reject action a lie.
const actions = await page.$$eval('button', (els) => els.map((el) => (el.textContent || '').trim()))
for (const wanted of ['approve selected', 'reject selected']) {
  if (!actions.includes(wanted)) {
    await stop(2, `the bottom bar has no "${wanted}" action.`,
      'the served build is not this change, or the bar did not render.',
      'the capture would not show the multi-select decision this page exists for.',
      'rebuild from this worktree and retry.')
  }
}
const tally = await page.$eval('[data-testid="review-selection-count"]', (el) => (el.textContent || '').trim())

await shoot(`village-review-${theme}`)

// A second capture WITH a selection, so the bar's counted state is evidence
// too rather than only its empty one. The checkbox is clicked through the
// row's own control; the page is parked back at the top before the shoot.
const firstProjectBox = await page.$('section[aria-label^="project "] input[type="checkbox"]')
if (firstProjectBox) {
  await firstProjectBox.click()
  await pause(400)
  const selected = await page.$eval('[data-testid="review-selection-count"]', (el) => (el.textContent || '').trim())
  if (selected === tally) {
    await stop(1, `ticking a project changed nothing: the tally still reads "${selected}".`,
      'the selection did not reach the bottom bar.',
      'the capture would show a selected tree beside a bar claiming nothing is selected.',
      'confirm the tree and the bar read one selection and retry.')
  }
  // Provenance, read from the LIVE DOM rather than from the served HTML: the
  // route's code is in a lazily-loaded chunk, so the initial document names
  // none of this change's strings and a static grep of it proves nothing.
  // With a selection held and the fold still SHUT, the collapsed control must
  // name the hidden rows it has selected - wording this change introduces, so
  // a build without it fails here instead of producing a plausible image.
  const foldLabel = await page.$eval('[data-testid="child-session-disclosure-label"]',
    (el) => (el.textContent || '').trim())
  const foldOpen = await page.$eval('[data-testid="child-session-disclosure-toggle"]',
    (el) => el.getAttribute('aria-expanded'))
  if (foldOpen !== 'false') {
    await stop(1, `the child-session fold reads aria-expanded="${foldOpen}" before the selected capture.`,
      'the fold was opened, so its rows are on screen.',
      'the capture would not show what a CLOSED fold says about a selection nobody can see.',
      'leave the fold shut for this capture.')
  }
  if (!/, \d+ selected$/.test(foldLabel)) {
    await stop(2, `the collapsed fold reads ${JSON.stringify(foldLabel)} while a selection is held.`,
      'the served build does not name the selected rows a shut fold hides.',
      'the capture would show a selection resting on rows nobody can see, which this page forbids.',
      'rebuild and restart the app from THIS worktree and retry.')
  }

  // Open the preview column on a real submission. The column is half of what
  // this page is for - a reviewer decides work they can read - so a capture
  // showing only its empty state would be evidence for half the surface.
  const titleButton = await page.$('[data-testid^="contribute-session-row-"] button')
  if (!titleButton) {
    await stop(2, 'no session title control rendered, so the preview column cannot be opened.',
      'the served build does not draw the tree rows as preview triggers.',
      'the capture would show only the preview empty state.',
      'rebuild from this worktree and retry.')
  }
  await titleButton.click()
  await pause(1200)
  const previewEmpty = await page.$('[data-testid="transcript-preview-empty"]')
  if (previewEmpty) {
    await stop(2, 'the preview column stayed on its empty state after a session title was clicked.',
      'the click did not reach the preview, or the mock serves no transcript for that id.',
      'the capture would not show the preview this page exists to pair with the queue.',
      'confirm the mock answers /transcripts/{id} and /transcripts/{id}/content for the queue ids, and retry.')
  }
  await shoot(`village-review-selected-${theme}`, { fit: false })
  console.log('provenance', `projects=${projects.join(',')}`, `disclosures=${disclosures}`,
    `tally-empty="${tally}"`, `tally-selected="${selected}"`, `fold-label="${foldLabel}"`, 'preview=open')
} else {
  await stop(2, 'the tree rendered no project checkbox to tick.',
    'the served build does not draw the selection controls.',
    'the capture would not show a selectable review tree.',
    'rebuild from this worktree and retry.')
}

console.log('console errors:', errs.length ? errs.slice(0, 6) : 'none')
await browser.close()
process.exit(0)
