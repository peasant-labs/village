/* Host-integration BOOT check (the transcript oracle's "no host-integration regression" arm, R9).

   The capture harness (village-shoot.mjs) drives a backend-free dev FIXTURE route for determinism, so it
   does NOT exercise village's REAL data path: the `/transcripts/[id]` route → React Query (`useTranscript`
   + `useTranscriptContent`) → REST `GET /transcripts/{id}` + `/transcripts/{id}/content` → the
   SessionDetailV2 adapter → the shared `<SessionDetail>` composer. This script is that missing arm: it
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
     CHROME_PATH          Chrome/Chromium binary                      (required)
     PUPPETEER_CORE       explicit puppeteer-core module path         (optional)
*/
import { SurfaceGate } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const CHROME = process.env.CHROME_PATH
const ORIGIN = (process.env.VILLAGE_REAL_ORIGIN || 'http://localhost:3000').replace(/\/$/, '')
const TRANSCRIPT = process.env.VILLAGE_TRANSCRIPT || 'demo'
const URL = process.env.VILLAGE_REAL_URL || `${ORIGIN}/transcripts/${encodeURIComponent(TRANSCRIPT)}`

if (!CHROME) {
  console.error('ERROR [boot-village.mjs] CHROME_PATH is unset — set it to your Chrome/Chromium binary.')
  process.exit(1)
}

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
    // .tb-detail only mounts once `useTranscriptContent` resolves the REST /content payload
    mount: '.tb-detail',
    capture: '.tb-detail',
    interact: null,
  },
]

const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', defaultViewport: { width: 1460, height: 1000, deviceScaleFactor: 1 } })
const page = await browser.newPage()
await applyDeterminism(page) // reduced-motion + frozen clock/PRNG (set BEFORE any navigation) so the real render is deterministic
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
    await page.goto(s.url, { waitUntil: 'networkidle0' })
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
      `  What failed: the SessionDetailV2 adapter did not render a composite within 12s.\n` +
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

  /* assert the rendered surface is non-empty (not a blank skeleton) via the shared gate (exit 1) */
  const tmp = `/tmp/village-boot-${s.id}.png`.replace(/[^\w./-]/g, '_')
  const el = await page.$(s.capture)
  const box = await el.boundingBox()
  await el.screenshot({ path: tmp, captureBeyondViewport: true })
  try {
    const r = await gate.assert(s.id, tmp, { sel: s.capture, where: 'boot-village.mjs' })
    console.log(`OK host-integration: "${s.id}" real route rendered ${s.capture} ${Math.round(box.width)}x${Math.round(box.height)} — nonbg=${(r.nonbgRatio * 100).toFixed(1)}% colors=${r.distinctColors}`)
  } catch (e) {
    console.error(e.message)
    await browser.close()
    process.exit(1)
  }
}

console.log(`OK host-integration: all ${SURFACES.length} boot-arm surface(s) rendered non-empty through the real route.`)
console.log('console errors:', errs.length ? errs.slice(0, 4) : 'none')
await browser.close()
