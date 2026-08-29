/* Screenshot how each surface treats a session that another session started.

   One script, every surface, because the answers are one design and a reviewer
   has to read them together:

     explore  /explore. The started session is folded away and the grid keeps
              the parent card alone. NO control reveals it here: a browse card
              names no parent, so a count hanging off one would ask a visitor to
              guess whose it was. Captures: cex-explore-child-folded.
     home     `/` signed in. The recent-sessions list hangs an expandable chip
              off the row that started them.
              Captures: village-home-child-{collapsed,expanded}.
     project  /users/{username}/projects/{projectHash}. The same chip.
              Captures: village-project-child-{collapsed,expanded}.

   The lists below fold behind the SAME collapsed control, and each is captured
   closed and open. They read a fixture mock of their own,
   `mock-rest-child-sessions.mjs`, whose every dataset carries the two cases the
   contrast needs: a parent with two started sessions, and a row naming a
   starter the response does not carry, which must keep its ordinary row.

     profile                   /users/{username}. The fold within a project of
                               the person's own library.
                               Captures: village-profile-child-*.
     collective-data           /groups/{id}. The collective's contributions,
                               now drawn by the shared transcript list rather
                               than a table of their own: the owner's per-row
                               selection and the "all" select-all are asserted
                               here, and so is the absence of a table.
                               Captures: village-collective-data-child-*.
     collective-repos          the same page, read by repository. Every
                               repository is opened, because the parent and the
                               unplaceable row are in different ones.
                               Captures: village-collective-repos-child-*.
     collective-pending        the same page's review queue. It does NOT fold:
                               every submission is drawn flat, side by side, in
                               the one `ModerationQueue`, each keeping its own
                               approve and reject actions.
                               Captures: village-collective-pending-child-flat.
     collective-contributions  the same page's "your contributions", where each
                               revealed row keeps its remove action.
                               Captures: village-collective-contributions-child-*.
     contribute-tree           /groups/{id}/contribute. The label is a bare
                               count here now; it read "+ N child sessions"
                               before.
                               Captures: village-contribute-child-*.

   The three collective entries share one route. `collective-data` and
   `collective-repos` still fold, so their CLOSED captures are the same page
   and come out byte-identical; that is honest, not a fault. `collective-pending`
   does not fold at all, so it produces one flat capture rather than a
   closed/open pair.

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
     MOCK_GROUP_ID   the collective the fixture mock serves (default demo-group)
     MOCK_USERNAME   the profile to shoot (default alice-dev)
   usage: VILLAGE_URL=... CHROME_PATH=... node child-session-shoot.mjs <surface> <theme> <outdir>
*/
import { mkdirSync, statSync } from 'node:fs'
import { SurfaceGate } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

