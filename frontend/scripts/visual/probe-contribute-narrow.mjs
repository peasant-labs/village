/* Computed-style probe for the `/groups/{id}/contribute` page's narrow layout.

   Close-value layouts (a single grid column vs. two) are indistinguishable in a
   scaled PNG, so this probe reads `getComputedStyle` directly at both sides of
   the page's 880px @container breakpoint and asserts the grid ACTUALLY
   switches from a single implicit column to the two explicit
   `minmax(20rem, 2fr) 3fr` columns, rather than trusting the screenshot alone.

   usage:
     MOCK_REST_PORT=8788 node scripts/visual/mock-rest.mjs &
     NEXT_PUBLIC_API_URL=http://localhost:8788/api/v1 pnpm dev &
     CHROME_PATH=... node scripts/visual/probe-contribute-narrow.mjs
*/
import puppeteer from 'puppeteer-core'
import { applyDeterminism } from './determinism.mjs'

const CHROME = process.env.CHROME_PATH
const ORIGIN = (process.env.VILLAGE_URL || 'http://localhost:3000').replace(/\/$/, '')
const GROUP_ID = process.env.VILLAGE_GROUP_ID || 'demo-group'

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
  await page.goto(`${ORIGIN}/groups/${encodeURIComponent(GROUP_ID)}/contribute`, { waitUntil: 'domcontentloaded' })
  await pause(900)
  return page.evaluate(() => {
    const grid = document.querySelector('.cmg-root .grid')
    if (!grid) return null
    const style = getComputedStyle(grid)
    return {
      gridTemplateColumns: style.gridTemplateColumns,
      columnCount: style.gridTemplateColumns.split(' ').filter(Boolean).length,
    }
  })
}

const narrow = await readGrid(700)
const wide = await readGrid(1000)
await browser.close()

console.log('narrow (700px):', JSON.stringify(narrow))
console.log('wide (1000px):', JSON.stringify(wide))

if (!narrow || !wide) {
  console.error('ERROR [probe-contribute-narrow.mjs] the contribute grid never mounted (".cmg-root .grid" not found).')
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
