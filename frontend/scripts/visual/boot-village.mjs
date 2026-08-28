/* Host-integration BOOT check (the transcript oracle's "no host-integration regression" arm, R9).

   The capture harness (village-shoot.mjs) drives a backend-free dev FIXTURE route for determinism, so it
   does NOT exercise village's REAL data path: the `/transcripts/[id]` route → React Query (`useTranscript`
   + `useTranscriptContent`) → REST `GET /transcripts/{id}` + `/transcripts/{id}/content` → the
   SessionDetailV2 adapter → Fairtrade's canonical `<TranscriptViewer>` with fairtrade's own graph
   engine. This script is that missing arm: it
   boots each REAL viewer surface against a running REST backend and asserts the composite actually renders
   through the real adapter + REST/React-Query path — so a broken REST wiring / adapter / host shell fails
   LOUD even though the fixture-route captures are green.

   It needs the app served with a REST backend that has the session. The simplest self-contained way is
   the bundled mock (no Postgres/MinIO/auth stack needed):
     MOCK_REST_PORT=8788 node scripts/visual/mock-rest.mjs &
     NEXT_PUBLIC_API_URL=http://localhost:8788/api/v1 pnpm dev &          # next dev on :3000
     CHROME_PATH=/path/to/chrome VILLAGE_TRANSCRIPT=demo node scripts/visual/boot-village.mjs
   Or point it at a real village backend that serves a viewable transcript (VILLAGE_REAL_ORIGIN +
   VILLAGE_TRANSCRIPT, or VILLAGE_REAL_URL).

   Exit codes (held PER SURFACE; first failure exits immediately): 0 = EVERY boot-arm surface rendered a
   non-empty composite; 2 = a surface's real route never mounted (its mount selector never appeared — the
   real REST/React-Query path is broken/unreachable); 1 = a surface mounted but failed the non-empty gate.

   env:
     VILLAGE_REAL_ORIGIN  origin of the running app                   (default http://localhost:3000)
     VILLAGE_TRANSCRIPT   transcript id segment of the viewer route   (default `demo`, the mock's id)
     VILLAGE_REAL_URL     full viewer URL, overrides the above
     VILLAGE_THEME        dark or light (default dark)
     VILLAGE_CAPTURE_DIR  output directory for the mounted PNG (default /tmp/village-observed-model)
     CHROME_PATH          Chrome/Chromium binary                      (required)
     PUPPETEER_CORE       explicit puppeteer-core module path         (optional)
*/
import { SurfaceGate } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'
import { mkdirSync } from 'node:fs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const CHROME = process.env.CHROME_PATH
const ORIGIN = (process.env.VILLAGE_REAL_ORIGIN || 'http://localhost:3000').replace(/\/$/, '')
const TRANSCRIPT = process.env.VILLAGE_TRANSCRIPT || 'demo'
const URL = process.env.VILLAGE_REAL_URL || `${ORIGIN}/transcripts/${encodeURIComponent(TRANSCRIPT)}`
const THEME = process.env.VILLAGE_THEME || 'dark'
const CAPTURE_DIR = process.env.VILLAGE_CAPTURE_DIR || '/tmp/village-observed-model'
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
  console.error('ERROR [boot-village.mjs] CHROME_PATH is unset — set it to your Chrome/Chromium binary.')
  process.exit(1)
}
if (!['dark', 'light'].includes(THEME)) {
  console.error(`ERROR [boot-village.mjs] VILLAGE_THEME=${JSON.stringify(THEME)} is unsupported — use "dark" or "light".`)
  process.exit(1)
}
mkdirSync(CAPTURE_DIR, { recursive: true })

/* ── BOOT-ARM REGISTRY ──────────────────────────────────────────────────────────────────────────────
   Each entry is ONE real-route surface proven through the REAL data path (adapter + REST/React-Query) —
   the arm the backend-free fixture-route captures cannot cover. Per-surface exit contract:
     2 = the surface's real route never mounted (`mount` selector never appeared) — REST/adapter path broken
     1 = the surface mounted but failed the non-empty gate (rendered blank/duplicate)
     0 = every surface mounted AND passed the non-empty gate

   To add a surface, append an entry below the seed:
     {
       id:       'unique-id',                 // log label + the non-empty-gate key (must be unique)
       url:      `${ORIGIN}/...`,             // the REAL backend-served route that reaches the surface
       mount:    '.css-selector',             // data-ready signal; absence within `timeoutMs` => exit 2
       capture:  '.css-selector',             // element screenshotted + run through the non-empty gate (=> exit 1)
       interact: async (page, pause) => {},   // OPTIONAL: clicks/scrolls to reveal the surface before capture
     }
   The seed below is the current working arm; keep it and add new arms as lifted surfaces land real routes. */
