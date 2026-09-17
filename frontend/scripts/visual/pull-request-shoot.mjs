/* Screenshot the pull request page, `/pulls/{owner}/{name}/{number}` (#113).

   Captures per theme, from the same live page:
     pr-pull-request-page     the whole page: header, digest header line, skills,
                              the chain, and the attached transcripts
     pr-pull-request-actions  the author-only confirm/detach panel

   Build provenance is asserted BEFORE anything is written: the served page must
   carry the pull request title, a digest header line, a commit anchor linking to
   github.com, and a prompt link carrying `?turn=`. A stale server, or one serving
   a different worktree, fails with a nonzero exit instead of producing a
   misleading PNG.

   Computed styles are probed and printed alongside: --surface vs --canvas and
   --ink-2 vs --ink-3 are indistinguishable in a scaled PNG, so the token check
   is a DOM assertion, never an eyeball.

   env:
     VILLAGE_URL     the pulls route on the running app (required)
     CHROME_PATH     Chrome/Chromium binary (required)
     PUPPETEER_CORE  explicit module path to puppeteer-core (optional)
   usage: VILLAGE_URL=... CHROME_PATH=... node pull-request-shoot.mjs <theme> <outdir>
*/
import { mkdirSync, statSync, writeFileSync } from 'node:fs'
import { SurfaceGate, MIN_NONBG_RATIO, MIN_DISTINCT_COLORS } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const CHROME = process.env.CHROME_PATH
const TARGET_URL = process.env.VILLAGE_URL
const theme = process.argv[2] || 'dark'
const out = process.argv[3] || `/tmp/pull-request-${theme}`
mkdirSync(out, { recursive: true })

const VP = { width: 1396, height: 1200, deviceScaleFactor: 1 }

const die = (code, what, why, means, fix) => {
  console.error(
    `ERROR [pull-request-shoot.mjs] ${what}\n  Why: ${why}\n  Where: pull-request-shoot.mjs, theme=${theme}, url=${TARGET_URL}.\n  Means: ${means}\n  Fix: ${fix}`,
  )
  process.exit(code)
}

if (!CHROME) die(1, 'CHROME_PATH is unset.', 'the script drives a real Chrome.', 'no capture can be taken.', 'set CHROME_PATH and retry.')
if (!TARGET_URL) die(1, 'VILLAGE_URL is unset.', 'the route is keyed on owner/name/number, so there is no safe default.', 'no capture can be taken.', 'set VILLAGE_URL to /pulls/{owner}/{name}/{number}.')

const parsed = new URL(TARGET_URL)
const match = /^\/pulls\/([^/]+)\/([^/]+)\/(\d+)\/?$/.exec(parsed.pathname)
if (!match) {
  die(1, `VILLAGE_URL path ${JSON.stringify(parsed.pathname)} is not the pull request route.`,
    'this change adds /pulls/{owner}/{name}/{number}.', 'the capture would prove nothing about this change.',
    'point VILLAGE_URL at the pulls route on the running app.')
}
const [, owner, name, number] = match

const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', defaultViewport: { ...VP } })
const page = await browser.newPage()
await page.setCookie({ name: 'peasant_token', value: 'demo-token', domain: 'localhost', path: '/' })
await applyDeterminism(page)
const errs = []
page.on('console', (m) => { if (m.type() === 'error' && !/favicon|404|hydrat/.test(m.text())) errs.push(m.text()) })
page.on('pageerror', (e) => errs.push('pageerr: ' + e.message))

await page.goto(TARGET_URL, { waitUntil: 'networkidle0' })
await page.evaluate((nextTheme) => localStorage.setItem('peasant-theme', nextTheme), theme)
await page.reload({ waitUntil: 'networkidle0' })
await new Promise((r) => setTimeout(r, 900))

const actualTheme = await page.evaluate(() => document.documentElement.getAttribute('data-theme'))
if (actualTheme !== theme) {
  await browser.close()
  die(3, `the requested theme did not apply: [data-theme]="${actualTheme}".`,
    'the theme handshake did not settle.', 'every capture would be the wrong theme.',
    'confirm the root layout uses the shared theme hook and retry.')
}

const pause = (ms) => new Promise((r) => setTimeout(r, ms))
const gate = new SurfaceGate(page)
const waitFor = async (sel, timeoutMs = 12000) => {
  const start = Date.now()
  while (Date.now() - start < timeoutMs) {
    const el = await page.$(sel)
    if (el) return el
    await pause(100)
  }
  return null
}

// ── Build provenance ────────────────────────────────────────────────────────
// Each marker exists only in the build under test, so a served build without it
// is not this change and nothing is captured.
const title = await waitFor('[data-testid="pull-request-title"]')
if (!title) {
  await browser.close()
  die(2, 'the pull request title never rendered.',
    'the served build predates this route, or the attachment fetch failed.',
    'the capture would be blank or a not-found panel.',
    'rebuild and restart the app from THIS worktree against the mock, and retry.')
}
const titleText = await page.evaluate((el) => el.textContent.trim(), title)
if (titleText !== `${owner}/${name} #${number}`) {
  await browser.close()
  die(1, `the title reads ${JSON.stringify(titleText)}, want ${JSON.stringify(`${owner}/${name} #${number}`)}.`,
    'the served payload is for a different pull request.', 'the capture would document the wrong attachment.',
    'confirm the mock and the URL name the same pull request and retry.')
}
const commit = await waitFor('a[href^="https://github.com/"][href*="/commit/"]')
const promptLink = await waitFor('a[href*="?turn="]')
if (!commit || !promptLink) {
  await browser.close()
  die(2, 'the digest chain did not render its anchors.',
    'a commit anchor or a turn link is missing, so this is not the digest this change renders.',
    'the capture would prove nothing about the chain.',
    'confirm the mock serves a digest with a commit item and a prompt item and retry.')
}

