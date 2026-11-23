/* Screenshot the transcript page over the positioned reader, on the standalone build.

   Surfaces (one theme per run):
     positioned-far-target       a far deep link: served numbers on the target and the gap between two runs
     positioned-no-number        a row the server sends no number for
     positioned-history-closed   the earlier-history section closed, with its recorded count
     positioned-history-open     the same section open, numbered on its own
     positioned-busy             a busy read waiting its Retry-After
     positioned-reload           a stale read asking for a reload
     positioned-snapped-turn     an unserved ?turn and the status line it shows
     positioned-restored         a return to the plain address: the saved turn, its offset and the open history
     positioned-snapped-saved    a saved position the transcript no longer serves, and the status line it shows
     positioned-download-stopped a download a revision change stopped, and the line that says so
     positioned-failure-downloads  every segment read failing, with the download menu open
     positioned-preview-contribute the contribute page's preview column
     positioned-preview-review     the review page's preview column
     positioned-graph              the graph view, drawn from the one whole-document read opening it makes

   Fails closed before any PNG: the served build must carry `transcript-mode-positioned`, the page
   response must carry `Referrer-Policy: no-referrer`, and the page must mount the positioned mode.
   Every capture passes the non-empty surface gate. Failure states are injected by intercepting the
   browser's positioned reads (a held read is left unanswered); a saved position the transcript no
   longer serves is written into the page's session store before it loads; everything else is
   served by scripts/visual/mock-rest-positioned.mjs.

   usage (the app built with NEXT_PUBLIC_API_URL pointing at the mock, served by the standalone server):
     MOCK_REST_PORT=8795 node scripts/visual/mock-rest-positioned.mjs &
     NEXT_PUBLIC_API_URL=http://localhost:8795/api/v1 pnpm build
     cp -r .next/static .next/standalone/frontend/.next/static && cp -r public .next/standalone/frontend/public
     (provenance reads that served copy, .next/standalone/frontend/.next; NEXT_DIST_DIR overrides it)
     PORT=3020 node .next/standalone/frontend/server.js &
     CHROME_PATH=... VILLAGE_URL=http://localhost:3020 node scripts/visual/positioned-transcript-shoot.mjs <theme> <outdir>
   Write captures outside the repository (for example /tmp/opencode/v417-captures). */
import { mkdirSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { SurfaceGate } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'
import { HttpMethod, POSITIONED } from '../journey/lib/positioned-transcript-fixtures.mjs'
import { SessionDetailV2TestId } from '../../src/components/session-detail/v2/readModeTestIds.ts'
import { TranscriptHeaderActionsLabel, TranscriptHeaderActionsTestId } from '../../src/components/transcript/transcriptHeaderActionsNames.ts'

const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default
const WHERE = 'positioned-transcript-shoot.mjs'
const CHROME = process.env.CHROME_PATH
const ORIGIN = (process.env.VILLAGE_URL || 'http://localhost:3020').replace(/\/$/, '')
const GROUP = process.env.MOCK_GROUP_ID || 'demo-group'
const THEME = process.argv[2] || 'dark'
const OUT = process.argv[3] || `/tmp/opencode/v417-captures/${THEME}`
// The build the standalone server serves: its server bundle and the static assets copied beside it.
const NEXT_DIR = resolve(process.env.NEXT_DIST_DIR || '.next/standalone/frontend/.next')
const MARKER = SessionDetailV2TestId.ModePositioned.wire
// unstring-allow: testid-selector-string — Puppeteer has no test-id query; this one helper builds the selector from typed members
const byTestId = (id) => `[data-testid="${id}"]`
const HEADER_ACTIONS = byTestId(TranscriptHeaderActionsTestId.Root.wire)
/** A main-partition row's text, as the fixture module generates it. */
const rowText = (position) => {
  const step = Math.floor(position / 2)
  const pool = position % 2 === 0 ? MAIN.prompts : MAIN.replies
  return `${pool[step % pool.length]} [step ${step + 1}]`
}
const MAIN = POSITIONED.main
const PREVIEW = POSITIONED.preview
const FAR_SEGMENT = Math.floor(MAIN.farTurn / MAIN.rowsPerSegment)

const fail = (message, code = 1) => {
  console.error(`ERROR [${WHERE}] ${message}`)
  process.exit(code)
}
if (!CHROME) fail('CHROME_PATH is unset; set it to your Chrome/Chromium binary.')
if (!['dark', 'light'].includes(THEME)) fail(`theme ${JSON.stringify(THEME)} is not dark | light.`)

/* Provenance: a build without this change has no positioned mode, and would capture the whole-document
   page under positioned names. */
function walk(dir) {
  const files = []
  for (const name of readdirSync(dir)) {
    const path = join(dir, name)
    const stat = statSync(path, { throwIfNoEntry: false })
    if (stat?.isDirectory()) files.push(...walk(path))
    else if (/\.(js|html|rsc)$/.test(name)) files.push(path)
  }
  return files
}
const served = [join(NEXT_DIR, 'static'), join(NEXT_DIR, 'server')].filter((dir) => { try { return statSync(dir).isDirectory() } catch { return false } })
if (!served.flatMap(walk).some((file) => readFileSync(file, 'utf8').includes(MARKER))) {
  fail(`the build in ${NEXT_DIR} does not contain ${JSON.stringify(MARKER)}; it predates this change. Rebuild from this branch and retry.`, 2)
}
console.log(`provenance ok: ${MARKER} found in ${NEXT_DIR}`)

mkdirSync(OUT, { recursive: true })
const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', defaultViewport: { width: 1460, height: 1000, deviceScaleFactor: 1 } })
const gate = new SurfaceGate(await browser.newPage())
const pause = (ms) => new Promise((r) => setTimeout(r, ms))
const errors = []

/** What an override returns to leave a read unanswered until the page aborts it. */
const HOLD = Symbol('hold the read')

/** A fresh page: signed in, theme pinned, deterministic, with an optional positioned-read override. */
async function openPage(override) {
  const page = await browser.newPage()
  await page.setCookie({ name: 'peasant_token', value: 'demo-token', domain: 'localhost', path: '/' })
  await page.evaluateOnNewDocument((theme) => localStorage.setItem('peasant-theme', theme), THEME)
  await applyDeterminism(page)
  page.on('pageerror', (e) => errors.push(`pageerror: ${e.message}`))
  if (override) {
    await page.setRequestInterception(true)
    page.on('request', (request) => {
      if (request.isInterceptResolutionHandled()) return
      // A CORS preflight goes through, so the injected status reaches the reader as an answer.
      const answer = request.method() === HttpMethod.Get ? override(new URL(request.url())) : null
      if (!answer) return request.continue()
      if (answer === HOLD) return undefined
      return request.respond({
        status: answer.status,
        contentType: 'application/json',
        headers: { 'access-control-allow-origin': '*', 'access-control-expose-headers': 'Retry-After', ...(answer.headers ?? {}) },
        // unstring-allow: json-body-object — an injected failure body the reader discards unread
        body: JSON.stringify({ error: 'injected' }),
      })
    })
  }
  return page
}
const waitFor = async (page, sel, timeoutMs = 20000) => {
  const handle = await page.waitForSelector(sel, { timeout: timeoutMs }).catch(() => null)
  if (!handle) throw new Error(`selector "${sel}" never mounted`)
  return handle
}
async function shot(page, name) {
  await pause(400)
  const path = `${OUT}/${name}-${THEME}.png`
  await page.screenshot({ path })
  const r = await gate.assert(`${name}-${THEME}`, path, { sel: 'page', where: WHERE })
  console.log('shot', name.padEnd(32), `nonbg=${(r.nonbgRatio * 100).toFixed(1)}%`)
}
const transcript = (search = '') => `${ORIGIN}/transcripts/${MAIN.transcriptId}${search}`
const segmentRead = (ordinal) => (url) => url.pathname.endsWith(`/transcripts/${MAIN.transcriptId}/segments/${ordinal}`)
const centre = (page, sel) => page.$eval(sel, (el) => el.scrollIntoView({ block: 'center' }))

try {
  // The far deep link: provenance on the live page, then numbers and the gap.
  {
    const page = await openPage()
    const response = await page.goto(transcript(`?turn=${MAIN.farTurn}`), { waitUntil: 'domcontentloaded' })
    const policy = response.headers()['referrer-policy']
    if (policy !== 'no-referrer') fail(`the transcript page answered Referrer-Policy ${JSON.stringify(policy)}; no-referrer is expected. The served build predates the policy, or next.config headers changed.`, 2)
    await waitFor(page, byTestId(MARKER))
    await waitFor(page, '.txn-segboundary-between')
    await centre(page, '.txn-segboundary-between')
    await shot(page, 'positioned-far-target')
    await page.close()
  }
  // The default open: the row with no number, the history section closed and open.
  {
    const page = await openPage()
    await page.goto(transcript(), { waitUntil: 'domcontentloaded' })
    await waitFor(page, '.txn-earlier-toggle')
    await waitFor(page, '.txn-turn')
    await shot(page, 'positioned-history-closed')
    const unnumbered = await page.waitForFunction((text) => [...document.querySelectorAll('.txn-turn')].find((el) => el.textContent.includes(text)), { timeout: 10000 }, rowText(MAIN.numberAbsent[0]))
    await unnumbered.evaluate((el) => el.scrollIntoView({ block: 'center' }))
    await shot(page, 'positioned-no-number')
    await page.$eval('.txn-earlier-toggle', (el) => el.scrollIntoView({ block: 'start' }))
    await page.click('.txn-earlier-toggle')
    await page.waitForFunction((text) => document.querySelector('.txn-earlier')?.textContent.includes(text), { timeout: 10000 }, MAIN.history[0].turns[0].content)
    await shot(page, 'positioned-history-open')
    await page.close()
  }
  // Busy and stale: the far run's segment answers 503 with Retry-After, or 409.
  for (const [name, answer, kind] of [
    ['positioned-busy', { status: 503, headers: { 'Retry-After': '120' } }, 'busy'],
    ['positioned-reload', { status: 409 }, 'stale'],
  ]) {
    const page = await openPage((url) => (segmentRead(FAR_SEGMENT)(url) ? answer : null))
    await page.goto(transcript(`?turn=${MAIN.farTurn}`), { waitUntil: 'domcontentloaded' })
    await waitFor(page, `[data-read-failure="${kind}"]`)
    await centre(page, `[data-read-failure="${kind}"]`)
    await shot(page, name)
    await page.close()
  }
  // An unserved ?turn snaps to the window's anchor and says so.
  {
    const page = await openPage()
    await page.goto(transcript(`?turn=${MAIN.unservedTurn}`), { waitUntil: 'domcontentloaded' })
    await waitFor(page, byTestId(SessionDetailV2TestId.SnappedTurn.wire))
    await shot(page, 'positioned-snapped-turn')
    await page.close()
  }
  // A return to the plain address after reading the far run with the history open.
  {
    const page = await openPage()
    await page.goto(transcript(`?turn=${MAIN.farTurn}`), { waitUntil: 'domcontentloaded' })
    await waitFor(page, '.txn-earlier-toggle')
    await page.waitForFunction((text) => document.querySelector('.txn-turn.txn-active')?.textContent.includes(text), { timeout: 10000 }, rowText(MAIN.farTurn))
    await page.click('.txn-earlier-toggle')
    await page.waitForFunction((text) => document.querySelector('.txn-earlier')?.textContent.includes(text), { timeout: 10000 }, MAIN.history[0].turns[0].content)
    // The reader brings the far row back to the top and scrolls back a little.
    const far = await page.waitForFunction((text) => [...document.querySelectorAll('.txn-turn')].find((el) => el.textContent.includes(text)), { timeout: 10000 }, rowText(MAIN.farTurn))
    await far.evaluate((row) => row.scrollIntoView({ block: 'start' }))
    await page.$eval('.txn-stream', (stream) => stream.scrollBy(0, -60))
    await page.waitForFunction((text) => document.querySelector('.txn-turn.txn-active')?.textContent.includes(text), { timeout: 10000 }, rowText(MAIN.farTurn))
    await pause(400)
    await page.goto(ORIGIN, { waitUntil: 'domcontentloaded' })
    await page.goto(transcript(), { waitUntil: 'domcontentloaded' })
    await page.waitForFunction((text) => document.querySelector('.txn-turn.txn-active')?.textContent.includes(text), { timeout: 10000 }, rowText(MAIN.farTurn))
    await page.waitForFunction((text) => document.querySelector('.txn-earlier')?.textContent.includes(text), { timeout: 10000 }, MAIN.history[0].turns[0].content)
    await shot(page, 'positioned-restored')
    await page.close()
  }
  // A saved position the transcript no longer serves snaps to the window's anchor and says so. The
  // page saves its position on the first visit; the shoot then moves the saved turn to one the
  // transcript does not serve, as a republish that drops turns would, and returns.
  {
    const page = await openPage()
    await page.goto(transcript(), { waitUntil: 'domcontentloaded' })
    await waitFor(page, '.txn-turn.txn-active')
    await pause(400)
    const moved = await page.evaluate((id, turn) => {
      const key = Object.keys(sessionStorage).find((name) => name.endsWith(id))
      if (!key) return false
      sessionStorage.setItem(key, JSON.stringify({ ...JSON.parse(sessionStorage.getItem(key)), activeTurn: turn }))
      return true
    }, MAIN.transcriptId, MAIN.unservedTurn)
    if (!moved) fail('the positioned page saved no reading position on its first visit; it must save one as its position changes.')
    await page.goto(transcript(), { waitUntil: 'domcontentloaded' })
    await waitFor(page, byTestId(SessionDetailV2TestId.SnappedTurn.wire))
    await shot(page, 'positioned-snapped-saved')
    await page.close()
  }
  // A download in flight when the reader reloads onto the transcript's current copy: the far run
  // answers stale once, the download's /content read is held, and the reload ends its epoch.
  {
    let staleSent = false
    const page = await openPage((url) => {
      if (url.pathname.endsWith(`/transcripts/${MAIN.transcriptId}/content`)) return HOLD
      if (!staleSent && segmentRead(FAR_SEGMENT)(url)) {
        staleSent = true
        return { status: 409 }
      }
      return null
    })
    await page.goto(transcript(`?turn=${MAIN.farTurn}`), { waitUntil: 'domcontentloaded' })
    await waitFor(page, '[data-read-failure="stale"]')
    await page.click(`${HEADER_ACTIONS} button[aria-label="${TranscriptHeaderActionsLabel.More.wire}"]`)
    const json = await page.waitForFunction((name) => [...document.querySelectorAll('[role="menuitem"]')].find((item) => item.textContent.trim() === name), { timeout: 10000 }, TranscriptHeaderActionsLabel.DownloadJSON.wire)
    await json.click()
    const reload = await page.waitForFunction((name) => [...document.querySelectorAll('button')].find((b) => b.textContent.trim() === name), { timeout: 10000 }, POSITIONED.failureActions.reload)
    await reload.click()
    await waitFor(page, byTestId(SessionDetailV2TestId.DownloadStopped.wire))
    await page.waitForFunction((text) => [...document.querySelectorAll('.txn-turn')].some((el) => el.textContent.includes(text)), { timeout: 10000 }, rowText(MAIN.farTurn))
    await shot(page, 'positioned-download-stopped')
    await page.close()
  }
  // Every segment read fails: the failure state, and the download menu still offered.
  {
    const page = await openPage((url) => (/\/segments\/\d+$/.test(url.pathname) ? { status: 500 } : null))
    await page.goto(transcript(), { waitUntil: 'domcontentloaded' })
    await waitFor(page, '[data-read-failure="failed"]')
    await page.click(`${HEADER_ACTIONS} button[aria-label="${TranscriptHeaderActionsLabel.More.wire}"]`)
    await waitFor(page, '[role="menu"]')
    await shot(page, 'positioned-failure-downloads')
    await page.close()
  }
  // The graph view: opening it reads the whole stored copy once, and the graph draws from it.
  {
    const page = await openPage()
    const contentReads = []
    page.on('request', (request) => { if (new URL(request.url()).pathname.endsWith(`/transcripts/${MAIN.transcriptId}/content`) && request.method() === HttpMethod.Get) contentReads.push(request.url()) })
    await page.goto(transcript(), { waitUntil: 'domcontentloaded' })
    await waitFor(page, '.txn-turn')
    if (contentReads.length !== 0) fail(`the list read /content ${contentReads.length} time(s) before the graph opened; it must read none.`)
    const graph = await page.waitForFunction((name) => [...document.querySelectorAll('button')].find((b) => b.textContent.trim() === name), { timeout: 10000 }, POSITIONED.viewButtons.graph)
    await graph.click()
    await waitFor(page, '.react-flow .react-flow__node')
    if (contentReads.length !== 1) fail(`opening the graph read /content ${contentReads.length} time(s); it must read it once.`)
    await shot(page, 'positioned-graph')
    await page.close()
  }
  // Both preview columns.
  for (const [name, route] of [['positioned-preview-contribute', 'contribute'], ['positioned-preview-review', 'review']]) {
    const page = await openPage()
    await page.goto(`${ORIGIN}/collectives/${GROUP}/${route}`, { waitUntil: 'domcontentloaded' })
    const button = await page.waitForFunction((title) => [...document.querySelectorAll('button')].find((b) => b.textContent.trim() === title), { timeout: 20000 }, PREVIEW.title)
    await button.click()
    await waitFor(page, `.on-contribute-preview ${byTestId(MARKER)}`)
    await waitFor(page, '.on-contribute-preview .txn-turn')
    if (await page.$(`.on-contribute-preview ${HEADER_ACTIONS}`)) fail(`the ${route} preview offers header actions; a preview is read-only.`)
    await shot(page, name)
    await page.close()
  }
} catch (e) {
  fail(e.message)
} finally {
  console.log('page errors:', errors.length ? errors.slice(0, 5) : 'none')
  await browser.close()
}