const SURFACES = [
  {
    id: 'session-detail',
    url: URL,
    // .txn-app only mounts once `useTranscriptContent` resolves and Fairtrade renders the adapted payload.
    mount: '.txn-app',
    // Capture the complete mounted shell: Village's persistent header plus the production transcript body.
    capture: 'body',
    interact: null,
  },
]

const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', defaultViewport: { width: 1460, height: 1000, deviceScaleFactor: 1 } })
const page = await browser.newPage()
await applyDeterminism(page) // reduced-motion + frozen clock/PRNG (set BEFORE any navigation) so the real render is deterministic
await page.evaluateOnNewDocument((theme) => localStorage.setItem('peasant-theme', theme), THEME)
const errs = []
page.on('console', (m) => { if (m.type() === 'error' && !/favicon|404|hydrat/.test(m.text())) errs.push(m.text()) })
page.on('pageerror', (e) => errs.push('pageerr: ' + e.message))

const pause = (ms) => new Promise((r) => setTimeout(r, ms))

/* data-ready wait: the mount selector only appears once the real adapter resolves its REST payload */
const waitFor = async (sel, timeoutMs = 12000) => {
  const start = Date.now()
  while (Date.now() - start < timeoutMs) { const el = await page.$(sel); if (el) return el; await pause(120) }
  return null
}

const gate = new SurfaceGate(page) // one gate across all arms → also rejects two arms capturing a byte-identical view

