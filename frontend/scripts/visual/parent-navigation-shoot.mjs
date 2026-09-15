/* Capture the mounted session-context branch-point exit, both themes.

   Drives the REAL `/transcripts/{id}` route against the bundled REST mock with
   MOCK_CONTEXT_NAV=1 (see mock-rest.mjs): the child renders two authorized
   context/starter links and the exact-anchor note, and the CURRENT context
   target serves its own turns. The script:

     1. asserts the child actually makes the branch-point promise (the exact
        anchor note is present), then captures it;
     2. clicks the actual canonical context button;
     3. waits for the client route to land on the target with the carried anchor
        query, and for the NON-FIRST referenced turn to be the active turn;
     4. captures the target at the recorded branch point.

   Step 3 is the provenance assertion: a build that pushes the bare route, or
   drops the anchor, fails here instead of producing a plausible-looking image
   of the target at its default position.

   env:
     VILLAGE_URL              app origin (default http://localhost:3000)
     CHROME_PATH              Chrome/Chromium binary (required)
     PARENT_NAV_TRANSCRIPT    child transcript route id (default `demo`)
     PARENT_NAV_TARGET_ID     current context target id (default the mock's)
     PARENT_NAV_TARGET_REF    the target turn's cooked source ref (default e_parent)
     PUPPETEER_CORE           explicit module path to puppeteer-core (optional)
   usage: VILLAGE_URL=... CHROME_PATH=... node parent-navigation-shoot.mjs <theme> <outdir>
*/
import { mkdirSync, statSync } from 'node:fs'
import { SurfaceGate } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const theme = process.argv[2] || 'dark'
const out = process.argv[3] || `/tmp/parent-navigation-${theme}`
const CHROME = process.env.CHROME_PATH
const ORIGIN = (process.env.VILLAGE_URL || 'http://localhost:3000').replace(/\/$/, '')
const CHILD = process.env.PARENT_NAV_TRANSCRIPT || 'demo'
const TARGET = process.env.PARENT_NAV_TARGET_ID || 'e0e0e0e0-0000-4000-8000-000000000011'
const TARGET_REF = process.env.PARENT_NAV_TARGET_REF || 'e_parent'
const NOTE = 'opens the current source at the recorded branch point'

const die = (code, what, why, means, fix) => {
  console.error(
    `ERROR [parent-navigation-shoot.mjs] ${what}
  Why: ${why}
  Where: parent-navigation-shoot.mjs, theme=${theme}, origin=${ORIGIN}, child=${CHILD}, target=${TARGET}.
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

mkdirSync(out, { recursive: true })

const BASE_VP = { width: 1396, height: 939, deviceScaleFactor: 1 }
const pause = (ms) => new Promise((r) => setTimeout(r, ms))

const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', defaultViewport: { ...BASE_VP } })
const page = await browser.newPage()
await applyDeterminism(page)
const errs = []
// The mock answers unmatched routes with 401 by design (an anonymous viewer
// still renders the transcript; auth-only chrome stays empty), so those are
// expected here, alongside the favicon/404/hydration noise other harnesses skip.
page.on('console', (m) => { if (m.type() === 'error' && !/favicon|404|401|Unauthorized|hydrat|collectives/.test(m.text())) errs.push(m.text()) })
page.on('pageerror', (e) => errs.push('pageerr: ' + e.message))

const stop = async (...args) => { await browser.close(); die(...args) }

await page.goto(`${ORIGIN}/transcripts/${encodeURIComponent(CHILD)}`, { waitUntil: 'networkidle0' })
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

/* Structural: the child must actually mount the composer and its context links. */
{
  let ready = false
  const s = Date.now()
  while (Date.now() - s < 15000) { if (await page.$('.txn-context-link')) { ready = true; break } await pause(150) }
  if (!ready) await stop(4, 'the child never rendered a ".txn-context-link" — the mocked navigation did not cook source/starter links.',
    'MOCK_CONTEXT_NAV=1 must be set on the mock, and the transcript must carry its durable relationships.',
    'the branch-point exit cannot be captured.',
    'start the mock with MOCK_CONTEXT_NAV=1 and confirm the route serves relationshipNavigation.')
  const note = await page.evaluate(() => document.querySelector('.txn-context-note')?.textContent ?? '')
  if (!note.includes(NOTE)) await stop(5, `the exact-anchor promise is missing from the child's context note (got: ${JSON.stringify(note)}).`,
    'the child is not promising an exact branch point, so there is no exact exit to verify.',
    'the capture would not test the finding.',
    'confirm the context navigation resolves to an exact anchor (matching public revision).')
}

