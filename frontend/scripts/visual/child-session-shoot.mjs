/* Screenshot how each surface treats a session that another session started.

   One script, three surfaces, because the three answers are one design and a
   reviewer has to read them together:

     explore  /explore. The started session is folded away and the grid keeps
              the parent card alone. NO control reveals it here: a browse card
              names no parent, so a count hanging off one would ask a visitor to
              guess whose it was. Captures: cex-explore-child-folded.
     home     `/` signed in. The recent-sessions list hangs an expandable chip
              off the row that started them.
              Captures: village-home-child-{collapsed,expanded}.
     project  /users/{username}/projects/{projectHash}. The same chip.
              Captures: village-project-child-{collapsed,expanded}.

   Build provenance is asserted before any PNG is written, per surface, against
   the LIVE DOM. A stale server, or a build from another worktree, fails here
   rather than producing a plausible-looking image.

   Capture geometry is asserted too. Every image is taken with the whole
   document inside one viewport at scroll offset zero, so the fixed app header
   sits at the top of the raster and the bottom of the list is in frame. A
   capture taken at a scroll offset fails: a nav painted across the middle of a
   page is misleading review evidence.

   env:
     VILLAGE_URL     app origin (default http://localhost:3000)
     CHROME_PATH     Chrome/Chromium binary (required)
     PUPPETEER_CORE  explicit module path to puppeteer-core (optional)
     UNMATCHED_CHILD_ID  transcript id whose parent the response does not carry;
                         it must keep an ordinary row on every surface, never be
                         folded away (default per surface)
     PROJECT_PATH    the project route to shoot (project surface only)
   usage: VILLAGE_URL=... CHROME_PATH=... node child-session-shoot.mjs <surface> <theme> <outdir>
*/
import { mkdirSync, statSync } from 'node:fs'
import { SurfaceGate } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const SURFACES = {
  explore: {
    path: '/explore',
    // The explore mock's row whose parent it does not carry.
    unmatched: 'c1a099',
  },
  home: {
    path: '/',
    // The home mock's row naming a session it does not carry.
    unmatched: 'a8',
    listSelector: '[data-testid="home-recent-sessions"]',
    prefix: 'village-home-child',
  },
  project: {
    path: process.env.PROJECT_PATH || `/users/alice-dev/projects/${'1'.repeat(64)}`,
    unmatched: null,
    listSelector: '[data-testid="project-display-name"]',
    prefix: 'village-project-child',
  },
}

const surface = process.argv[2]
const theme = process.argv[3] || 'dark'
const out = process.argv[4] || `/tmp/child-session-${surface}-${theme}`
const CHROME = process.env.CHROME_PATH
const ORIGIN = (process.env.VILLAGE_URL || 'http://localhost:3000').replace(/\/$/, '')

const die = (code, what, why, means, fix) => {
  console.error(
    `ERROR [child-session-shoot.mjs] ${what}
  Why: ${why}
  Where: child-session-shoot.mjs, surface=${surface}, theme=${theme}, origin=${ORIGIN}.
  Means: ${means}
  Fix: ${fix}`,
  )
  process.exit(code)
}

if (!SURFACES[surface]) {
  die(1, `unknown surface ${JSON.stringify(surface)}.`,
    `the first argument must be one of ${Object.keys(SURFACES).join(', ')}.`,
    'nothing would be captured.',
    'pass a known surface name.')
}
if (!CHROME) {
  die(1, 'CHROME_PATH is unset.',
    'the script has no browser to drive.',
    'no capture can be taken.',
    'set CHROME_PATH to your Chrome/Chromium binary.')
}

const config = SURFACES[surface]
const UNMATCHED = process.env.UNMATCHED_CHILD_ID || config.unmatched
const URL = `${ORIGIN}${config.path}`
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
const linkedIDs = (root = '') =>
  page.$$eval(`${root} a[href^="/transcripts/"]`.trim(), (els) =>
    els.map((el) => el.getAttribute('href').replace('/transcripts/', '')))