for (const s of SURFACES) {
  /* navigate to the surface's real route */
  try {
    await page.goto(s.url, { waitUntil: 'domcontentloaded' })
  } catch (e) {
    console.error(
      `ERROR [boot-village.mjs] host-integration FAILED for "${s.id}" — could not load the real viewer route ${s.url}.\n` +
      `  What failed: ${e.message}\n` +
      `  Why: the app is not reachable at that origin.\n` +
      `  Fix: serve the app with a REST backend (the bundled mock: scripts/visual/mock-rest.mjs + NEXT_PUBLIC_API_URL, or a real village backend) and set VILLAGE_REAL_ORIGIN to its origin.`
    )
    await browser.close()
    process.exit(2)
  }

  /* optional interaction to reach the surface (tab click, scroll, popover open, …) */
  if (s.interact) {
    try { await s.interact(page, pause) } catch (e) {
      console.error(`ERROR [boot-village.mjs] host-integration FAILED for "${s.id}" — interaction to reach the surface threw: ${e.message}`)
      await browser.close()
      process.exit(2)
    }
  }

  /* data-ready mount gate (exit 2) */
  const app = await waitFor(s.mount, 12000)
  if (!app) {
    console.error(
      `ERROR [boot-village.mjs] host-integration FAILED for "${s.id}" — the real viewer ("${s.mount}") never mounted at ${s.url}.\n` +
      `  What failed: the SessionDetailV2 adapter did not render Fairtrade's TranscriptViewer within 12s.\n` +
      `  Why: the real data path is broken — REST GET /transcripts/${TRANSCRIPT}/content returned nothing/an error (no backend, wrong NEXT_PUBLIC_API_URL, unknown transcript), or the adapter/host shell errored.\n` +
      `  Where: boot-village.mjs real-route data-ready wait for "${s.id}".\n` +
      `  Means: the production transcript viewer would render blank — a host-integration regression the fixture-route capture cannot catch.\n` +
      `  Fix: serve the app with a REST backend that has transcript "${TRANSCRIPT}" (the bundled mock: node scripts/visual/mock-rest.mjs + NEXT_PUBLIC_API_URL=http://localhost:<port>/api/v1) and point VILLAGE_REAL_ORIGIN at the app.\n` +
      `  console errors: ${errs.length ? JSON.stringify(errs.slice(0, 4)) : 'none'}`
    )
    await browser.close()
    process.exit(2)
  }
  await pause(800)

  const contractProbe = await page.evaluate(() => {
    const modelNodes = [...document.querySelectorAll('.txn-turnmodel')]
    const markerNodes = [...document.querySelectorAll('.txn-modelchange')]
    const body = document.querySelector('.txn-body')
    const model = modelNodes[0]
    const marker = markerNodes[0]
    const nav = document.querySelector('body > div header') || document.querySelector('header.fixed')
    return {
      documentTheme: document.documentElement.getAttribute('data-theme'),
      viewerTheme: document.querySelector('.txn-app')?.getAttribute('data-theme'),
      models: modelNodes.map((node) => node.textContent?.trim() ?? ''),
      markers: markerNodes.map((node) => node.textContent?.trim() ?? ''),
      turnCount: document.querySelectorAll('.txn-turnwrap').length,
      bodyFont: body ? getComputedStyle(body).fontFamily : null,
      modelFont: model ? getComputedStyle(model).fontFamily : null,
      markerDisplay: marker ? getComputedStyle(marker).display : null,
      persistentHeaderMounted: nav != null,
    }
  })
  const probeFailures = []
  if (contractProbe.documentTheme !== THEME || contractProbe.viewerTheme !== THEME) {
    probeFailures.push(`theme mismatch: html=${contractProbe.documentTheme}, viewer=${contractProbe.viewerTheme}, requested=${THEME}`)
  }
  if (JSON.stringify(contractProbe.models) !== JSON.stringify(EXPECTED_MODELS)) {
    probeFailures.push(`effective-model sequence ${JSON.stringify(contractProbe.models)} != ${JSON.stringify(EXPECTED_MODELS)}`)
  }
  if (JSON.stringify(contractProbe.markers) !== JSON.stringify(EXPECTED_MARKERS)) {
    probeFailures.push(`transition markers ${JSON.stringify(contractProbe.markers)} != ${JSON.stringify(EXPECTED_MARKERS)}`)
  }
  if (contractProbe.turnCount !== 5) probeFailures.push(`mounted turn count ${contractProbe.turnCount} != 5`)
  if (!contractProbe.persistentHeaderMounted) probeFailures.push('Village persistent product header is absent from the mounted shell')
  const normalizedBodyFont = contractProbe.bodyFont?.toLowerCase().replace(/[^a-z]/g, '') ?? ''
  const normalizedModelFont = contractProbe.modelFont?.toLowerCase().replace(/[^a-z]/g, '') ?? ''
  if (!normalizedBodyFont.includes('atkinsonhyperlegible')) {
    probeFailures.push(`transcript body font ${JSON.stringify(contractProbe.bodyFont)} does not use Atkinson Hyperlegible`)
  }
  if (!normalizedModelFont.includes('atkinsonhyperlegiblemono')) {
    probeFailures.push(`model chrome font ${JSON.stringify(contractProbe.modelFont)} does not use Atkinson Hyperlegible Mono`)
  }
  if (contractProbe.markerDisplay === 'none' || contractProbe.markerDisplay == null) {
    probeFailures.push(`transition marker display is ${JSON.stringify(contractProbe.markerDisplay)}`)
  }
  if (probeFailures.length > 0) {
    console.error(
      `ERROR [boot-village.mjs] mounted observed-model contract FAILED for "${s.id}".\n` +
      `  What failed: ${probeFailures.join('; ')}.\n` +
      `  Why: the released Schema/Fairtrade composition did not render the strict A, omission, B, omission fixture as A, A, B, B with one marker in the requested theme.\n` +
      `  Where: ${s.url}, production REST → React Query → SessionDetailV2 → adaptTranscript → TranscriptViewer path.\n` +
      `  Means: Village would show incorrect or unproven model attribution to transcript readers.\n` +
      `  Fix: verify the exact installed package versions, preserve absent observedModel fields in mock-rest.mjs, rebuild the standalone app, and retry both themes.`,
    )
    await browser.close()
    process.exit(1)
  }
  console.log('mounted contract probe:', JSON.stringify(contractProbe))

  /* assert the rendered surface is non-empty (not a blank skeleton) via the shared gate (exit 1) */
  const tmp = `${CAPTURE_DIR}/${s.id}-${THEME}.png`.replace(/[^\w./-]/g, '_')
  const el = await page.$(s.capture)
  const box = await el.boundingBox()
  await el.screenshot({ path: tmp, captureBeyondViewport: true })
  try {
    const r = await gate.assert(s.id, tmp, { sel: s.capture, where: 'boot-village.mjs' })
    console.log(`OK host-integration: "${s.id}" real route rendered ${s.capture} ${Math.round(box.width)}x${Math.round(box.height)} — nonbg=${(r.nonbgRatio * 100).toFixed(1)}% colors=${r.distinctColors}`)
    console.log(`CAPTURE=${tmp}`)
  } catch (e) {
    console.error(e.message)
    await browser.close()
    process.exit(1)
  }
}

console.log(`OK host-integration: all ${SURFACES.length} boot-arm surface(s) rendered non-empty through the real route.`)
console.log('console errors:', errs.length ? errs.slice(0, 4) : 'none')
await browser.close()