const gate = new SurfaceGate(page)
const shot = async (name, sel) => {
  const el = await page.$(sel)
  if (!el) throw new Error(`"${sel}" is not mounted for ${name}`)
  const box = await el.boundingBox()
  if (!box || box.width < 4 || box.height < 4) throw new Error(`"${sel}" blank/zero-size for ${name}: ${JSON.stringify(box)}`)
  await el.screenshot({ path: `${out}/${name}.png`, captureBeyondViewport: true })
  await gate.assert(name, `${out}/${name}.png`, { sel, where: 'parent-navigation-shoot.mjs' })
  console.log('shot', name.padEnd(34), `${Math.round(box.width)}x${Math.round(box.height)}`, `${(statSync(`${out}/${name}.png`).size / 1024).toFixed(1)}KB`)
}

await shot(`session-context-child-${theme}`, '.txn-app')

/* Follow the ACTUAL canonical context button; the client route must land on the
   current target with the carried anchor and select the referenced turn. */
await page.evaluate(() => document.querySelector('.txn-context-link')?.click())
{
  let landed = false
  const s = Date.now()
  while (Date.now() - s < 15000) {
    const url = page.url()
    if (url.includes(`/transcripts/${TARGET}`) && url.includes('entry=')) { landed = true; break }
    await pause(150)
  }
  if (!landed) await stop(6, `the context button did not open the target with a carried anchor (url=${page.url()}).`,
    'the host pushed the bare current-target route instead of carrying the verified branch point.',
    'the finding this capture exists to disprove is still present.',
    'carry the exact anchor through host navigation and retry.')
}
{
  // The target must land ON the named turn, not at the transcript top: the
  // anchored turnwrap has to sit in the stream's top band (where a reader
  // arrives after the one-time position). A discarded anchor leaves the stream
  // at 0 with the referenced turn far below the fold.
  let landed = false
  const s = Date.now()
  while (Date.now() - s < 15000) {
    const position = await page.evaluate((ref) => {
      const wrap = document.querySelector(`.txn-turnwrap[data-source-entry-ref='${ref}']`)
      const stream = document.querySelector('.txn-stream')
      if (!wrap || !stream) return null
      const wrapRect = wrap.getBoundingClientRect()
      const streamRect = stream.getBoundingClientRect()
      return {
        turn: wrap.getAttribute('data-turn'),
        offset: wrapRect.top - streamRect.top,
        height: streamRect.height,
        scrollTop: stream.scrollTop,
        active: !!wrap.querySelector('.txn-turn.txn-active'),
      }
    }, TARGET_REF)
    if (position && position.turn !== '0' && position.offset <= position.height * 0.4) { landed = true; console.log('anchor position:', JSON.stringify(position)); break }
    await pause(150)
  }
  if (!landed) {
    const diag = await page.evaluate(() => ({
      url: window.location.href,
      active: [...document.querySelectorAll('.txn-turnwrap')].map((w) => ({
        turn: w.getAttribute('data-turn'),
        ref: w.getAttribute('data-source-entry-ref'),
        active: !!w.querySelector('.txn-turn.txn-active'),
      })),
      streamScrollTop: document.querySelector('.txn-stream')?.scrollTop ?? null,
      hasApp: !!document.querySelector('.txn-app'),
    }))
    await stop(7, `the target did not open at the recorded branch turn (${TARGET_REF}). diagnostics=${JSON.stringify(diag)}`,
      'the carried anchor was not resolved against the target\'s rendered turns.',
      'the reader lands at the target default, not the recorded branch point.',
      'resolve the anchor against the cooked target turns and retry.')
  }
}
await pause(400)
await shot(`session-context-anchor-target-${theme}`, '.txn-app')

console.log(`\nTHEME=${theme} active=[data-theme]=${await page.evaluate(() => document.documentElement.getAttribute('data-theme'))}`)
console.log('target url:', page.url())
console.log('console errors:', errs.length ? errs.slice(0, 6) : 'none')
await browser.close()
if (errs.length) {
  console.error(`\nEXIT 1: ${errs.length} console error(s) during the captured run.`)
  process.exit(1)
}