/* Grow the viewport to the whole document and park the page at its top.

   The app header is `position: fixed`, so with `captureBeyondViewport` Chrome
   rasters it wherever the viewport currently sits — scrolling a control into
   view before a capture paints the nav across the MIDDLE of the image. That is
   misleading review evidence, so every capture is taken with the whole surface
   inside one viewport at scroll offset zero instead.

   The height is measured, applied, and re-measured: growing the viewport can
   reflow the page and change the document height again, so it settles rather
   than trusting one reading. */
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
        'the page grew without bound, or a fixture served far more rows than this surface expects.',
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
  // Non-convergence is not thrown here: the caller's fit assertion is the single
  // place that decides whether the geometry is good enough to capture, and it
  // reports these readings.
  await pause(250)
  return { attempts: SETTLE_ATTEMPTS, heights: seen }
}

/* The regression guard for the defect above.

   The load-bearing assertions are the scroll offset and the fit: the page must
   be parked at its top, and the whole document must be inside the viewport.
   Together those are what put the header at the top of the raster.

   The header's own `top` is recorded but cannot fail on its own — the header is
   `position: fixed`, so it reads 0 at any scroll offset. It is kept only as the
   reading that makes a failure legible, never as the test. */
const assertCaptureGeometry = async (name, settled) => {
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
  if (geom.docHeight > geom.viewportHeight + 1) {
    await stop(1, `${name} would be captured with ${geom.docHeight}px of document in a ${geom.viewportHeight}px viewport.`,
      `the viewport did not converge on the document height in ${settled.attempts} attempt(s); it measured ${settled.heights.join('px, ')}px.`,
      'the capture would cut off the bottom of the list.',
      'find what keeps growing the page, or raise SETTLE_ATTEMPTS / the reflow pause so it can settle.')
  }
  return geom
}

const shoot = async (name) => {
  const settled = await settleWholePageInViewport()
  const geom = await assertCaptureGeometry(name, settled)
  const el = await page.$('body')
  const box = await el.boundingBox()
  if (!box || box.width < 4 || box.height < 4) {
    await stop(1, `body resolved to a blank or zero-size box: ${JSON.stringify(box)}.`,
      'the app shell or the surface did not lay out.',
      'the PNG would be empty.',
      'confirm the route is reachable and the fixtures loaded.')
  }
  // The clip box is what is actually rastered, so it is asserted against the
  // document rather than trusted to follow from the viewport fit: `body` can lay
  // out shorter than the document it overflows, and that PNG would cut the
  // bottom of the list while every DOM reading above stayed green.
  if (Math.round(box.y) !== 0 || Math.round(box.height) + 1 < geom.docHeight) {
    await stop(1, `${name} would raster a ${Math.round(box.height)}px box at y=${Math.round(box.y)} over a ${geom.docHeight}px document.`,
      'the captured element does not cover the whole page.',
      'the capture would cut off part of the surface.',
      'capture an element that spans the document, or extend the clip to the document height.')
  }
  const file = `${out}/${name}.png`
  await el.screenshot({ path: file, captureBeyondViewport: true })
  // Bracket the raster: puppeteer may scroll an element into view to shoot it,
  // so the geometry is re-read afterwards rather than assumed to have held.
  const after = await assertCaptureGeometry(`${name} (after capture)`, settled)
  const r = await gate.assert(name, file, { sel: 'body', where: 'child-session-shoot.mjs' })
  const bytes = statSync(file).size
  console.log('shot', name.padEnd(32), `${Math.round(box.width)}x${Math.round(box.height)}`.padEnd(11),
    `nonbg=${(r.nonbgRatio * 100).toFixed(1)}% colors=${r.distinctColors} ${(bytes / 1024).toFixed(1)}KB`,
    `doc-h=${geom.docHeight} viewport-h=${geom.viewportHeight} settle-attempts=${settled.attempts} scrollY-after=${after.scrollY}`)
  return file
}

