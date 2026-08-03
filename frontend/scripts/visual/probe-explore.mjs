/* Probe the village Explore route DOM so the capture script targets the right selectors.

   Prints the shell + surface boxes, the browse-card counts, and the scroll metrics.
   The route is the real app root (`/`), which renders the shared Explore surface.

   env: VILLAGE_URL (default http://localhost:3000/), CHROME_PATH (required),
        PUPPETEER_CORE (optional explicit module path to puppeteer-core if a bare import won't resolve).
*/
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const CHROME = process.env.CHROME_PATH
const URL = process.env.VILLAGE_URL || 'http://localhost:3000/'
const theme = process.argv[2] || 'dark'

if (!CHROME) {
  console.error('ERROR [probe-explore.mjs] CHROME_PATH is unset — set it to your Chrome/Chromium binary.')
  process.exit(1)
}

const browser = await puppeteer.launch({
  executablePath: CHROME,
  headless: 'new',
  defaultViewport: { width: 1396, height: 939, deviceScaleFactor: 1 },
})
const page = await browser.newPage()
await page.goto(URL, { waitUntil: 'networkidle0' })
await page.evaluate((nextTheme) => localStorage.setItem('peasant-theme', nextTheme), theme)
await page.reload({ waitUntil: 'networkidle0' })
await new Promise((r) => setTimeout(r, 900))

const report = await page.evaluate(() => {
  const box = (sel) => {
    const el = document.querySelector(sel)
    if (!el) return null
    const b = el.getBoundingClientRect()
    return { w: Math.round(b.width), h: Math.round(b.height), y: Math.round(b.y) }
  }
  const count = (sel) => document.querySelectorAll(sel).length
  return {
    dataTheme: document.querySelector('[data-theme]')?.getAttribute('data-theme'),
    boxes: {
      body: box('body'),
      main: box('main'),
      exploreRoot: box('.cex-root'),
      exploreHead: box('.cex-explore-head'),
      exploreBody: box('.cex-explore-body'),
      rail: box('.cex-rail'),
      results: box('.cex-results'),
      grid: box('.cex-grid'),
      list: box('.cex-list'),
      empty: box('.cex-empty'),
    },
    counts: {
      cards: count('.cex-tcard'),
      rows: count('.cex-trow'),
      collectives: count('.cex-ccard'),
      topics: count('.cex-topic'),
      pagination: count('.cex-pgn'),
    },
    pageScroll: { scrollHeight: document.documentElement.scrollHeight, innerHeight: window.innerHeight },
  }
})

console.log(JSON.stringify(report, null, 2))
await browser.close()
