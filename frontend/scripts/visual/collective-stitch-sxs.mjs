/* Side-by-side composites for the collective grouped surfaces:
   canonical Fairtrade demo (LEFT) | village app (RIGHT), both themes.

   Two composites are built per arm per theme, from the SAME two captures:
     <surface>.png           the demo's whole in-use surface | the app's whole surface
     <surface>-grouped.png   the demo's demonstration element  | the app's bounded grouped region

   Three things are enforced per arm per theme, and every one of them fails the
   run closed rather than being reported as a claim:
     1. BOTH SIDES EXIST AND ARE NON-BLANK — the vendored non-empty-surface floor,
        plus a byte-identical-sides refusal, so a composite can never be built
        from a missing or blank pane or from the same pixels twice.
     2. THE COMPUTED-STYLE COMPARISON RUNS — both shoots read the SAME property set
        (`COMPARED_PROPERTIES`) through `measureCollectiveStyles` and write it to
        `styles.json`; this script compares them property by property. A mismatch
        aborts the run. An element one side does not mount aborts it too, unless
        the arm names that element in `STYLE_EXCEPTIONS` with its reason (exactly
        two, both the same design-system fact).
     3. THE REFERENCE IS ATTRIBUTED — each side's `provenance.json` (written by its
        shoot) supplies the release proof and the served asset's sha256; the
        composite header states those, and what they do NOT prove.

   The pixel-diff printed per composite is INFORMATIONAL. These pairs are
   deliberately not the same surface, so a differing-pixel share is expected and is
   not a pass/fail. Element widths + wrap state are measured and printed too, never
   gated, because the two containers are legitimately different widths.

   Expects captures staged as (all four files written by the two shoots):
     <base>/demo/collective/<theme>/demo-<surface>{,-tree}.png
     <base>/demo/collective/<theme>/{styles,provenance}.json
     <base>/village/collective/<theme>/<app capture filenames>
     <base>/village/collective/<theme>/{styles,provenance}.json

   usage: CHROME_PATH=/path/to/chrome node scripts/visual/collective-stitch-sxs.mjs <base-dir>
*/
import { writeFileSync, existsSync, mkdirSync, readFileSync } from 'node:fs'
import { SurfaceGate } from './surface-gate.mjs'
import { diffPixels, dataUrl } from './png-diff.mjs'
import {
  COLLECTIVE_ARMS,
  REQUIRED_ARM_SURFACES,
  THEMES,
  appDir,
  compareStyleRecords,
  demoDir,
  describeStyleCheck,
  describeWrap,
  groupedSidePaths,
  provenanceForArm,
  provenanceLine,
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

const abort = async (message) => {
  await browser.close()
  console.error(message)
  process.exit(1)
}

/* Measure one side, aborting the whole run when it may not be composed. Both
   checks are `sideProblem`'s: the same predicate the self-check drives. */
const measuredSide = async ({ level, side, surface, theme, path, otherMd5 }) => {
  const exists = existsSync(path)
  const measurement = exists ? await gate.measure(path) : null
  const problem = sideProblem({ side, surface: `${surface}${level ? ` ${level}` : ''}`, theme, path, exists, measurement, otherMd5 })
  if (problem) await abort(problem)
  return measurement
}

/* The style + provenance records each shoot writes. A missing record is a hard
   failure: the comparison and the attribution are the parts of this gate that
   cannot be skipped silently. */
const SIDE_LABEL = { reference: 'reference (fairtrade demo) side', subject: 'subject (village app) side' }
const loadRecord = async ({ side, theme, file }) => {
  const path = `${side === 'reference' ? demoDir(BASE, theme) : appDir(BASE, theme)}/${file}`
  if (!existsSync(path)) {
    await abort(
      `ERROR [collective-stitch-sxs] the ${SIDE_LABEL[side]} has no ${file} for ${theme}.\n` +
      `  What failed: no file at ${path}.\n` +
      `  Why: the side was captured by an older shoot, or staged by hand.\n` +
      `  Where: collective-stitch-sxs.mjs ${file} load.\n` +
      `  Means: the ${side === 'reference' ? 'exact-release proof' : 'served-bundle proof'} and the computed-style comparison for this side would not run.\n` +
      `  Fix: re-run that side's shoot (demo-helper-groups-shoot.mjs / grouped-helper-shoot.mjs) for ${theme}, then re-stitch.`,
    )
  }
  try {
    return JSON.parse(readFileSync(path, 'utf8'))
  } catch (error) {
    await abort(`ERROR [collective-stitch-sxs] ${path} is not readable JSON: ${error.message}`)
  }
}

const compose = async ({ refUrl, appUrl, title, refLabel, appLabel, header }) =>
  page.evaluate(async (refUrl, appUrl, title, refLabel, appLabel, header) => {
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
    const wrap = (text, max) => {
      const words = String(text).split(/\s+/)
      const lines = []
      let line = ''
      for (const word of words) {
        if ((line + ' ' + word).trim().length > max) { lines.push(line.trim()); line = word }
        else line += ' ' + word
      }
      if (line.trim()) lines.push(line.trim())
      return lines
    }
    const paneWidth = Math.max(a.width, b.width)
    const mappingLines = header.mapping.flatMap((text) => wrap(text, Math.max(60, Math.floor(paneWidth / 6.6))).map((t) => ({ t, size: 13, color: '#c9c2b6' })))
    const evidenceLines = header.evidence.flatMap((text) => wrap(text, Math.max(70, Math.floor(paneWidth / 6.2))).map((t) => ({ t, size: 12, color: '#8f8a80' })))
    const lines = [...mappingLines, ...evidenceLines]
    const labelH = 74 + lines.length * 17
    const targetW = paneWidth
    const targetH = Math.max(a.height, b.height)
    const w = a.width + b.width + gapW + pad * 2
    const h = targetH + labelH + pad * 2
    const c = document.createElement('canvas'); c.width = w; c.height = h
    const x = c.getContext('2d')
    x.fillStyle = frame; x.fillRect(0, 0, w, h)
    x.fillStyle = ink; x.font = 'bold 22px ui-sans-serif, system-ui, sans-serif'
    x.fillText(title, pad, 34)
    lines.forEach((line, i) => {
      x.font = `${line.size}px ui-monospace, monospace`
      x.fillStyle = line.color
      x.fillText(line.t, pad, 58 + i * 17)
    })
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
  }, refUrl, appUrl, title, refLabel, appLabel, header)

const REF_LABEL = 'FAIRTRADE DEMO  (left)'
const APP_LABEL = 'VILLAGE APP  (right)'
let made = 0
const diffs = []
const styleSummary = []

for (const theme of THEMES) {
  const outDir = `${BASE}/sxs/collective/${theme}`
  mkdirSync(outDir, { recursive: true })
  const refStyles = await loadRecord({ side: 'reference', theme, file: 'styles.json' })
  const subStyles = await loadRecord({ side: 'subject', theme, file: 'styles.json' })
  const refProvenance = await loadRecord({ side: 'reference', theme, file: 'provenance.json' })
  const subProvenance = await loadRecord({ side: 'subject', theme, file: 'provenance.json' })

  for (const arm of COLLECTIVE_ARMS) {
    /* 1. the computed-style comparison, per arm, against the ONE property set */
    for (const [side, styles] of [['reference', refStyles], ['subject', subStyles]]) {
      if (!styles.arms?.[arm.surface]) {
        await abort(
          `ERROR [collective-stitch-sxs] the ${SIDE_LABEL[side]} has no style record for arm "${arm.surface}" (${theme}).\n` +
          `  What failed: ${side === 'reference' ? demoDir(BASE, theme) : appDir(BASE, theme)}/styles.json carries no arms["${arm.surface}"].\n` +
          `  Why: that side was captured before the shared style probe existed, or staged by hand.\n` +
          `  Where: collective-stitch-sxs.mjs style-record lookup.\n` +
          `  Means: this gate would attest a surface it never compared, which is the claim it exists to keep exact.\n` +
          `  Fix: re-run ${side === 'reference' ? 'demo-helper-groups-shoot.mjs' : `grouped-helper-shoot.mjs with GROUPED_SHOOT_SURFACE=${arm.shootSurface}`} for ${theme}, then re-stitch.`,
        )
      }
    }
    const compare = compareStyleRecords({
      arm: arm.surface,
      reference: refStyles.arms?.[arm.surface],
      subject: subStyles.arms?.[arm.surface],
    })
    if (compare.mismatches.length > 0) {
      await abort(
        `ERROR [collective-stitch-sxs] arm "${arm.surface}" (${theme}) does not match the demo's computed styles.\n` +
        `  What failed: ${compare.mismatches.map((m) => `${m.element}.${m.property}: demo=${JSON.stringify(m.reference)} app=${JSON.stringify(m.subject)}`).join('; ')}\n` +
        `  Where: collective-sxs.mjs COMPARED_PROPERTIES, read on both sides and compared here.\n` +
        `  Means: the app surface no longer renders the design system's grouped-helper primitive as the canonical demonstration does.\n` +
        `  Fix: inspect the matching composite under ${BASE}/sxs/collective/${theme}/; if the difference is intended, name it in STYLE_EXCEPTIONS with its reason, otherwise correct the surface.`,
      )
    }
    // Attribute each arm by the bundle ITS OWN route served: the per-arm entry is
    // the only one that can be reported for this composite.
    const refAttribution = provenanceForArm(refProvenance, arm.surface)
    const subAttribution = provenanceForArm(subProvenance, arm.surface)
    if (!refAttribution || !subAttribution) {
      await abort(
        `ERROR [collective-stitch-sxs] arm "${arm.surface}" (${theme}) has no per-arm provenance entry on the ${!refAttribution ? 'reference' : 'subject'} side.\n` +
        `  What failed: ${!refAttribution ? demoDir(BASE, theme) : appDir(BASE, theme)}/provenance.json does not name "${arm.surface}" in its arms list.\n` +
        `  Where: collective-sxs.mjs provenanceForArm, applied per arm.\n` +
        `  Means: this composite could only be attributed with another arm's bundle, which is not this surface's build.\n` +
        `  Fix: re-run ${!refAttribution ? 'demo-helper-groups-shoot.mjs' : `grouped-helper-shoot.mjs with GROUPED_SHOOT_SURFACE=${arm.shootSurface}`} for ${theme}, then re-stitch.`,
      )
    }
    const styleLine = describeStyleCheck(compare)
    const wrapLine = `wrap (measured, not gated) · count: demo ${describeWrap(refStyles.arms?.[arm.surface]?.wrap?.count)} / app ${describeWrap(subStyles.arms?.[arm.surface]?.wrap?.count)} · facts: demo ${describeWrap(refStyles.arms?.[arm.surface]?.wrap?.facts)} / app ${describeWrap(subStyles.arms?.[arm.surface]?.wrap?.facts)}`
    styleSummary.push({ theme, arm: arm.surface, compared: compare.compared, exceptions: compare.exceptions, divergences: compare.divergences, reference: refStyles.arms?.[arm.surface], subject: subStyles.arms?.[arm.surface] })
    for (const divergence of compare.divergences) {
      // Loud in the run log, printed on every composite, and never an exception:
      // a product divergence the harness records rather than explains away.
      console.log('DIVERGENCE', `${theme}/${arm.surface}`.padEnd(40), `${divergence.element} absent on ${divergence.missingOn}`)
      console.log('           ', divergence.reason)
    }

    const levels = [
      { level: '', paths: sidePaths(BASE, theme, arm), refLabel: `${REF_LABEL}  helpers=${arm.demoCase} · ${arm.demoState}`, appLabel: APP_LABEL },
      { level: 'grouped', paths: groupedSidePaths(BASE, theme, arm), refLabel: `${REF_LABEL}  ${arm.demoCase} demonstration`, appLabel: `${APP_LABEL}  grouped region` },
    ]
    for (const { level, paths, refLabel, appLabel } of levels) {
      const reference = await measuredSide({ level, side: 'reference', surface: arm.surface, theme, path: paths.reference })
      await measuredSide({ level, side: 'subject', surface: arm.surface, theme, path: paths.subject, otherMd5: reference.md5 })
      const file = `${arm.surface}${level ? '-grouped' : ''}`
      const meta = await compose({
        refUrl: dataUrl(paths.reference),
        appUrl: dataUrl(paths.subject),
        title: `${file}  ·  ${theme}`,
        refLabel,
        appLabel,
        header: {
          mapping: [arm.mapping],
          evidence: [
            `reference · ${provenanceLine(refAttribution)}`,
            `subject · ${provenanceLine(subAttribution)}`,
            styleLine,
            wrapLine,
          ],
        },
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
  console.log(`styles ${theme}: ${styleSummary.filter((s) => s.theme === theme).map((s) => `${s.arm.slice('collective-grouped-'.length)}=${s.compared} compared/${s.exceptions.length} named exceptions/${s.divergences.length} recorded divergences`).join(' · ')}`)
}

await browser.close()
console.log(`\nbuilt ${made} side-by-side composites under ${BASE}/sxs/collective/ (both themes, demo left | app right)`)
console.log(`INFORMATIONAL_DIFFS=${JSON.stringify(diffs)}`)
console.log(`STYLE_SUMMARY=${JSON.stringify(styleSummary)}`)