// ── explore: the fold, and no control ────────────────────────────────────────

if (surface === 'explore') {
  if (!(await waitFor('.cex-tcard'))) {
    await stop(2, 'the Explore browse cards never mounted.',
      'the browse payload is missing, or NEXT_PUBLIC_API_URL points at a backend that does not serve the explore fixtures.',
      'the capture would be blank or a loading skeleton.',
      'start the explore mock REST server, point NEXT_PUBLIC_API_URL at it, and retry.')
  }

  // Provenance: the fold is what this build introduces, so the served page must
  // show it happening. The mock's two started rows must be off the grid, and the
  // row whose parent it does not carry must still be on it.
  const FOLDED = (process.env.FOLDED_CHILD_IDS || 'c1a002,c1a001').split(',')
  const browsed = await linkedIDs()
  const stillBrowsing = FOLDED.filter((id) => browsed.includes(id))
  if (stillBrowsing.length > 0) {
    await stop(2, `the grid still browses ${stillBrowsing.join(', ')}, which another row in this response started.`,
      'the served build predates the fold, or the mock served no row naming a visible parent session.',
      'the capture would prove nothing about this change.',
      'rebuild and restart the app from THIS worktree, confirm the mock serves parent_session_id, and retry.')
  }
  if (UNMATCHED && !browsed.includes(UNMATCHED)) {
    await stop(1, `the served list does not browse ${UNMATCHED}, whose parent this response does not carry.`,
      'the fold removed a row instead of leaving it in the list.',
      'the capture would show a session disappearing, which this change forbids.',
      'confirm the served build is this worktree and that an unmatched row keeps its own row.')
  }

  // The ruling this capture is evidence for: discovery offers no control.
  const controls = await page.$$eval('[data-parent-transcript-id]', (els) => els.length)
  if (controls > 0) {
    await stop(1, `discovery rendered ${controls} control(s) for the rows it folded away.`,
      'a chip of started sessions reached the browse column.',
      'the capture would contradict the surface it is evidence for: a browse card names no parent for a count to hang off.',
      'remove the control from the discovery route and rebuild.')
  }

  // The count above the grid must describe the cards under it.
  const headerCount = await page.$$eval('.cex-eyebrow', (els) => {
    const eyebrow = els.find((el) => (el.textContent || '').trim().startsWith('transcripts'))
    const count = eyebrow && eyebrow.querySelector('.cex-count')
    return count ? count.textContent.trim() : null
  })
  if (headerCount === null) {
    await stop(2, 'the browse list rendered no transcripts count above the grid.',
      'the served build is not the Explore surface this gate expects.',
      'the capture would prove nothing about the count.',
      'rebuild from this worktree and retry.')
  }
  const browseCards = await page.$$eval('.cex-tcard', (els) => els.length)
  if (headerCount !== String(browseCards)) {
    await stop(1, `the count above the grid reads ${headerCount} over ${browseCards} cards.`,
      'the count still counts rows this page folded away.',
      'the capture would show a count disagreeing with the list under it.',
      'rebuild from this worktree and retry.')
  }

  await shoot('cex-explore-child-folded')
  console.log('provenance', `folded-off-the-grid=${FOLDED.join(',')}`, `unmatched-row-browsing=${UNMATCHED}`,
    `controls=${controls}`, `header-count=${headerCount}`, `browse-cards=${browseCards}`)
  console.log('console errors:', errs.length ? errs.slice(0, 6) : 'none')
  await browser.close()
  process.exit(0)
}

// ── home and a project page: the chip ────────────────────────────────────────

if (!(await waitFor(config.listSelector))) {
  await stop(2, `the ${surface} surface never mounted (${config.listSelector} is absent).`,
    'the route did not render, or the mock served no rows for it.',
    'the capture would be blank or a loading skeleton.',
    `start the home mock REST server with MOCK_CHILD_SESSIONS=1, point NEXT_PUBLIC_API_URL at it, and retry.`)
}

