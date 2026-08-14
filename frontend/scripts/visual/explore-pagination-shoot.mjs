/* Mounted `/` pagination browser evidence (one theme per run).

   Drives the REAL Explore route in a real browser through the REAL published
   Explore pager: it captures page 1, activates the "page 2" control, captures the
   busy "loading page 2; showing page 1" state, and captures the settled page 2.
   It probes the live DOM/computed semantics the pagination fix must preserve — the
   numbered pagination landmark (aria-current scoped to it), exactly one
   aria-current, aria-busy on the results while a page loads, the VISIBLE loading
   cue (present with honest text while loading, absent when settled), and the
   page-named row titles proving the displayed page actually changed. A run FAILS
   closed if the real route regressed (no page change, wrong current marker, a
   stale-page repaint, or a missing/sr-only/occluded loading cue), so a broken
   build cannot pass. Before every capture the fixed/sticky chrome is switched to
   static and scroll/focus reset to the top, so the shell header is captured once
   at the true top (never floated mid-image at the scroll offset) and cannot
   occlude the cue; a framing gate fails on any surviving fixed/sticky element or a
   shell header not at the top, and a build-provenance check proves the served
   bundle carries the visible-loading-cue change.

   Pair with mock-rest-pagination.mjs (the data + optional latency) and a running
   `next dev` whose NEXT_PUBLIC_API_URL points at that mock.

   env:
     CHROME_PATH               Chrome/Chromium binary (required)
     VILLAGE_PAGINATION_URL    app URL (default http://localhost:3111/)
     PUPPETEER_CORE            explicit puppeteer-core module path (optional)
   usage: CHROME_PATH=... node explore-pagination-shoot.mjs <theme> <outdir>
*/
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { SurfaceGate } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const CHROME = process.env.CHROME_PATH
const URL = process.env.VILLAGE_PAGINATION_URL || 'http://localhost:3111/'
const theme = process.argv[2] || 'dark'
const out = process.argv[3] || `/tmp/pagination-${theme}`
mkdirSync(out, { recursive: true })

if (!CHROME) {
  console.error('ERROR [explore-pagination-shoot.mjs] CHROME_PATH is unset — set it to your Chrome/Chromium binary.')
  process.exit(1)
}

const pause = (ms) => new Promise((r) => setTimeout(r, ms))

const browser = await puppeteer.launch({
  executablePath: CHROME,
  headless: 'new',
  defaultViewport: { width: 1396, height: 1200, deviceScaleFactor: 1 },
})
const page = await browser.newPage()
await page.setCookie({ name: 'peasant_token', value: 'demo-token', domain: 'localhost', path: '/' })
await applyDeterminism(page)
const errs = []
page.on('console', (m) => { if (m.type() === 'error' && !/favicon|404|hydrat/.test(m.text())) errs.push(m.text()) })
page.on('pageerror', (e) => errs.push('pageerr: ' + e.message))

const gate = new SurfaceGate(page)

const fail = async (message) => {
  console.error(`ERROR [explore-pagination-shoot.mjs] ${message}`)
  await browser.close()
  process.exit(1)
}

const waitFor = async (sel, timeoutMs = 15000) => {
  const start = Date.now()
  while (Date.now() - start < timeoutMs) {
    if (await page.$(sel)) return true
    await pause(120)
  }
  return false
}

// Prove the SERVED Village build actually includes the visible-loading-cue change
// (frontend commit 9246da3), not only the fixed Fairtrade bytes. The unique cue
// string "until it arrives" is emitted by page.tsx; assert it is present in a
// served JS chunk so a stale build cannot pass with an invisible/absent cue.
const CUE_MARKER = 'until it arrives'
const assertBuildProvenance = async () => {
  const srcs = await page.evaluate(() => [...document.querySelectorAll('script[src]')].map((s) => s.src))
  let found = false
  for (const src of srcs) {
    const hit = await page.evaluate(async (u, marker) => {
      try {
        const res = await fetch(u)
        if (!res.ok) return false
        return (await res.text()).includes(marker)
      } catch {
        return false
      }
    }, src, CUE_MARKER)
    if (hit) { found = true; break }
  }
  if (!found) {
    await fail(`the served JS bundle does not contain the loading-cue string ${JSON.stringify(CUE_MARKER)} from frontend commit 9246da3; the running Village build predates the visible-loading-cue change. Rebuild/serve the branch that includes 9246da3 before capturing.`)
  }
  console.log(`  build provenance: served bundle contains the 9246da3 loading-cue string ${JSON.stringify(CUE_MARKER)}`)
}

