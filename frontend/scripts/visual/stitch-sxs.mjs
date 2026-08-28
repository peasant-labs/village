/* Build labeled, HEIGHT-MATCHED side-by-side composites (REFERENCE | SUBJECT) per surface per theme.
   Dependency-free: decodes the two PNGs as data: URLs onto a <canvas> inside headless Chrome, draws
   them with labels, exports a PNG.

   HEIGHT-MATCH (so rows line up for row-by-row comparison, no ragged panes):
     - both panes are drawn at the SAME height = max(refH, appH), TOP-ALIGNED.
     - the shorter pane is PADDED (never scaled/distorted) down to that height with its OWN
       background colour, sampled from the capture's border (margins/gutters). The pad colour
       comes from the pixels themselves — no design-token value is hardcoded — so it stays
       seamless across dark/light and across the reference vs subject surfaces.
     - a faint dashed hairline marks where the shorter capture actually ends, so the padded
       region is obvious and not mistaken for empty UI.
   Where the subject side has no capture (a recorded gap), it draws a full-height placeholder panel
   with the reason, so the side-by-side set stays complete and self-explanatory.

   It pairs a REFERENCE set against the SUBJECT app captures (<base-dir>/<APP_DIR>/<theme>/) — both
   produced elsewhere; this script only composites them. See the README "Oracle" section for what the
   transcript SxS does and does NOT gate:
     - REF_DIR=tb (default) vs APP_DIR=village → the SAME-component <SessionDetail>
       before/after. The reference is the COMMITTED, frozen `baseline/tb/` set tracked next to these
       scripts (the frozen pre-theme-convergence capture); a divergence is a real regression.
       (Resolved from the committed dir unless a same-named set is staged under <base-dir>.)
     - REF_DIR=demo vs APP_DIR=village → a NON-GATING design-language sanity panel (the fairtrade demo's
       TranscriptViewer is a DIFFERENT component from the app's <SessionDetail>); stage the demo under
       <base-dir>/demo/.

   env:
     CHROME_PATH     (required) Chrome/Chromium binary puppeteer drives
     REF_DIR         reference (left) set name                 (default `tb`, the committed baseline/tb/ <SessionDetail> reference)
     REF_LABEL       the reference-pane caption               (default the <SessionDetail>-reference caption)
     APP_DIR         subject (right) app capture subdir        (default `village`)
     APP_LABEL       the subject-pane caption                 (default the village frontend caption)
     PUPPETEER_CORE  explicit module path to puppeteer-core   (optional)
   usage: CHROME_PATH=/path/to/chrome node stitch-sxs.mjs <base-dir>
*/
import { writeFileSync, existsSync, mkdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { diffPixels, dataUrl } from './png-diff.mjs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

// imgdiff gate (additive to the human-glance composite below): per [surface, theme], pixel-diff the RAW
// reference vs the RAW app capture and FAIL the run if any pair diverges past the threshold.
//   IMGDIFF_TOL      per-channel tolerance (out of 255) that absorbs sub-pixel anti-aliasing shimmer.
//   IMGDIFF_FAIL_PCT max share of differing pixels (%) a surface may have before it FAILs.
const IMGDIFF_TOL = 16

const CHROME = process.env.CHROME_PATH
const CAPTURE_ROOT = process.argv[2]
const COMMITTED_ROOT = join(dirname(fileURLToPath(import.meta.url)), 'baseline')
const SURFACE_SET = process.env.SURFACE_SET || 'txn'
const IMGDIFF_FAIL_PCT = SURFACE_SET === 'cex' ? 12 : 0.5
// The LEFT (reference) pane and the RIGHT (subject) pane are both parameterized:
//   - DEFAULT transcript oracle: REF_DIR=tb vs APP_DIR=village — the SAME-component before/after view.
//     The reference `tb` is the committed, frozen <SessionDetail>
//     capture from before theme convergence (non-regenerable; tracked at baseline/tb/ next to
//     these scripts); the subject is the current village <SessionDetail>. Both are `.tb-*`, so a
//     divergence is a real transcript regression, not a component difference. (The SxS is NOT expected to
//     be zero-diff — the theme-convergence delta is intentional; it's judged for design cohesion + no
//     host-integration regression, not pixel-identity.)
//   - OPTIONAL design-language sanity: REF_DIR=demo — pairs the fairtrade demo (its TranscriptViewer,
//     `.txn-*`) against the app's <SessionDetail> (`.tb-*`). DIFFERENT components, so this is a NON-GATING
//     design-language sanity panel only, NOT a transcript pass/fail (see README "Oracle").
const REF_DIR = process.env.REF_DIR || (SURFACE_SET === 'cex' ? 'demo' : 'tb')
const REF_LABEL = process.env.REF_LABEL || (SURFACE_SET === 'cex'
  ? 'REFERENCE  (fairtrade in-use demo — village app)'
  : 'REFERENCE  (<SessionDetail>, before convergence)')
const APP_DIR = process.env.APP_DIR || 'village'
const APP_LABEL = process.env.APP_LABEL || (SURFACE_SET === 'cex'
  ? 'VILLAGE-FRONTEND  (Explore — current)'
  : 'VILLAGE-FRONTEND  (<SessionDetail> — current)')
const THEMES = ['dark', 'light']

// Resolve the reference dir for a theme: prefer a set staged under the capture base
// (<base>/<REF_DIR>/<theme>/, e.g. a regenerable `demo` run), else fall back to the COMMITTED reference
// set tracked next to these scripts (scripts/visual/baseline/<REF_DIR>/<theme>/ — the frozen `tb`
// golden). So the default no-regression oracle works on a clean checkout with nothing to stage.
const refDirFor = (theme) => {
  const staged = `${CAPTURE_ROOT}/${REF_DIR}/${theme}`
  return existsSync(staged) ? staged : join(COMMITTED_ROOT, REF_DIR, theme)
}
// The canonical surface set. Each entry names the output surface plus the
// reference/app capture filenames. The cex Explore set compares the fairtrade
// demo's existing `app-2-village.png` against this app's `cex-explore.png`.
const SURFACES = SURFACE_SET === 'cex'
  ? [
      { surface: 'cex-explore', refSurface: 'app-2-village', appSurface: 'cex-explore', gap: null },
    ]
  : [
      { surface: 'txn-highlights', refSurface: 'txn-highlights', appSurface: 'txn-highlights', gap: null },
      { surface: 'txn-scorecard', refSurface: 'txn-scorecard', appSurface: 'txn-scorecard', gap: null },
      { surface: 'txn-trace-canvas', refSurface: 'txn-trace-canvas', appSurface: 'txn-trace-canvas', gap: null },
      { surface: 'txn-scrubber', refSurface: 'txn-scrubber', appSurface: 'txn-scrubber', gap: null },
      { surface: 'txn-rails', refSurface: 'txn-rails', appSurface: 'txn-rails', gap: null },
      { surface: 'txn-label-popover', refSurface: 'txn-label-popover', appSurface: 'txn-label-popover', gap: null },
      { surface: 'txn-graph', refSurface: 'txn-graph', appSurface: 'txn-graph', gap: null },
      { surface: 'txn-diffs', refSurface: 'txn-diffs', appSurface: 'txn-diffs', gap: null },
      { surface: 'txn-files', refSurface: 'txn-files', appSurface: 'txn-files', gap: null },
      { surface: 'txn-annotations', refSurface: 'txn-annotations', appSurface: 'txn-annotations', gap: null },
    ]

if (!CHROME) {
  console.error('ERROR [stitch-sxs.mjs] CHROME_PATH is unset — set it to your Chrome/Chromium binary.')
  process.exit(1)
}
if (!CAPTURE_ROOT) {
  console.error('ERROR [stitch-sxs.mjs] missing <base-dir> argument.\n  usage: CHROME_PATH=... node scripts/visual/stitch-sxs.mjs <base-dir>')
  process.exit(1)
}

const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new' })
const page = await browser.newPage()
await page.goto('about:blank')

let made = 0
const diffResults = [] // { theme, surface, status:'compared'|'dim'|'no-ref'|'no-app', pct, diff, total }
for (const theme of THEMES) {
  const outDir = `${CAPTURE_ROOT}/sxs/${theme}`
  mkdirSync(outDir, { recursive: true })
  for (const { surface, refSurface, appSurface, gap } of SURFACES) {
    const refPath = `${refDirFor(theme)}/${refSurface}.png`
    const appPath = `${CAPTURE_ROOT}/${APP_DIR}/${theme}/${appSurface}.png`
    if (!existsSync(refPath)) {
      console.error('skip (no reference):', theme, surface)
      diffResults.push({ theme, surface, status: 'no-ref', pct: Infinity, diff: 0, total: 0 })
      continue
    }
    const refUrl = dataUrl(refPath)
    const appUrl = existsSync(appPath) ? dataUrl(appPath) : null
    const meta = await page.evaluate(async (refUrl, appUrl, gap, title, appLabel, refLabel) => {
      const load = (u) => new Promise((res, rej) => { const i = new Image(); i.onload = () => res(i); i.onerror = rej; i.src = u })
      const a = await load(refUrl)
      const b = appUrl ? await load(appUrl) : null

      /* sample a robust "page background" = the most common colour around an image's border
         (margins/gutters), weighted toward the BOTTOM row where the padding goes. Used to pad
         the shorter pane seamlessly. Colour is read from the capture — no token is hardcoded. */
      const sampleBg = (img) => {
        const tc = document.createElement('canvas'); tc.width = img.width; tc.height = img.height
        const tx = tc.getContext('2d'); tx.drawImage(img, 0, 0)
        const W = img.width, H = img.height
        const counts = new Map()
        const tally = (px, py) => { const d = tx.getImageData(px, py, 1, 1).data; const k = d[0] + ',' + d[1] + ',' + d[2]; counts.set(k, (counts.get(k) || 0) + 1) }
        const sx = Math.max(1, Math.floor(W / 100)), sy = Math.max(1, Math.floor(H / 100))
        for (let px = 0; px < W; px += sx) { tally(px, H - 1); tally(px, H - 2); tally(px, 0) }
        for (let py = 0; py < H; py += sy) { tally(0, py); tally(W - 1, py) }
        let best = '20,20,22', bestN = -1
        for (const [k, n] of counts) if (n > bestN) { bestN = n; best = k }
        return 'rgb(' + best + ')'
      }

      const pad = 28, gapW = 28, labelH = 64, frame = '#161616', ink = '#f2f2f2', sub = '#9aa0a6'
      const targetW = Math.max(a.width, b ? b.width : a.width)
      const bW = b ? b.width : Math.max(560, Math.round(a.width * 0.8))
      const targetH = Math.max(a.height, b ? b.height : a.height)   // HEIGHT-MATCH to the taller pane
      const w = a.width + bW + gapW + pad * 2
      const h = targetH + labelH + pad * 2
      const c = document.createElement('canvas'); c.width = w; c.height = h
      const x = c.getContext('2d')
      x.fillStyle = frame; x.fillRect(0, 0, w, h)
      // title bar
      x.fillStyle = ink; x.font = 'bold 22px ui-sans-serif, system-ui, sans-serif'
      x.fillText(title, pad, 34)
      // column captions
      x.font = 'bold 16px ui-monospace, monospace'; x.fillStyle = sub
      x.fillText(refLabel, pad, labelH - 8)
      x.fillText(appLabel, pad + a.width + gapW, labelH - 8)

      const bodyY = labelH + pad
      const appX = pad + a.width + gapW

      // REFERENCE pane — bg-pad to targetH, then draw top-aligned
      x.fillStyle = sampleBg(a); x.fillRect(pad, bodyY, a.width, targetH)
      x.drawImage(a, pad, bodyY)

      if (b) {
        // app pane — bg-pad to targetH, draw top-aligned so rows line up from the top
        x.fillStyle = sampleBg(b); x.fillRect(appX, bodyY, b.width, targetH)
        x.drawImage(b, appX, bodyY)
        // dashed hairline at the shorter pane's content bottom → padded region is obvious
        x.strokeStyle = 'rgba(150,150,150,0.5)'; x.lineWidth = 1; x.setLineDash([6, 5])
        if (a.height < targetH) { const yy = bodyY + a.height + 0.5; x.beginPath(); x.moveTo(pad, yy); x.lineTo(pad + a.width, yy); x.stroke() }
        if (b.height < targetH) { const yy = bodyY + b.height + 0.5; x.beginPath(); x.moveTo(appX, yy); x.lineTo(appX + b.width, yy); x.stroke() }
        x.setLineDash([])
      } else {
        // full-height placeholder panel with the gap reason
        x.fillStyle = '#202022'; x.fillRect(appX, bodyY, bW, targetH)
        x.strokeStyle = '#3a3a3d'; x.lineWidth = 2; x.strokeRect(appX + 1, bodyY + 1, bW - 2, targetH - 2)
        x.fillStyle = '#e06c5e'; x.font = 'bold 18px ui-monospace, monospace'
        x.fillText('⚠ surface not captured by the harness route', appX + 20, bodyY + 40)
        x.fillStyle = sub; x.font = '14px ui-monospace, monospace'
        const reason = gap || 'No app capture for this surface — see the run log and the MANIFEST gaps section.'
        reason.split('\n').forEach((line, i) => x.fillText(line, appX + 20, bodyY + 74 + i * 22))
      }
      const padTo = (img) => {
        const pc = document.createElement('canvas'); pc.width = targetW; pc.height = targetH
        const px = pc.getContext('2d')
        px.fillStyle = sampleBg(img)
        px.fillRect(0, 0, targetW, targetH)
        px.drawImage(img, 0, 0)
        return pc.toDataURL('image/png')
      }
      return { url: c.toDataURL('image/png'), aH: a.height, bH: b ? b.height : null, targetH, targetW, refDiffUrl: padTo(a), appDiffUrl: b ? padTo(b) : null }
    }, refUrl, appUrl, gap, `${surface}  ·  ${theme}`, APP_LABEL, REF_LABEL)
    const b64 = meta.url.replace(/^data:image\/png;base64,/, '')
    writeFileSync(`${outDir}/${surface}.png`, Buffer.from(b64, 'base64'))
    made++
    const padNote = meta.bH == null ? 'ref|GAP' : (meta.aH === meta.bH ? 'equal' : `pad ${meta.aH < meta.bH ? 'REF' : 'APP'} +${Math.abs(meta.aH - meta.bH)}px → ${meta.targetH}`)
    console.log('sxs', `${theme}/${surface}`.padEnd(34), padNote)

    // imgdiff arm: pixel-diff normalized images. In the cex Explore gate we compare
    // the padded versions so the human-review surface can differ slightly in shell geometry
    // without tripping a size-mismatch false positive. Missing captures still fail closed.
    if (!appUrl) {
      diffResults.push({ theme, surface, status: 'no-app', pct: Infinity, diff: 0, total: 0 })
    } else {
      const diffRef = SURFACE_SET === 'cex' ? meta.refDiffUrl : refUrl
      const diffApp = SURFACE_SET === 'cex' ? meta.appDiffUrl : appUrl
      const r = await diffPixels(page, diffRef, diffApp, IMGDIFF_TOL, false)
      const pct = r.dim ? Infinity : (100 * r.diff) / r.total
      diffResults.push({ theme, surface, status: r.dim ? 'dim' : 'compared', pct, diff: r.dim ? 0 : r.diff, total: r.dim ? 0 : r.total })
    }
  }
}
console.log(`\nbuilt ${made} height-matched side-by-side composites under ${CAPTURE_ROOT}/sxs/`)
await browser.close()

/* ── imgdiff summary + pass/fail gate (additive to the composites above) ──────────────────────────────
   The composites are for human glance; THIS is the automated pass/fail. A surface PASSES iff it was
   comparable (ref + app both present, same size) AND its differing-pixel share is within IMGDIFF_FAIL_PCT.
   Fail closed: a non-comparable surface (no ref / no app / dim size mismatch) FAILs, and a run that
   compared nothing is a FAILURE, never a vacuous 0.0000%. */
console.log(`\n=== imgdiff (TOL=${IMGDIFF_TOL}/255, FAIL > ${IMGDIFF_FAIL_PCT.toFixed(2)}% differing pixels) ===`)
let worst = 0
let failures = 0
let comparedCount = 0
for (const d of diffResults) {
  let tag
  if (d.status === 'no-ref') { tag = 'NO-REF'; failures++ }
  else if (d.status === 'no-app') { tag = 'NO-APP'; failures++ }
  else if (d.status === 'dim') { tag = 'DIM!'; failures++ }
  else {
    comparedCount++
    worst = Math.max(worst, d.pct)
    if (d.pct > IMGDIFF_FAIL_PCT) { tag = 'DIFF!'; failures++ }
    else tag = d.pct === 0 ? 'IDENTICAL' : d.pct < 0.05 ? 'ok~' : 'CHECK'
  }
  const pctStr = Number.isFinite(d.pct) ? `${d.pct.toFixed(4)}%` : '   --   '
  const counts = d.status === 'compared' ? ` (${d.diff}/${d.total})` : ''
  console.log(`${tag.padEnd(10)} ${`${d.theme}/${d.surface}`.padEnd(34)} ${pctStr}${counts}`)
}
console.log(`\nworst: ${worst.toFixed(4)}%  compared: ${comparedCount}/${diffResults.length}  failures: ${failures}`)

if (comparedCount === 0) {
  console.error(
    `\nFAIL [stitch-sxs.mjs] imgdiff compared ZERO surfaces — the gate would pass vacuously.\n` +
    `  Means: no [surface, theme] pair had BOTH a reference (baseline/${REF_DIR}/) and an app capture (${APP_DIR}/).\n` +
    `  Fix: run the shoot for ${APP_DIR} in both themes so ${CAPTURE_ROOT}/${APP_DIR}/<theme>/<surface>.png exist, then re-stitch.`
  )
  process.exit(1)
}
if (failures > 0) {
  console.error(
    `\nFAIL [stitch-sxs.mjs] imgdiff gate did not pass cleanly (${failures} failing surface(s); worst ${worst.toFixed(4)}% > ${IMGDIFF_FAIL_PCT.toFixed(2)}%).\n` +
    `  NO-REF/NO-APP = a surface could not be compared (missing baseline or app capture) — fail closed.\n` +
    `  DIM!          = the reference and app capture differ in size.\n` +
    `  DIFF!         = the differing-pixel share exceeds ${IMGDIFF_FAIL_PCT.toFixed(2)}%.\n` +
    `  Fix: inspect the flagged rows + the matching composite under ${CAPTURE_ROOT}/sxs/<theme>/, and correct the visual regression (or restage the missing capture).`
  )
  process.exit(1)
}