const chip = await waitFor('[data-parent-transcript-id]')
if (!chip) {
  await stop(2, 'no chip of started sessions rendered under any row.',
    'the served build predates the chip, or the mock served no row naming a visible parent session.',
    'the capture would prove nothing about this change.',
    'rebuild and restart the app from THIS worktree with MOCK_CHILD_SESSIONS=1, and retry.')
}
const parentID = await page.evaluate((el) => el.getAttribute('data-parent-transcript-id'), chip)
const toggle = await waitFor('[data-testid="child-session-disclosure-toggle"]')
if (!toggle) {
  await stop(2, `the chip under ${parentID} rendered without its control.`,
    'the shared collapsed-group control did not render.',
    'the capture would show a chip nobody can open.',
    'rebuild from this worktree and retry.')
}
// Read from the label element itself, not the whole control: the control also
// carries its show/hide affordance, and a check over that text could not tell a
// bare count from one with a leading mark in front of it.
const labelEl = await waitFor('[data-testid="child-session-disclosure-label"]')
if (!labelEl) {
  await stop(2, 'the chip rendered no label element.',
    'the served build predates the shared collapsed-group control.',
    'the capture would prove nothing about the chip.',
    'rebuild from this worktree and retry.')
}
const collapsedLabel = (await page.evaluate((el) => el.textContent.trim(), labelEl)).replace(/\s+/g, ' ')
if (!/^\d+ child session/.test(collapsedLabel)) {
  await stop(1, `the collapsed label reads ${JSON.stringify(collapsedLabel)}.`,
    'the chip rendered without its count, or with something in front of it. It announces a bare count: the chip ' +
      'hangs off its own parent row, so the count reads as part of that row rather than as an item being offered.',
    'the capture would show the wrong chrome.',
    'rebuild from this worktree and retry.')
}

// The chip hangs off its own parent's row: what it belongs to is a fact about
// the DOM, not something a reader is asked to infer from the order.
const sitsWithItsParent = await page.evaluate((el) => {
  const unit = el.parentElement
  if (!unit) return false
  return [...unit.querySelectorAll('a[href^="/transcripts/"]')]
    .some((a) => a.getAttribute('href') === `/transcripts/${el.getAttribute('data-parent-transcript-id')}`)
}, chip)
if (!sitsWithItsParent) {
  await stop(1, `the chip under ${parentID} does not sit with that row.`,
    'the chip and the row it belongs to are not one unit of the list.',
    'the capture would show a count a viewer cannot attribute to any row, which is the finding this change answers.',
    'render the chip inside the same list unit as its parent row and rebuild.')
}

/* The vertical rhythm between a parent row and its own chip.

   Asserted as COMPUTED style on the served page, not as a class name: the gap a
   reader complained about is a rendered distance, and a class that stopped
   resolving to any padding would still be present in the markup.

   Three readings, because one alone can pass while the design is wrong: the row
   that carries a chip must be one design-system step tighter underneath, an
   ordinary row in the same list must be UNCHANGED (so the tightening did not
   leak into every row), and the chip's indentation must be untouched. */
