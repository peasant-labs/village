/* Computed-style and data-contract probe for the mounted Village transcript route.

   Run against the production standalone server plus scripts/visual/mock-rest.mjs.
   The probe fails unless the exact A, omission, B, omission source fixture renders
   effective A, A, B, B with one transition marker through Fairtrade's adapter.

   env: VILLAGE_URL (default http://localhost:3000/transcripts/demo),
        VILLAGE_THEME (dark or light, default dark), CHROME_PATH (required),
        PUPPETEER_CORE (optional explicit puppeteer-core module path). */
import { applyDeterminism } from './determinism.mjs'

const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default
const CHROME = process.env.CHROME_PATH
const URL = process.env.VILLAGE_URL || 'http://localhost:3000/transcripts/demo'
const THEME = process.env.VILLAGE_THEME || 'dark'
const EXPECTED_MODELS = [
  'anthropic/claude-fable-5',
  'anthropic/claude-fable-5',
  'anthropic/claude-opus-4-8',
  'anthropic/claude-opus-4-8',
]
const EXPECTED_MARKERS = [
  'model changed: anthropic/claude-fable-5 -> anthropic/claude-opus-4-8',
]

if (!CHROME) {
  console.error('ERROR [probe-village.mjs] CHROME_PATH is unset — set it to the Chrome/Chromium binary used for mounted evidence.')
  process.exit(1)
}
if (!['dark', 'light'].includes(THEME)) {
  console.error(`ERROR [probe-village.mjs] VILLAGE_THEME=${JSON.stringify(THEME)} is unsupported — use "dark" or "light".`)
  process.exit(1)
}

const browser = await puppeteer.launch({
  executablePath: CHROME,
  headless: 'new',
  defaultViewport: { width: 1460, height: 1000, deviceScaleFactor: 1 },
})
const page = await browser.newPage()
await applyDeterminism(page)
await page.evaluateOnNewDocument((theme) => localStorage.setItem('peasant-theme', theme), THEME)
const errs = []
page.on('console', (message) => {
  if (message.type() === 'error' && !/favicon|404|hydrat/.test(message.text())) errs.push(message.text())
})
page.on('pageerror', (error) => errs.push(`pageerr: ${error.message}`))

try {
  await page.goto(URL, { waitUntil: 'domcontentloaded' })
  await page.waitForSelector('.txn-app', { timeout: 12_000 })
  await new Promise((resolve) => setTimeout(resolve, 500))

  const report = await page.evaluate(() => {
    const box = (selector) => {
      const element = document.querySelector(selector)
      if (!element) return null
      const bounds = element.getBoundingClientRect()
      return { width: Math.round(bounds.width), height: Math.round(bounds.height), y: Math.round(bounds.y) }
    }
    const stream = document.querySelector('.txn-stream')
    const body = document.querySelector('.txn-body')
    const model = document.querySelector('.txn-turnmodel')
    const marker = document.querySelector('.txn-modelchange')
    return {
      documentTheme: document.documentElement.getAttribute('data-theme'),
      viewerTheme: document.querySelector('.txn-app')?.getAttribute('data-theme'),
      boxes: {
        app: box('.txn-app'),
        stream: box('.txn-stream'),
        firstTurn: box('.txn-turnwrap'),
        marker: box('.txn-modelchange'),
      },
      counts: {
        turns: document.querySelectorAll('.txn-turnwrap').length,
        models: document.querySelectorAll('.txn-turnmodel').length,
        markers: document.querySelectorAll('.txn-modelchange').length,
      },
      models: [...document.querySelectorAll('.txn-turnmodel')].map((node) => node.textContent?.trim() ?? ''),
      markers: [...document.querySelectorAll('.txn-modelchange')].map((node) => node.textContent?.trim() ?? ''),
      streamScroll: stream ? { scrollHeight: stream.scrollHeight, clientHeight: stream.clientHeight } : null,
      styles: {
        bodyFont: body ? getComputedStyle(body).fontFamily : null,
        bodyLineHeight: body ? getComputedStyle(body).lineHeight : null,
        modelFont: model ? getComputedStyle(model).fontFamily : null,
        markerDisplay: marker ? getComputedStyle(marker).display : null,
        markerColor: marker ? getComputedStyle(marker).color : null,
      },
      persistentHeaderMounted: document.querySelector('header.fixed') != null,
    }
  })
  console.log(JSON.stringify(report, null, 2))

  const failures = []
  if (report.documentTheme !== THEME || report.viewerTheme !== THEME) {
    failures.push(`theme mismatch: html=${report.documentTheme}, viewer=${report.viewerTheme}, requested=${THEME}`)
  }
  if (report.counts.turns !== 5) failures.push(`mounted turn count ${report.counts.turns} != 5`)
  if (JSON.stringify(report.models) !== JSON.stringify(EXPECTED_MODELS)) {
    failures.push(`effective models ${JSON.stringify(report.models)} != ${JSON.stringify(EXPECTED_MODELS)}`)
  }
  if (JSON.stringify(report.markers) !== JSON.stringify(EXPECTED_MARKERS)) {
    failures.push(`markers ${JSON.stringify(report.markers)} != ${JSON.stringify(EXPECTED_MARKERS)}`)
  }
  const normalizedBodyFont = report.styles.bodyFont?.toLowerCase().replace(/[^a-z]/g, '') ?? ''
  const normalizedModelFont = report.styles.modelFont?.toLowerCase().replace(/[^a-z]/g, '') ?? ''
  if (!normalizedBodyFont.includes('atkinsonhyperlegible')) {
    failures.push(`body font ${JSON.stringify(report.styles.bodyFont)} is not Atkinson Hyperlegible`)
  }
  if (!normalizedModelFont.includes('atkinsonhyperlegiblemono')) {
    failures.push(`model font ${JSON.stringify(report.styles.modelFont)} is not Atkinson Hyperlegible Mono`)
  }
  if (report.styles.markerDisplay === 'none' || report.styles.markerDisplay == null) {
    failures.push(`marker display is ${JSON.stringify(report.styles.markerDisplay)}`)
  }
  if (!report.persistentHeaderMounted) failures.push('Village persistent product header is absent')
  if (errs.length > 0) failures.push(`browser console errors: ${JSON.stringify(errs.slice(0, 4))}`)

  if (failures.length > 0) {
    throw new Error(
      `mounted production-route probe failed: ${failures.join('; ')}. Verify registry package versions, rebuild the standalone app from this branch, and retry with the mock REST fixture.`,
    )
  }
  console.log(`OK mounted observed-model probe: ${THEME} production route preserves A,A,B,B and one transition marker.`)
} catch (error) {
  console.error(`ERROR [probe-village.mjs] ${error.message}`)
  await browser.close()
  process.exit(1)
}

await browser.close()
