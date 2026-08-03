/* Probe the village visual-harness route DOM so the capture script targets the right selectors:
   prints the tab + view-toggle labels, the composer box sizes, and the page scroll metrics. The shared
   `<SessionDetail>` composer the village frontend renders uses `.tb-*` classes (the `.tb-detail` root,
   `.tb-canvas` trace column, `.tb-scorecard`, `.tb-detail-graphwrap`, the `.tb-segmented-btn` list/graph
   toggle, the `.tb-stickyhead` sticky condensed header) and scrolls the PAGE (it is not a height-bounded
   inner-scroller), so the document overflows the viewport and the sticky header reveals on window scroll.
   Run this first whenever the harness route or the shared composite changes.

   env: VILLAGE_URL (default http://localhost:3000/dev/visual-harness), CHROME_PATH (required),
        PUPPETEER_CORE (optional explicit module path to puppeteer-core if a bare import won't resolve). */
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const CHROME = process.env.CHROME_PATH
const URL = process.env.VILLAGE_URL || 'http://localhost:3000/dev/visual-harness'

const browser = await puppeteer.launch({
  executablePath: CHROME,
  headless: 'new',
  defaultViewport: { width: 1460, height: 1000, deviceScaleFactor: 1 },
})
const page = await browser.newPage()
await page.emulateMediaFeatures([{ name: 'prefers-reduced-motion', value: 'reduce' }])
const errs = []
page.on('console', (m) => { if (m.type() === 'error' && !/favicon|hydrat/.test(m.text())) errs.push(m.text()) })
page.on('pageerror', (e) => errs.push('pageerr: ' + e.message))
await page.goto(URL, { waitUntil: 'networkidle0' })
await new Promise((r) => setTimeout(r, 1200))

const report = await page.evaluate(() => {
  const box = (sel) => { const el = document.querySelector(sel); if (!el) return null; const b = el.getBoundingClientRect(); return { w: Math.round(b.width), h: Math.round(b.height), y: Math.round(b.y) } }
  const count = (sel) => document.querySelectorAll(sel).length
  const tabs = [...document.querySelectorAll('.txn-tab, .tb-tabstrip-tab')].map((t) => t.textContent.trim())
  const viewtoggle = [...document.querySelectorAll('.tb-segmented-btn')].map((t) => t.textContent.trim())
  const themeBtn = !!document.querySelector('.theme-btn')
  const dataTheme = document.querySelector('[data-theme]')?.getAttribute('data-theme')
  return {
    dataTheme,
    themeBtn,
    boxes: {
      '.tb-root': box('.tb-root'),
      '.tb-detail': box('.tb-detail'),
      '.tb-detail-main': box('.tb-detail-main'),
      '.tb-canvas': box('.tb-canvas'),
      '.tb-detail-railwrap': box('.tb-detail-railwrap'),
      '.tb-scorecard': box('.tb-scorecard'),
      '.tb-outline': box('.tb-outline'),
    },
    counts: {
      '.txn-tab': count('.txn-tab, .tb-tabstrip-tab'),
      '.tb-segmented-btn': count('.tb-segmented-btn'),
      '.txn-viewsw': count('.txn-viewsw'),
      '.tb-scorecard': count('.tb-scorecard'),
      '.tb-stickyhead': count('.tb-stickyhead'),
    },
    tabs,
    viewtoggle,
    // The composer scrolls the PAGE (the document overflows the viewport) — the signal that there is a
    // full trace to grow the viewport into, and that the sticky header can reveal on window scroll.
    pageScroll: { scrollHeight: document.documentElement.scrollHeight, innerHeight: window.innerHeight },
  }
})
console.log(JSON.stringify(report, null, 2))
console.log('console errors:', errs.length ? errs.slice(0, 8) : 'none')
await browser.close()