const RHYTHM = { tightRowPaddingBottom: 8, ordinaryRowPaddingBottom: 12, maxDetailToLabelGap: 22, chipIndent: 20 }
const rhythm = await page.evaluate((el) => {
  const unit = el.parentElement
  const row = unit.firstElementChild
  const label = el.querySelector('[data-testid="child-session-disclosure-label"]')
  const detail = row.querySelectorAll('.min-w-0 > span')[1]
  // A row in the same list that carries no chip: its unit holds the row alone.
  const ordinary = [...unit.parentElement.children].find(
    (sibling) => sibling !== unit && sibling.children.length === 1,
  )
  const px = (value) => Math.round(parseFloat(value))
  return {
    tightRowPaddingBottom: px(getComputedStyle(row).paddingBottom),
    ordinaryRowPaddingBottom: ordinary ? px(getComputedStyle(ordinary.firstElementChild).paddingBottom) : null,
    detailToLabelGap: Math.round(label.getBoundingClientRect().top - detail.getBoundingClientRect().bottom),
    chipIndent: px(getComputedStyle(el).paddingLeft),
  }
}, chip)
const rhythmFaults = []
if (rhythm.tightRowPaddingBottom !== RHYTHM.tightRowPaddingBottom) {
  rhythmFaults.push(
    `the row carrying the chip leaves ${rhythm.tightRowPaddingBottom}px under itself, not ${RHYTHM.tightRowPaddingBottom}px`)
}
if (rhythm.ordinaryRowPaddingBottom === null) {
  rhythmFaults.push('no ordinary row was found in the list to compare against')
} else if (rhythm.ordinaryRowPaddingBottom !== RHYTHM.ordinaryRowPaddingBottom) {
  rhythmFaults.push(
    `an ordinary row leaves ${rhythm.ordinaryRowPaddingBottom}px under itself, not ${RHYTHM.ordinaryRowPaddingBottom}px, ` +
      'so the tightening leaked out of the rows that carry a chip')
}
if (rhythm.detailToLabelGap > RHYTHM.maxDetailToLabelGap) {
  rhythmFaults.push(
    `${rhythm.detailToLabelGap}px sits between the row's detail line and the chip's label, over the ` +
      `${RHYTHM.maxDetailToLabelGap}px this surface is held to`)
}
if (rhythm.chipIndent !== RHYTHM.chipIndent) {
  rhythmFaults.push(`the chip is indented ${rhythm.chipIndent}px, not the ${RHYTHM.chipIndent}px it has always been`)
}
if (rhythmFaults.length > 0) {
  await stop(1, `the chip's vertical rhythm is wrong: ${rhythmFaults.join('; ')}.`,
    'the served page does not lay the chip out under its parent row the way this surface is held to.',
    'the capture would show spacing a reader has already asked to have corrected, or an indentation change nobody asked for.',
    'rebuild from this worktree, and check the row spacing that applies when a row carries a chip.')
}

if (UNMATCHED && !(await linkedIDs()).includes(UNMATCHED)) {
  await stop(1, `the served list does not show ${UNMATCHED}, whose parent it does not carry.`,
    'the fold removed a row instead of leaving it in the list.',
    'the capture would show a session disappearing, which this change forbids.',
    'confirm the served build is this worktree and that an unmatched row keeps its own row.')
}

await shoot(`${config.prefix}-collapsed`)

await toggle.click()
if (!(await waitFor('[data-testid="child-session-disclosure-rows"] a[href^="/transcripts/"]'))) {
  await stop(1, 'expanding the chip produced no rows.',
    'the rows folded out of the list were not handed to the chip.',
    'the expanded capture would show an empty chip.',
    'rebuild from this worktree and retry.')
}
const expandedIDs = await linkedIDs('[data-testid="child-session-disclosure-rows"]')
if (UNMATCHED && expandedIDs.includes(UNMATCHED)) {
  await stop(1, `expanding the chip revealed ${UNMATCHED}, whose parent this response does not carry.`,
    'a row was folded under a parent the response never held.',
    'the capture would show behavior this change forbids.',
    'rebuild from this worktree and retry.')
}
// Let the revealed rows finish laying out before the document height is measured.
await pause(400)
await shoot(`${config.prefix}-expanded`)

console.log('rhythm', JSON.stringify(rhythm))
console.log('provenance', `chip-under=${parentID}`, `collapsed-label=${JSON.stringify(collapsedLabel)}`,
  `expanded-rows=${expandedIDs.join(',')}`, `unmatched-row-listed=${UNMATCHED ?? 'n/a'}`)
console.log('console errors:', errs.length ? errs.slice(0, 6) : 'none')
await browser.close()