// The observable pagination state read straight off the live DOM.
const readState = () =>
  page.evaluate(() => {
    // Scope the current-page marker to the pagination landmark: the persistent
    // top-nav also marks the active route with aria-current="page", so a
    // document-wide selector would read the nav, not the pager.
    const pager = document.querySelector('nav[aria-label="pagination"]')
    const marker = pager?.querySelector('[aria-current="page"]') ?? null
    const results = document.querySelector('[data-testid="session-list-results"]')
    const status = document.querySelector('[data-testid="session-list-status"]')
    // The visible (non-sr-only) transient loading cue, distinct from the sr-only
    // polite announcer. Sighted users must see loading text while prior rows stay.
    const cue = document.querySelector('[data-testid="session-list-loading"]')
    const titles = [...document.querySelectorAll('.cex-title, .cex-trow-title')].map((el) => el.textContent?.trim() || '')
    const ids = [...document.querySelectorAll('a[href^="/transcripts/"]')].map((a) => a.getAttribute('href'))
    return {
      hasPager: !!pager,
      currentCount: pager ? pager.querySelectorAll('[aria-current="page"]').length : 0,
      currentPage: marker ? Number(marker.textContent) : null,
      ariaBusy: results ? results.getAttribute('aria-busy') : null,
      status: status ? status.textContent : null,
      loadingCue: cue ? (cue.textContent || '').trim() : null,
      loadingSrOnly: cue ? cue.classList.contains('sr-only') : null,
      titles,
      ids,
      nextDisabled: document.querySelector('button[aria-label="next page"]')?.disabled ?? null,
      prevDisabled: document.querySelector('button[aria-label="previous page"]')?.disabled ?? null,
    }
  })

try {
  await page.goto(URL, { waitUntil: 'networkidle0' })
} catch (e) {
  await fail(`could not load the real Explore route ${URL}: ${e.message}. Serve the app (next dev with NEXT_PUBLIC_API_URL pointed at mock-rest-pagination) and retry.`)
}
await page.evaluate((t) => localStorage.setItem('peasant-theme', t), theme)
await page.reload({ waitUntil: 'networkidle0' })
await pause(700)

const actualTheme = await page.evaluate(() => document.documentElement.getAttribute('data-theme'))
if (actualTheme !== theme) await fail(`requested theme "${theme}" but document is "${actualTheme}"; the theme handshake did not settle.`)

if (!(await waitFor('.cex-tcard, .cex-trow'))) await fail(`browse rows never mounted at ${URL}.`)
if (!(await waitFor('nav[aria-label="pagination"]'))) await fail('the numbered pagination landmark never rendered; the dataset must exceed one page.')

await assertBuildProvenance()

// Capture the mounted SHELL + BODY as one clean, top-framed image.
//
// fullPage / captureBeyondViewport composite a position:fixed element at the
// CURRENT SCROLL OFFSET. Because activating the bottom pager scrolls the tall
// page, that floated the Village navbar halfway down the page-2 image and let it
// occlude the loading cue. So before every capture we reset scroll + focus to the
// top and temporarily switch every fixed/sticky element to static (recording
// inline styles to restore afterward): the shell header then flows once at the
// true top, nothing composites at a scroll offset, and no fixed chrome can overlap
// the body. The shell is PRESERVED (not cropped away) — we capture the whole
// document (header + mounted route body).
const neutralizeChrome = () =>
  page.evaluate(() => {
    window.scrollTo(0, 0)
    const active = document.activeElement
    if (active && typeof active.blur === 'function') active.blur()
    const restore = []
    for (const el of document.querySelectorAll('body *')) {
      const pos = getComputedStyle(el).position
      if (pos === 'fixed' || pos === 'sticky') {
        restore.push([el, el.getAttribute('style')])
        el.style.setProperty('position', 'static', 'important')
        el.style.setProperty('transform', 'none', 'important')
      }
    }
    // The reserved top padding for the (now static) fixed navbar would double the
    // gap; drop it during capture so the shell sits flush above the body.
    const main = document.querySelector('main')
    if (main) {
      restore.push([main, main.getAttribute('style')])
      main.style.setProperty('padding-top', '0', 'important')
    }
    window.__chromeRestore = restore
  })

