/* Side-by-side composites for the collective grouped surfaces:
   canonical Fairtrade demo (LEFT) | village app (RIGHT), both themes.

   Two composites are built per arm per theme, from the SAME two captures:
     <surface>.png           the demo's whole in-use surface | the app's whole surface
     <surface>-grouped.png   the demo's demonstration element  | the app's bounded grouped region

   The mapping, the capture filenames and the fail-closed side check all live in
   `collective-sxs.mjs`, so this script, the two shoot scripts and the self-check
   read one table.

   FAIL CLOSED ON A MISSING OR BLANK SIDE. A pane is composed only after both
   sides exist and pass the vendored non-empty-surface floor (the same thresholds
   the shoots enforce), and after the two sides are proven not to be byte-identical
   to each other. A failed check aborts the run with an actionable message rather
   than writing a composite with an empty pane, or reporting a comparison that was
   never made.

   The pixel-diff printed per composite is INFORMATIONAL. These pairs are
   deliberately not the same surface: the reference is the demo's demonstration of
   the grouped-helper primitive and the subject is the app surface that composes
   it, so a differing-pixel share is expected and is not a pass/fail — the
   substantive judgement is the side-by-side read plus the computed-style probe
   the demo shoot and the app shoot each print.

   Expects captures staged as:
     <base>/demo/collective/<theme>/demo-<surface>{,-tree}.png
     <base>/village/collective/<theme>/<app capture filenames>

   usage: CHROME_PATH=/path/to/chrome node scripts/visual/collective-stitch-sxs.mjs <base-dir>
*/
import { writeFileSync, existsSync, mkdirSync } from 'node:fs'
import { SurfaceGate } from './surface-gate.mjs'
import { diffPixels, dataUrl } from './png-diff.mjs'
import {
  COLLECTIVE_ARMS,
  REQUIRED_ARM_SURFACES,
  THEMES,
  appDir,
  demoDir,
  groupedSidePaths,
  sidePaths,
  sideProblem,
} from './collective-sxs.mjs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const CHROME = process.env.CHROME_PATH
const BASE = process.argv[2]
const IMGDIFF_TOL = 16

if (!CHROME) {
  console.error('ERROR [collective-stitch-sxs] CHROME_PATH is unset - set it to your Chrome/Chromium binary.')
  process.exit(1)
}
if (!BASE) {
  console.error(
    'ERROR [collective-stitch-sxs] missing <base-dir> argument.\n' +
    '  usage: CHROME_PATH=... node scripts/visual/collective-stitch-sxs.mjs <base-dir>\n' +
    `  expected under it: ${demoDir('<base>', '<theme>')} and ${appDir('<base>', '<theme>')}`,
  )
  process.exit(1)
}

/* The arms table is the deletion-protection manifest: a required arm that lost
   its row would silently stop being stitched. */
const missingArms = REQUIRED_ARM_SURFACES.filter(
  (surface) => !COLLECTIVE_ARMS.some((arm) => arm.surface === surface),
)
if (missingArms.length > 0) {
  console.error(
    `ERROR [collective-stitch-sxs] the arms table dropped required arm(s): ${missingArms.join(', ')}.\n` +
    '  Fix: restore the arm(s) in collective-sxs.mjs or remove them from REQUIRED_ARM_SURFACES deliberately.',
  )
  process.exit(1)
}

const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new' })
const page = await browser.newPage()
await page.goto('about:blank')
const gate = new SurfaceGate(page)

/* Measure one side, aborting the whole run when it may not be composed. Both
   checks are `sideProblem`'s: the same predicate the self-check drives. */
const measuredSide = async ({ level, side, surface, theme, path, otherMd5 }) => {
  const exists = existsSync(path)
  const measurement = exists ? await gate.measure(path) : null
  const problem = sideProblem({ side, surface: `${surface}${level ? ` ${level}` : ''}`, theme, path, exists, measurement, otherMd5 })
  if (problem) {
    await browser.close()
    console.error(problem)
    process.exit(1)
  }
  return measurement
}

