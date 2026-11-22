/* Computed-style probe for the `/collectives/{id}/contribute` page's narrow layout.

   Close-value layouts (a single grid column vs. two) are indistinguishable in a
   scaled PNG, so this probe reads `getComputedStyle` directly at both sides of
   the page's 880px @container breakpoint and asserts the grid ACTUALLY
   switches from a single column to two, rather than trusting the screenshot
   alone.

   The breakpoint is a container query, so what decides the column count is
   the width of the page's own container, not the viewport: the app's sidebar
   takes its share first. The probe therefore reads the container's width
   too, and refuses to judge a side whose container did not actually land on
   that side of 880px.

   usage:
     MOCK_REST_PORT=8788 node scripts/visual/mock-rest.mjs &
     NEXT_PUBLIC_API_URL=http://localhost:8788/api/v1 pnpm dev &
     CHROME_PATH=... node scripts/visual/probe-contribute-narrow.mjs
*/
import puppeteer from 'puppeteer-core'
import { applyDeterminism } from './determinism.mjs'

const CHROME = process.env.CHROME_PATH
const ORIGIN = (process.env.VILLAGE_URL || 'http://localhost:3000').replace(/\/$/, '')
const GROUP_ID = process.env.VILLAGE_GROUP_ID || '9a11e000-0000-4000-8000-000000000001'

if (!CHROME) {
  console.error('ERROR [probe-contribute-narrow.mjs] CHROME_PATH is unset - set it to your Chrome/Chromium binary.')
  process.exit(1)
}

const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new' })
const page = await browser.newPage()
await page.setCookie({ name: 'peasant_token', value: 'demo-token', domain: 'localhost', path: '/' })
await applyDeterminism(page)
const pause = (ms) => new Promise((r) => setTimeout(r, ms))

async function readGrid(width) {
  await page.setViewport({ width, height: 1000, deviceScaleFactor: 1 })
  await page.goto(`${ORIGIN}/collectives/${encodeURIComponent(GROUP_ID)}/contribute`, { waitUntil: 'domcontentloaded' })
  // The grid mounts once the collective's read answers, which under a dev
  // server's first compile can take well past any fixed pause.
  await page.waitForSelector('.on-contribute-columns', { timeout: 60000 }).catch(() => {})
  await pause(300)
  return page.evaluate(() => {
    const grid = document.querySelector('.on-contribute-columns')
    if (!grid) return null
    let container = grid.parentElement
    while (container && getComputedStyle(container).containerType === 'normal') container = container.parentElement
    const style = getComputedStyle(grid)
    return {
      containerWidth: Math.round((container ?? grid).getBoundingClientRect().width),
      gridTemplateColumns: style.gridTemplateColumns,
      columnCount: style.gridTemplateColumns.split(' ').filter(Boolean).length,
    }
  })
}

const narrow = await readGrid(700)
const wide = await readGrid(1460)
await browser.close()

console.log('narrow (700px):', JSON.stringify(narrow))
console.log('wide (1460px):', JSON.stringify(wide))

if (!narrow || !wide) {
  console.error('ERROR [probe-contribute-narrow.mjs] the contribute grid never mounted (".on-contribute-columns" not found).')
  process.exit(1)
}
if (narrow.containerWidth >= 880 || wide.containerWidth < 880) {
  console.error(`ERROR [probe-contribute-narrow.mjs] the viewports did not straddle the breakpoint: the container measured ${narrow.containerWidth}px narrow and ${wide.containerWidth}px wide, and the switch is at 880px.`)
  process.exit(1)
}
if (narrow.columnCount !== 1) {
  console.error(`ERROR [probe-contribute-narrow.mjs] expected ONE grid column below 880px, got ${narrow.columnCount} (${narrow.gridTemplateColumns}).`)
  process.exit(1)
}
if (wide.columnCount !== 2) {
  console.error(`ERROR [probe-contribute-narrow.mjs] expected TWO grid columns at/above 880px, got ${wide.columnCount} (${wide.gridTemplateColumns}).`)
  process.exit(1)
}
console.log('PASS: the contribute page grid is one column below 880px and two columns at/above it.')