const restoreChrome = () =>
  page.evaluate(() => {
    for (const [el, style] of window.__chromeRestore || []) {
      if (style === null) el.removeAttribute('style')
      else el.setAttribute('style', style)
    }
    delete window.__chromeRestore
  })

// After neutralizing, prove clean framing: no fixed/sticky chrome remains (nothing
// can float mid-image) and the shell header sits at the true top.
const assertFraming = async () => {
  const frame = await page.evaluate(() => {
    let stillFixed = null
    for (const el of document.querySelectorAll('body *')) {
      const pos = getComputedStyle(el).position
      if (pos === 'fixed' || pos === 'sticky') { stillFixed = el.getAttribute('class') || el.tagName; break }
    }
    const shell = document.querySelector('a[aria-label="Village home"]')?.closest('header') || document.querySelector('header')
    const shellTop = shell ? Math.round(shell.getBoundingClientRect().top) : null
    return { stillFixed, shellPresent: !!shell, shellTop }
  })
  if (frame.stillFixed) {
    await restoreChrome()
    await fail(`a fixed/sticky element (${frame.stillFixed}) survived neutralization; it would float at the scroll offset in the capture (framing gate).`)
  }
  if (!frame.shellPresent) {
    await restoreChrome()
    await fail('the mounted shell header (Village navbar) was not found; the capture must include the shell, not a cropped body.')
  }
  if (frame.shellTop === null || frame.shellTop > 4) {
    await restoreChrome()
    await fail(`the shell header is not at the true top (top=${frame.shellTop}px); it would appear mid-image (framing gate).`)
  }
  return frame
}

const shoot = async (name, { assertCue = false } = {}) => {
  const file = join(out, `pagination-${name}-${theme}.png`)
  await neutralizeChrome()
  const frame = await assertFraming()
  let cue = null
  if (assertCue) {
    // Assert the loading cue is genuinely in the visible layout at capture time
    // (nonzero box, painted, unoccluded) — the DOM merely CONTAINING it is not
    // enough; the pixels must show it.
    cue = await page.evaluate(() => {
      const el = document.querySelector('[data-testid="session-list-loading"]')
      if (!el) return { ok: false, reason: 'cue element absent from the layout' }
      const r = el.getBoundingClientRect()
      const cs = getComputedStyle(el)
      const span = el.querySelector('span') || el
      const color = getComputedStyle(span).color
      const cx = r.left + r.width / 2
      const cy = r.top + r.height / 2
      const top = document.elementFromPoint(cx, cy)
      const unoccluded = !!top && (el === top || el.contains(top))
      const painted =
        r.width >= 40 && r.height >= 8 && cs.display !== 'none' && cs.visibility === 'visible' &&
        parseFloat(cs.opacity) >= 0.99 && color !== 'rgba(0, 0, 0, 0)' && unoccluded
      return {
        ok: painted,
        rect: { x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height) },
        display: cs.display, visibility: cs.visibility, opacity: cs.opacity, color,
        occludedBy: unoccluded ? null : (top ? (top.getAttribute('class') || top.tagName) : 'offscreen'),
        text: (el.textContent || '').trim(),
      }
    })
    if (!cue.ok) {
      await restoreChrome()
      await fail(`the visible loading cue is not paintable in the layout for capture: ${JSON.stringify(cue)}. It needs a nonzero box, display!=none, visibility:visible, opacity~1, a non-transparent color, and no occluder.`)
    }
  }
  await page.screenshot({ path: file, fullPage: true })
  await restoreChrome()
  // Fail a blank/near-empty or duplicate capture so a broken surface cannot pass.
  await gate.assert(`pagination-${name}`, file, { sel: 'body' })
  console.log(`  shot ${file} (shellTop=${frame.shellTop}px${assertCue ? `, cue ${JSON.stringify(cue.text)} @y${cue.rect.y} ${cue.rect.w}x${cue.rect.h} color=${cue.color}` : ''})`)
  return file
}