const compose = async ({ refUrl, appUrl, title, refLabel, appLabel, mapping }) =>
  page.evaluate(async (refUrl, appUrl, title, refLabel, appLabel, mapping) => {
    const load = (u) => new Promise((res, rej) => { const i = new Image(); i.onload = () => res(i); i.onerror = rej; i.src = u })
    const a = await load(refUrl)
    const b = await load(appUrl)
    /* sample a robust "page background" = the most common colour around an image's border
       (margins/gutters), weighted toward the BOTTOM row where the padding goes, so the
       shorter pane pads seamlessly with its own background. No token is hardcoded. */
    const sampleBg = (img) => {
      const tc = document.createElement('canvas'); tc.width = img.width; tc.height = img.height
      const tx = tc.getContext('2d'); tx.drawImage(img, 0, 0)
      const counts = new Map()
      const tally = (px, py) => { const d = tx.getImageData(px, py, 1, 1).data; const k = d[0] + ',' + d[1] + ',' + d[2]; counts.set(k, (counts.get(k) || 0) + 1) }
      const W = img.width, H = img.height
      const sx = Math.max(1, Math.floor(W / 100)), sy = Math.max(1, Math.floor(H / 100))
      for (let px = 0; px < W; px += sx) { tally(px, H - 1); tally(px, H - 2); tally(px, 0) }
      for (let py = 0; py < H; py += sy) { tally(0, py); tally(W - 1, py) }
      let best = '20,20,22', bestN = -1
      for (const [k, n] of counts) if (n > bestN) { bestN = n; best = k }
      return 'rgb(' + best + ')'
    }
    const pad = 28, gapW = 28, frame = '#161616', ink = '#f2f2f2', sub = '#9aa0a6'
    /* wrap the mapping note to the pane width so the composite states what it can and
       cannot compare, instead of a reader inferring it from the caption alone */
    const wrap = (text, max) => {
      const words = text.split(/\s+/)
      const lines = []
      let line = ''
      for (const word of words) {
        if ((line + ' ' + word).trim().length > max) { lines.push(line.trim()); line = word }
        else line += ' ' + word
      }
      if (line.trim()) lines.push(line.trim())
      return lines
    }
    const mappingLines = wrap(mapping, Math.max(60, Math.floor(Math.max(a.width, b.width) / 6.6)))
    const labelH = 74 + mappingLines.length * 18
    const targetW = Math.max(a.width, b.width)
    const targetH = Math.max(a.height, b.height)
    const w = a.width + b.width + gapW + pad * 2
    const h = targetH + labelH + pad * 2
    const c = document.createElement('canvas'); c.width = w; c.height = h
    const x = c.getContext('2d')
    x.fillStyle = frame; x.fillRect(0, 0, w, h)
    x.fillStyle = ink; x.font = 'bold 22px ui-sans-serif, system-ui, sans-serif'
    x.fillText(title, pad, 34)
    x.font = '13px ui-monospace, monospace'; x.fillStyle = '#c9c2b6'
    mappingLines.forEach((line, i) => x.fillText(line, pad, 58 + i * 18))
    x.font = 'bold 16px ui-monospace, monospace'; x.fillStyle = sub
    x.fillText(refLabel, pad, labelH - 10)
    x.fillText(appLabel, pad + a.width + gapW, labelH - 10)
    const bodyY = labelH + pad
    const appX = pad + a.width + gapW
    /* HEIGHT-MATCH, top-aligned: the shorter pane is padded (never scaled) with its
       own border-sampled background; a dashed hairline marks where it ends. */
    x.fillStyle = sampleBg(a); x.fillRect(pad, bodyY, a.width, targetH); x.drawImage(a, pad, bodyY)
    x.fillStyle = sampleBg(b); x.fillRect(appX, bodyY, b.width, targetH); x.drawImage(b, appX, bodyY)
    x.strokeStyle = 'rgba(150,150,150,0.5)'; x.lineWidth = 1; x.setLineDash([6, 5])
    if (a.height < targetH) { x.beginPath(); x.moveTo(pad, bodyY + a.height + 0.5); x.lineTo(pad + a.width, bodyY + a.height + 0.5); x.stroke() }
    if (b.height < targetH) { x.beginPath(); x.moveTo(appX, bodyY + b.height + 0.5); x.lineTo(appX + b.width, bodyY + b.height + 0.5); x.stroke() }
    x.setLineDash([])
    /* padded copies for the informational diff: a legitimately different pane height
       must not read as an incomparable pair, and padding never manufactures a pass. */
    const padTo = (img) => {
      const pc = document.createElement('canvas'); pc.width = targetW; pc.height = targetH
      const px = pc.getContext('2d')
      px.fillStyle = sampleBg(img); px.fillRect(0, 0, targetW, targetH); px.drawImage(img, 0, 0)
      return pc.toDataURL('image/png')
    }
    return { url: c.toDataURL('image/png'), aH: a.height, bH: b.height, targetH, refDiffUrl: padTo(a), appDiffUrl: padTo(b) }
  }, refUrl, appUrl, title, refLabel, appLabel, mapping)