// The audience statement is this change's surface: a page that offers the
// confirm button without it is not the page under test, and capturing it would
// document the very gap this closes.
const audience = await waitFor('[data-testid="attachment-audience"]')
if (!audience) {
  await browser.close()
  die(2, 'the confirmation did not state the audience.',
    'the author-only panel mounted without the statement naming who the transcripts become readable by.',
    'the capture would document the confirm step without the thing this change adds.',
    'confirm the served build is from THIS worktree and the mock serves an author-visible preview, then retry.')
}
const audienceText = await page.evaluate((el) => el.textContent.replace(/\s+/g, ' ').trim(), audience)
// The expected sentence comes from the repository kind the mock serves, not from
// either sentence being acceptable: a capture pointed at a private repository
// that rendered the public copy, or the reverse, must fail rather than pass on a
// statement merely existing.
const expectedAudience = process.env.PULL_PRIVATE === '1'
  ? 'attaching makes the transcripts behind these prompts readable by members of this collective. a transcript that is already shared or public keeps its own audience.'
  : 'attaching makes the transcripts behind these prompts readable by anyone.'
if (audienceText !== expectedAudience) {
  await browser.close()
  die(1, `the audience statement reads ${JSON.stringify(audienceText)}, want ${JSON.stringify(expectedAudience)}.`,
    'the copy does not match the repository kind the mock serves, so the page names the wrong audience.',
    'the capture would document the wrong audience for this repository.',
    'serve the mock with the PULL_PRIVATE the capture means and retry.')
}

const probe = await page.evaluate(() => {
  const cs = (sel) => {
    const el = document.querySelector(sel)
    if (!el) return null
    const s = getComputedStyle(el)
    return { background: s.backgroundColor, color: s.color }
  }
  const root = getComputedStyle(document.documentElement)
  return {
    surfaceToken: root.getPropertyValue('--surface').trim(),
    canvasToken: root.getPropertyValue('--canvas').trim(),
    ink2Token: root.getPropertyValue('--ink-2').trim(),
    ink3Token: root.getPropertyValue('--ink-3').trim(),
    audienceText: (() => {
      const el = document.querySelector('[data-testid="attachment-audience"]')
      return el ? el.textContent.replace(/\s+/g, ' ').trim() : null
    })(),
    page: cs('[data-testid="pull-request-page"]'),
    panel: cs('[data-testid="pull-request-page"] section'),
    // A prompt row is a preview, not the whole turn. The clamp comes from the
    // design system's own class, so assert the computed clamp on whichever row
    // carries it: a screenshot of a long row cannot distinguish a clamped one
    // from one that merely fits.
    promptLineClamp: (() => {
      const rows = [...document.querySelectorAll('.pd-prompt-clamp')]
      return rows.length ? getComputedStyle(rows[rows.length - 1]).webkitLineClamp : null
    })(),
  }
})

const shoot = async (rawName, sel, { sparse = false } = {}) => {
  const el = await page.$(sel)
  if (!el) {
    await browser.close()
    die(1, `selector ${sel} did not resolve for ${rawName}.`, 'the surface never mounted.',
      'the PNG would be empty.', 'confirm the route rendered and retry.')
  }
  const box = await el.boundingBox()
  if (!box || box.width < 4 || box.height < 4) {
    await browser.close()
    die(1, `${sel} resolved to a blank box ${JSON.stringify(box)}.`, 'the surface did not lay out.',
      'the PNG would be empty.', 'confirm the fixtures loaded and retry.')
  }
  const file = `${out}/${rawName}.png`
  await el.screenshot({ path: file, captureBeyondViewport: true })
  let r
  if (sparse) {
    r = await gate.measure(file)
    if (r.nonbgRatio < MIN_NONBG_RATIO || r.distinctColors < MIN_DISTINCT_COLORS) {
      await browser.close()
      die(1, `sparse surface ${rawName} painted no content: nonbg=${(r.nonbgRatio * 100).toFixed(2)}% colors=${r.distinctColors}.`,
        'it is a near-uniform fill.', 'the capture would prove nothing.', 'confirm the panel mounted with content.')
    }
  } else {
    r = await gate.assert(rawName, file, { sel, where: 'pull-request-shoot.mjs' })
  }
  console.log('shot', rawName.padEnd(24), `${Math.round(box.width)}x${Math.round(box.height)}`.padEnd(11),
    `nonbg=${(r.nonbgRatio * 100).toFixed(1)}% colors=${r.distinctColors} ${(statSync(file).size / 1024).toFixed(1)}KB`)
  return file
}

// The page container carries no background of its own — the theme paints the
// body — so the full-page capture is the body, or the PNG would be transparent
// wherever the container is not covered by a panel.
await shoot('pr-pull-request-page', 'body')
await shoot('pr-pull-request-actions', '[data-testid="pull-request-actions"]', { sparse: true })

if (errs.length) {
  await browser.close()
  die(4, `the page logged ${errs.length} console error(s).`, errs[0],
    'a page that errored is not a page to document.', 'fix the error and recapture.')
}

writeFileSync(`${out}/probe.json`, JSON.stringify({ theme, titleText, audienceText, probe }, null, 2))
console.log('probe', JSON.stringify(probe))
await browser.close()