// Poll until the pager has hydrated its current-page marker before asserting.
let page1 = await readState()
const hydrateStart = Date.now()
while (Date.now() - hydrateStart < 8000 && page1.currentPage == null) {
  await pause(150)
  page1 = await readState()
}
if (page1.currentPage !== 1) await fail(`initial current page marker is ${page1.currentPage}, expected 1.`)
if (page1.currentCount !== 1) await fail(`expected exactly one aria-current on page 1, found ${page1.currentCount}.`)
if (page1.prevDisabled !== true) await fail('previous-page control must be disabled on the first page.')
if (!page1.titles.some((t) => /Page 1/i.test(t))) await fail(`page 1 rows do not name page 1: ${JSON.stringify(page1.titles.slice(0, 3))}`)
await shoot('page1')

// Activate the real "page 2" pager control.
const clicked = await page.evaluate(() => {
  const btn = document.querySelector('button[aria-label="page 2"]')
  if (!btn) return false
  btn.focus()
  btn.click()
  return true
})
if (!clicked) await fail('the "page 2" pager control was not present to activate.')

// Required: capture and assert the VISIBLE loading cue during the page-2
// transition. The mock adds latency (PAGINATION_DELAY_MS) so the pending state is
// observable; poll for it and fail closed if it never appears.
let mid = null
const midStart = Date.now()
while (Date.now() - midStart < 6000) {
  const s = await readState()
  if (s.ariaBusy === 'true' && s.currentPage === 2) { mid = s; break }
  await pause(50)
}
if (!mid) {
  await fail('never observed the page-2 pending state; set PAGINATION_DELAY_MS on mock-rest-pagination so the visible loading cue can be captured.')
}
if (!mid.loadingCue) await fail('the visible loading cue (session-list-loading) was absent during the page-2 transition; sighted users would see no loading text.')
if (mid.loadingSrOnly) await fail('the loading cue is sr-only; it must be a visible strip separate from the sr-only announcer.')
if (!/loading page 2/i.test(mid.loadingCue)) await fail(`loading cue text is not honest: ${JSON.stringify(mid.loadingCue)} (expected to name the requested page 2).`)
if (!/showing page 1/i.test(mid.loadingCue)) await fail(`loading cue must name the page still shown while loading: ${JSON.stringify(mid.loadingCue)}.`)
console.log(`  loading cue: ${JSON.stringify(mid.loadingCue)}`)
await shoot('page2-loading', { assertCue: true })

// Wait for page 2 to settle.
const settleStart = Date.now()
let page2 = await readState()
while (Date.now() - settleStart < 15000 && !(page2.currentPage === 2 && page2.titles.some((t) => /Page 2/i.test(t)) && page2.ariaBusy !== 'true')) {
  await pause(150)
  page2 = await readState()
}

if (page2.currentPage !== 2) await fail(`after activating page 2, the current marker is ${page2.currentPage} (a released Explore that copies the response page back into intent regresses here).`)
if (page2.currentCount !== 1) await fail(`expected exactly one aria-current on page 2, found ${page2.currentCount}.`)
if (!page2.titles.some((t) => /Page 2/i.test(t))) await fail(`page 2 rows do not name page 2: ${JSON.stringify(page2.titles.slice(0, 3))}`)
if (page2.titles.some((t) => /Page 1/i.test(t))) await fail(`page 1 rows are still shown after settling on page 2 (mixed/stale repaint): ${JSON.stringify(page2.titles.slice(0, 3))}`)
if (new Set(page2.ids).size !== page2.ids.length) await fail(`duplicate row ids on page 2: ${JSON.stringify(page2.ids)}`)
if (page2.loadingCue) await fail(`the visible loading cue is still present after page 2 settled: ${JSON.stringify(page2.loadingCue)} (it must clear when not loading).`)
await shoot('page2')

if (errs.length) {
  console.error(`ERROR [explore-pagination-shoot.mjs] console/page errors during capture:\n  ${errs.join('\n  ')}`)
  await browser.close()
  process.exit(1)
}

console.log(`OK [explore-pagination-shoot.mjs] ${theme}: page 1 -> page 2 verified on the mounted route (build provenance, current marker, page-named rows, unique ids, single aria-current, paintable+unoccluded loading cue present-then-cleared, shell-at-top framing).`)
await browser.close()