const REF_LABEL = 'FAIRTRADE DEMO  (left)'
const APP_LABEL = 'VILLAGE APP  (right)'
let made = 0
const diffs = []

for (const theme of THEMES) {
  const outDir = `${BASE}/sxs/collective/${theme}`
  mkdirSync(outDir, { recursive: true })
  for (const arm of COLLECTIVE_ARMS) {
    const levels = [
      { level: '', paths: sidePaths(BASE, theme, arm), refLabel: `${REF_LABEL}  helpers=${arm.demoCase} · ${arm.demoState}`, appLabel: APP_LABEL, mapping: arm.mapping },
      { level: 'grouped', paths: groupedSidePaths(BASE, theme, arm), refLabel: `${REF_LABEL}  ${arm.demoCase} demonstration`, appLabel: `${APP_LABEL}  grouped region`, mapping: arm.mapping },
    ]
    for (const { level, paths, refLabel, appLabel, mapping } of levels) {
      const reference = await measuredSide({ level, side: 'reference', surface: arm.surface, theme, path: paths.reference })
      await measuredSide({ level, side: 'subject', surface: arm.surface, theme, path: paths.subject, otherMd5: reference.md5 })
      const file = `${arm.surface}${level ? '-grouped' : ''}`
      const meta = await compose({
        refUrl: dataUrl(paths.reference),
        appUrl: dataUrl(paths.subject),
        title: `${file}  ·  ${theme}`,
        refLabel,
        appLabel,
        mapping,
      })
      writeFileSync(`${outDir}/${file}.png`, Buffer.from(meta.url.replace(/^data:image\/png;base64,/, ''), 'base64'))
      made++
      const r = await diffPixels(page, meta.refDiffUrl, meta.appDiffUrl, IMGDIFF_TOL, false)
      const pct = r.dim ? Infinity : (100 * r.diff) / r.total
      diffs.push({ theme, surface: file, reference: paths.reference, subject: paths.subject, aH: meta.aH, bH: meta.bH, pct })
      const padNote = meta.aH === meta.bH ? 'equal' : `pad ${meta.aH < meta.bH ? 'REF' : 'APP'} +${Math.abs(meta.aH - meta.bH)}px`
      console.log('sxs', `${theme}/${file}`.padEnd(46), padNote.padEnd(20), r.dim ? 'informational diff: n/a' : `informational diff: ${pct.toFixed(2)}%`)
    }
  }
}

await browser.close()
console.log(`\nbuilt ${made} side-by-side composites under ${BASE}/sxs/collective/ (both themes, demo left | app right)`)
console.log(`INFORMATIONAL_DIFFS=${JSON.stringify(diffs)}`)