// The collective the fixture mock serves. Every collective surface reads it.
const GROUP_ID = process.env.MOCK_GROUP_ID || 'demo-group'

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
  /* ── The lists that fold behind one collapsed control ────────────────────

     Each of these entries names, in the mock's own ids and words, what the
     served page must show before a PNG is written: the row a control hangs
     under, the rows it reveals, and the row naming a starter this response
     does NOT carry, which must keep its ordinary place. A build that folds
     differently fails here rather than producing a plausible-looking image.

     The three collective entries share one route: a collective's page draws
     its contributions, its review queue and the caller's own contributions
     together, so each entry names the control it is evidence for rather than
     the page they sit on. */

  profile: {
    path: `/users/${process.env.MOCK_USERNAME || 'alice-dev'}`,
    prefix: 'village-profile-child',
    mountSelector: '[data-testid="child-session-disclosure"]',
    parentID: 'lib-parent',
    revealed: ['Search the rule table for the git-context rules', 'Write the activation-level table test'],
    unmatchedText: 'A session whose starter is not on this page',
  },

  'collective-data': {
    path: `/groups/${GROUP_ID}`,
    prefix: 'village-collective-data-child',
    mountSelector: '[aria-label="Select every transcript on this page"]',
    parentID: 'gt-parent',
    revealed: ['Read the collapsed-group control', 'Draw the selection box on a folded row'],
    unmatchedText: 'A contribution whose starter this collective does not hold',
    verify: 'browse-panel-is-a-list',
  },

  'collective-repos': {
    path: `/groups/${GROUP_ID}`,
    prefix: 'village-collective-repos-child',
    mountSelector: '[aria-label="Select every transcript on this page"]',
    // The repository view is a choice on the same panel, so the capture makes
    // it before asserting anything about what the panel then shows.
    openView: 'repos',
    // Every repository is opened, not only the one the view opens by default:
    // the row naming a starter this response does not carry belongs to the
    // OTHER repository, and the contrast between the two is what this capture
    // is for.
    openEveryRepository: true,
    parentID: 'gt-parent',
    revealed: ['Read the collapsed-group control', 'Draw the selection box on a folded row'],
    unmatchedText: 'A contribution whose starter this collective does not hold',
    // Read INSIDE the repository panel. The page draws the same rows elsewhere,
    // and a reading over the whole page would be satisfied by one of those
    // rather than by the view this capture is evidence for.
    unmatchedWithin: 'Repositories',
    verify: 'fold-sits-under-a-repository',
  },

  // The pending-review queue does NOT fold: it is a flat `ModerationQueue`
  // listing every submission side by side, each with its own approve/reject.
  // It has no `parentID`/`mountSelector`, so it never enters the generic
  // fold-behind-one-control branch below; it is handled in its own branch.
  'collective-pending': {
    path: `/groups/${GROUP_ID}`,
    prefix: 'village-collective-pending-child',
    queueLabel: 'pending review',
    rows: [
      'Rebuild the push wizard on the shared kit',
      'Read the consent copy the wizard shows',
      'Draft the published-transcript preview',
      'A submission whose starter was never offered here',
    ],
  },

  'collective-contributions': {
    path: `/groups/${GROUP_ID}`,
    prefix: 'village-collective-contributions-child',
    mountSelector: '[data-parent-transcript-id="ms-parent"]',
    parentID: 'ms-parent',
    revealed: ['Read the profile library grouping', 'Check the queue keeps approve and reject'],
    unmatchedText: 'A contribution whose starter stayed private',
    verifyExpanded: 'revealed-contributions-keep-their-remove-action',
  },

  'contribute-tree': {
    path: `/groups/${GROUP_ID}/contribute`,
    prefix: 'village-contribute-child',
    mountSelector: '[data-parent-transcript-id="ct-parent"]',
    parentID: 'ct-parent',
    revealed: ['Read the tree builder', 'Swap the label onto the shared control'],
    unmatchedText: 'A session whose starter was never fetched',
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

// ── collective-pending: a flat queue, no fold ────────────────────────────────

if (surface === 'collective-pending') {
  // Scoped to `.rs-canvas` (the RailShell canvas village's own hand-authored
  // block renders into), NOT a bare `.mod-queue[aria-label="pending review"]`.
  // The fairtrade `<Manage>` component this page also mounts (the `.cmg-*`
  // governance summary) independently reads the SAME `pendingReview` data and
  // draws its OWN queue under the identical aria-label -- a pre-existing,
  // unrelated duplicate render this gate does not own. Scoping keeps this
  // gate evidence about the block this change actually touched.
  const queueSelector = `.rs-canvas .mod-queue[aria-label=${JSON.stringify(config.queueLabel)}]`

  if (!(await waitFor(queueSelector))) {
    await stop(2, `the "${config.queueLabel}" review queue never mounted.`,
      'the route did not render, or the mock served no pending shares for it.',
      'the capture would be blank or a loading skeleton.',
      'start mock-rest-child-sessions.mjs, point NEXT_PUBLIC_API_URL at it, rebuild, and retry.')
  }

  // The ruling this capture is evidence for: the queue does not fold. NO
  // element carrying `data-parent-transcript-id` may exist inside it -- that
  // attribute is the fold's own marker, so its presence here means the old
  // nested-disclosure design leaked back in.
  const foldMarkers = await page.$$eval(
    `${queueSelector} [data-parent-transcript-id]`, (els) => els.length)
  if (foldMarkers > 0) {
    await stop(1, `the review queue still carries ${foldMarkers} fold marker(s) (data-parent-transcript-id).`,
      'a submission was nested under another instead of being drawn flat.',
      'the capture would show the queue this change removed, not the flat one that replaced it.',
      'confirm the served build is this worktree, where the queue no longer folds, and retry.')
  }

  // Every submission the mock serves must be its own row, side by side, each
  // keeping its own approve and reject. A row with either action missing (or
  // resolved into a pill by a stray click) fails closed rather than passing on
  // a queue a moderator could not actually clear.
  const rows = await page.$$eval(`${queueSelector} .mod-row`, (els) =>
    els.map((el) => ({
      who: (el.querySelector('.mod-row-who')?.textContent || '').trim(),
      approve: !!el.querySelector('.mod-act-approve'),
      reject: !!el.querySelector('.mod-act-reject'),
    })))

  const missingRows = config.rows.filter((title) => !rows.some((r) => r.who === title))
  if (missingRows.length > 0) {
    await stop(1, `the review queue lists no row for ${missingRows.map((t) => JSON.stringify(t)).join(', ')}.`,
      'a submission the mock served is not drawn as its own row in the flat queue.',
      'the capture would show fewer submissions than the fixture serves.',
      'rebuild from this worktree and retry.')
  }
  if (rows.length !== config.rows.length) {
    await stop(1, `the review queue lists ${rows.length} row(s), not the ${config.rows.length} the fixture serves.`,
      'the queue drew a different set of rows than the mock serves -- extra, duplicated, or merged rows.',
      'the capture would misstate what a moderator sees.',
      'rebuild from this worktree and retry.')
  }
  const rowsWithoutActions = rows.filter((r) => !r.approve || !r.reject)
  if (rowsWithoutActions.length > 0) {
    await stop(1, `${rowsWithoutActions.length} of ${rows.length} row(s) are missing their own approve or reject action.`,
      'a row lost its own decision controls -- the defect this flat queue exists to fix.',
      'the capture would show a moderator unable to decide every submission.',
      'rebuild from this worktree and retry.')
  }

  await shoot(`${config.prefix}-flat`)
  console.log('provenance', `queue=${JSON.stringify(config.queueLabel)}`,
    `fold-markers=${foldMarkers}`, `rows=${rows.length}`,
    `every-row-has-approve-and-reject=${rowsWithoutActions.length === 0}`)
  console.log('console errors:', errs.length ? errs.slice(0, 6) : 'none')
  await browser.close()
  process.exit(0)
}

// ── The lists that fold behind one collapsed control ─────────────────────────

/* Every surface below states the same three things, so they are asserted once
   here rather than six times: the row a control hangs under, the rows it
   reveals, and the row naming a starter this response does not carry, which
   must keep its ordinary place. A surface adds only what is true of IT alone
   -- that a collective's contributions are a LIST and no longer a table, that
   a revealed submission still carries its approve and reject actions -- and
   those extras are named in the surface's own entry. */

if (config.parentID) {
  const chipSelector = `[data-parent-transcript-id="${config.parentID}"]`
  const rowsSelector = `${chipSelector} [data-testid="child-session-disclosure-rows"]`

  if (!(await waitFor(config.mountSelector))) {
    await stop(2, `the ${surface} surface never mounted (${config.mountSelector} is absent).`,
      'the route did not render, or the mock served no rows for it.',
      'the capture would be blank or a loading skeleton.',
      'start mock-rest-child-sessions.mjs, point NEXT_PUBLIC_API_URL at it, rebuild, and retry.')
  }

  /* A view the viewer chooses on the same panel. Clicked from inside the page
     rather than through the browser's own click, which scrolls its target into
     view: this capture is taken at scroll offset zero and a scroll here would
     put the fixed header across the middle of the raster. */
  const clickInPage = (selectorOrText) => page.evaluate((wanted) => {
    const buttons = [...document.querySelectorAll('button')]
    const target = buttons.find((b) => (b.textContent || '').trim() === wanted)
    if (!target) return false
    target.click()
    return true
  }, selectorOrText)

  if (config.openView) {
    if (!(await clickInPage(config.openView))) {
      await stop(2, `the ${surface} surface offers no "${config.openView}" view to open.`,
        'the served build does not carry that view control.',
        'the capture would show the wrong view.',
        'rebuild from this worktree and retry.')
    }
    await pause(500)
  }

  if (config.openEveryRepository) {
    const openedRepositories = await page.evaluate(() => {
      const heading = [...document.querySelectorAll('span')].find(
        (el) => el.children.length === 0 && (el.textContent || '').trim() === 'Repositories')
      const panel = heading && heading.closest('div.border')
      if (!panel) return null
      // The collapsed-group control is also an aria-expanded button; it is
      // opened later, by the step that captures the opened state, so it is
      // left alone here.
      const closed = [...panel.querySelectorAll('button[aria-expanded="false"]')]
        .filter((button) => button.dataset.testid !== 'child-session-disclosure-toggle')
      closed.forEach((button) => button.click())
      return closed.length
    })
    if (openedRepositories === null) {
      await stop(2, 'the repository view never rendered its panel.',
        'the served build does not draw a repository view on this panel.',
        'the capture would be evidence about the wrong view.',
        'rebuild from this worktree and retry.')
    }
    await pause(500)
  }

  const chip = await waitFor(chipSelector)
  if (!chip) {
    await stop(2, `no collapsed control hangs under ${config.parentID}.`,
      'the served build predates the fold on this surface, or the mock served no row naming a visible parent.',
      'the capture would prove nothing about this change.',
      'rebuild and restart the app from THIS worktree, and retry.')
  }

  const labelEl = await waitFor(`${chipSelector} [data-testid="child-session-disclosure-label"]`)
  if (!labelEl) {
    await stop(2, `the control under ${config.parentID} rendered no label element.`,
      'the served build predates the shared collapsed-group control.',
      'the capture would prove nothing about the control.',
      'rebuild from this worktree and retry.')
  }
  const collapsedLabel = (await page.evaluate((el) => el.textContent.trim(), labelEl)).replace(/\s+/g, ' ')
  // A bare count, read off the label element alone: the control also carries a
  // show/hide affordance, and a check over the whole control could not tell a
  // bare count from one with a leading mark in front of it. The contribute tree
  // read "+ N child sessions" before this change, so the mark is what a stale
  // build shows here.
  if (!/^\d+ child sessions?$/.test(collapsedLabel)) {
    await stop(1, `the collapsed label reads ${JSON.stringify(collapsedLabel)}.`,
      'the control rendered without its bare count, or with a mark in front of it.',
      'the capture would show the wrong chrome.',
      'rebuild from this worktree and retry.')
  }

  /* The row this response cannot place. It names a starter the response does
     not carry, so it must keep its own ordinary place in the list -- never be
     folded under a parent nobody sent, and never disappear. */
  const readUnmatched = () => page.evaluate(({ text, within }) => {
    const scope = within
      ? [...document.querySelectorAll('span')]
          .find((el) => el.children.length === 0 && (el.textContent || '').trim() === within)
          ?.closest('div.border')
      : document.body
    if (!scope) return { found: false, insideAControl: false }
    const leaves = [...scope.querySelectorAll('*')].filter(
      (el) => el.children.length === 0 && (el.textContent || '').trim() === text)
    return {
      found: leaves.length > 0,
      insideAControl: leaves.length > 0 &&
        leaves.every((el) => el.closest('[data-testid="child-session-disclosure-rows"]') !== null),
    }
  }, { text: config.unmatchedText, within: config.unmatchedWithin ?? null })

  const unmatchedBefore = await readUnmatched()
  if (!unmatchedBefore.found) {
    await stop(1, `the ${surface} surface does not show ${JSON.stringify(config.unmatchedText)}, whose starter this response does not carry.`,
      'the fold removed a row instead of leaving it in the list.',
      'the capture would show a session disappearing, which this change forbids.',
      'confirm the served build is this worktree and that an unmatched row keeps its own row.')
  }
  if (unmatchedBefore.insideAControl) {
    await stop(1, `${JSON.stringify(config.unmatchedText)} is inside a collapsed control while every control is closed.`,
      'a row was folded under a parent this response never held.',
      'the capture would show behavior this change forbids.',
      'rebuild from this worktree and retry.')
  }

  // ── What is true of this surface alone ────────────────────────────────────

  const readBrowsePanel = () => page.evaluate(() => {
    const box = document.querySelector('[aria-label="Select every transcript on this page"]')
    const panel = box && box.closest('div.border')
    if (!panel) return null
    const chipEl = panel.querySelector('[data-parent-transcript-id="gt-parent"]')
    const row = chipEl && chipEl.parentElement && chipEl.parentElement.firstElementChild
    return {
      tables: panel.querySelectorAll('table').length,
      rowText: row ? (row.innerText || '').replace(/\s+/g, ' ').trim() : null,
      rowSelectionBoxes: panel.querySelectorAll('input[type="checkbox"][aria-label^="Select "]').length,
    }
  })

  const readReposPanel = () => page.evaluate(() => {
    const heading = [...document.querySelectorAll('span')].find(
      (el) => el.children.length === 0 && (el.textContent || '').trim() === 'Repositories')
    const panel = heading && heading.closest('div.border')
    const chipEl = document.querySelector('[data-parent-transcript-id="gt-parent"]')
    if (!panel || !chipEl) return null
    return {
      chipIsInside: panel.contains(chipEl),
      repositories: panel.querySelectorAll('button[aria-expanded]').length,
    }
  })

  const readRevealedContributions = () => page.evaluate((sel) => {
    const rows = document.querySelector(sel)
    if (!rows) return null
    return {
      rows: rows.children.length,
      removeActions: rows.querySelectorAll('button[title="Unshare from this collective"]').length,
    }
  }, rowsSelector)

  let panelReading = null
  if (config.verify === 'browse-panel-is-a-list') {
    panelReading = await readBrowsePanel()
    if (panelReading === null) {
      await stop(2, 'the collective browse panel rendered without its select-all control.',
        'the served build predates the shared list on this panel.',
        'the capture would prove nothing about the change.',
        'rebuild from this worktree and retry.')
    }
    if (panelReading.tables > 0) {
      await stop(1, `the collective browse panel still draws ${panelReading.tables} table(s).`,
        'the panel was not moved onto the shared transcript list.',
        'the capture would show the surface this change replaces.',
        'rebuild from this worktree and retry.')
    }
    // Every column the dropped table stated must still be stated by the row.
    const states = {
      contributor: /alice-dev/,
      provider: /claude-code/,
      turns: /\d+ turns/,
      tokens: /tok\b/,
      date: /\w{3} \d{1,2}, \d{4}/,
    }
    const missing = Object.entries(states)
      .filter(([, pattern]) => !pattern.test(panelReading.rowText || ''))
      .map(([name]) => name)
    if (missing.length > 0) {
      await stop(1, `a row on the collective browse panel states no ${missing.join(', ')}: ${JSON.stringify(panelReading.rowText)}.`,
        'the shared list was given fewer facts than the table it replaced.',
        'the capture would show a list that lost information the table carried.',
        'pass every fact the table stated to the list, rebuild, and retry.')
    }
    if (panelReading.rowSelectionBoxes < 1) {
      await stop(1, 'no row on the collective browse panel carries a selection box.',
        "the owner's per-row selection did not survive the move onto the shared list.",
        'the capture would show an owner unable to pick rows out.',
        'rebuild from this worktree and retry.')
    }
  }

  if (config.verify === 'fold-sits-under-a-repository') {
    panelReading = await readReposPanel()
    if (panelReading === null || !panelReading.chipIsInside) {
      await stop(1, 'the collapsed control does not sit inside the repository view.',
        'the repository view did not open, or it does not fold the sessions one session started.',
        'the capture would be evidence about the wrong view.',
        'rebuild from this worktree and retry.')
    }
    if (panelReading.repositories < 2) {
      await stop(1, `the repository view lists ${panelReading.repositories} repositor(y/ies).`,
        'the fixture served too few repositories for this view to be worth capturing.',
        'the capture would not show a fold WITHIN a repository.',
        'serve rows on at least two repositories and retry.')
    }
  }

  await shoot(`${config.prefix}-collapsed`)

  // ── Opened ────────────────────────────────────────────────────────────────

  const opened = await page.evaluate((sel) => {
    const toggle = document.querySelector(`${sel} [data-testid="child-session-disclosure-toggle"]`)
    if (!toggle) return false
    toggle.click()
    return true
  }, chipSelector)
  if (!opened) {
    await stop(2, `the control under ${config.parentID} rendered without its button.`,
      'the shared collapsed-group control did not render.',
      'the capture would show a control nobody can open.',
      'rebuild from this worktree and retry.')
  }
  await pause(500)

  const revealedText = await page.evaluate((sel) => {
    const rows = document.querySelector(sel)
    return rows ? (rows.innerText || '').replace(/\s+/g, ' ') : null
  }, rowsSelector)
  if (revealedText === null) {
    await stop(1, 'opening the control produced no rows.',
      'the rows folded out of the list were not handed to the control.',
      'the opened capture would show an empty control.',
      'rebuild from this worktree and retry.')
  }
  const unrevealed = config.revealed.filter((title) => !revealedText.includes(title))
  if (unrevealed.length > 0) {
    await stop(1, `opening the control did not reveal ${unrevealed.map((t) => JSON.stringify(t)).join(', ')}.`,
      'the control does not hold the rows this response folded under its parent.',
      'the capture would show a count that disagrees with what opening it produces.',
      'rebuild from this worktree and retry.')
  }
  if (revealedText.includes(config.unmatchedText)) {
    await stop(1, `opening the control revealed ${JSON.stringify(config.unmatchedText)}, whose starter this response does not carry.`,
      'a row was folded under a parent the response never held.',
      'the capture would show behavior this change forbids.',
      'rebuild from this worktree and retry.')
  }

  let openedReading = null
  if (config.verifyExpanded === 'revealed-contributions-keep-their-remove-action') {
    openedReading = await readRevealedContributions()
    if (openedReading === null || openedReading.rows < 1) {
      await stop(1, 'the opened control holds no contribution rows.',
        'the revealed contributions did not render.',
        'the capture would show an empty control.',
        'rebuild from this worktree and retry.')
    }
    if (openedReading.removeActions !== openedReading.rows) {
      await stop(1, `${openedReading.rows} revealed contribution(s) carry ${openedReading.removeActions} remove action(s).`,
        'folding a contribution under another took its remove action away.',
        'the capture would show a person unable to withdraw a contribution.',
        'rebuild from this worktree and retry.')
    }
  }

  const unmatchedAfter = await readUnmatched()
  if (!unmatchedAfter.found || unmatchedAfter.insideAControl) {
    await stop(1, `${JSON.stringify(config.unmatchedText)} left its own place when the control opened.`,
      'a row whose starter this response does not carry was moved into a control.',
      'the capture would show behavior this change forbids.',
      'rebuild from this worktree and retry.')
  }

  await shoot(`${config.prefix}-expanded`)

  console.log('provenance', `control-under=${config.parentID}`,
    `collapsed-label=${JSON.stringify(collapsedLabel)}`,
    `revealed=${JSON.stringify(config.revealed)}`,
    `unmatched-row-kept=${JSON.stringify(config.unmatchedText)}`,
    panelReading ? `panel=${JSON.stringify(panelReading)}` : '',
    openedReading ? `opened=${JSON.stringify(openedReading)}` : '')
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

   Four readings, because one alone can pass while the design is wrong. The row
   that carries a chip must be one design-system step tighter UNDERNEATH, and
   must still open its original distance ABOVE, so the step cannot come off the
   wrong side and close the row up against the row above it. An ordinary row in
   the same list must be UNCHANGED, so the tightening cannot leak into every row
   in the app. And the chip's indentation must be untouched.

   A fifth reading corroborates them: the rendered gap the reader actually
   complained about. It is the only one held to a band rather than a number,
   because it is measured from laid-out boxes and moves with font metrics, and
   it has a floor as well as a ceiling so an over-tightening is caught too. It
   is not what guards this design, though. The four exact readings are, and a
   build still carrying the old spacing fails them whatever its own font
   metrics do to the gap. */
const RHYTHM = {
  tightRowPaddingTop: 12,
  tightRowPaddingBottom: 8,
  ordinaryRowPaddingBottom: 12,
  // A BAND, not a single number: see the note above. Its edges were measured on
  // this design at 22px, not derived, so a run that lands outside them is a
  // reason to re-measure and re-justify rather than to widen it again.
  detailToLabelGap: { min: 20, max: 23 },
  chipIndent: 20,
}
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
    tightRowPaddingTop: px(getComputedStyle(row).paddingTop),
    tightRowPaddingBottom: px(getComputedStyle(row).paddingBottom),
    ordinaryRowPaddingBottom: ordinary ? px(getComputedStyle(ordinary.firstElementChild).paddingBottom) : null,
    detailToLabelGap: Math.round(label.getBoundingClientRect().top - detail.getBoundingClientRect().bottom),
    chipIndent: px(getComputedStyle(el).paddingLeft),
  }
}, chip)
const rhythmFaults = []
if (rhythm.tightRowPaddingTop !== RHYTHM.tightRowPaddingTop) {
  rhythmFaults.push(
    `the row carrying the chip opens ${rhythm.tightRowPaddingTop}px above itself, not ${RHYTHM.tightRowPaddingTop}px, ` +
      'so the step came off the wrong side and the row has closed up against the row above it')
}
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
if (
  rhythm.detailToLabelGap < RHYTHM.detailToLabelGap.min ||
  rhythm.detailToLabelGap > RHYTHM.detailToLabelGap.max
) {
  rhythmFaults.push(
    `${rhythm.detailToLabelGap}px sits between the row's detail line and the chip's label, outside the ` +
      `${RHYTHM.detailToLabelGap.min}px to ${RHYTHM.detailToLabelGap.max}px this surface is held to`)
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
