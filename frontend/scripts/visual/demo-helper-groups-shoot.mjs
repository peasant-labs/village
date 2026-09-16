/* Capture the canonical Fairtrade grouped-helper demonstration — the REFERENCE
   (left) side of the collective grouped side-by-side arms — from a SERVED demo
   build whose release is proven.

   The live in-use demo is the fidelity oracle, and it mounts no collective
   surface that reads the grouped helper pages (its collective browse is a flat
   table, its contribute tree is flat, its review queue is a flat queue). What it
   does mount is the canonical grouped-helper primitive demonstration,
   `?app=commons&helpers=<case>#inuse`, so that demonstration is the reference for
   every arm. Which case and which state each arm pairs against is the ARM table
   in `collective-sxs.mjs` — read its comment before changing anything here.

   WHAT THIS PROVES, AND WHAT IT DOES NOT (asserted before any PNG is written):
     1. EXACT RELEASE — the checkout named by FAIRTRADE_CHECKOUT carries the same
        package version this app pins and `git describe --tags --exact-match`
        names exactly `fairtrade-v<version>`. A checkout between releases cannot
        pass. Without a checkout, an operator may instead pin the served bytes
        with DEMO_ASSET_SHA256, which is recorded as `digest-only`.
     2. SERVED BYTES — the served asset is fetched and digested (sha256), and
        cross-checked byte-for-byte against the same file inside that checkout's
        `dist/` (or against the pinned digest). A server serving another dist
        fails here.
     3. CONTENT MARKERS — the live DOM carries the arm's demonstration and the
        demo shell, and the served asset carries the demonstration and connector
        markers. This is a content check, not the release proof.
     It does NOT prove the build was never rebuilt, that the tree the dist was
     built from was clean, or that these bytes were the published ones. The
     composite header repeats that limit on every artifact.

   The demo-side computed readings use the SAME property set the app side reads
   (`measureCollectiveStyles`), and are written to `styles.json` next to the
   captures for the stitcher's comparison. Element widths + wrap state are
   measured and recorded too, never eyeballed.

   env:
     CHROME_PATH          Chrome/Chromium binary (required)
     DEMO_ORIGIN          served demo origin (default http://localhost:5180)
     FAIRTRADE_CHECKOUT   the fairtrade checkout the served dist was built from
                          (required unless DEMO_ASSET_SHA256 is set)
     DEMO_ASSET_SHA256    the served asset's expected sha256 (digest-only proof)
     PUPPETEER_CORE       explicit module path to puppeteer-core (optional)
   usage: CHROME_PATH=... FAIRTRADE_CHECKOUT=<checkout> DEMO_ORIGIN=http://127.0.0.1:5190 \
            node scripts/visual/demo-helper-groups-shoot.mjs <theme> <outdir>
*/
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { createConnection } from 'node:net'
import { basename, join } from 'node:path'
import {
  COLLECTIVE_ARMS,
  DEMO_MARKERS,
  describeWrap,
  digestProblem,
  measureCollectiveStyles,
  pinnedFairtradeVersion,
  provenanceLine,
  releaseProofProblem,
  sha256Hex,
} from './collective-sxs.mjs'
import { SurfaceGate } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const CHROME = process.env.CHROME_PATH
const ORIGIN = (process.env.DEMO_ORIGIN || 'http://localhost:5180').replace(/\/$/, '')
const CHECKOUT = process.env.FAIRTRADE_CHECKOUT || ''
const PINNED_DIGEST = (process.env.DEMO_ASSET_SHA256 || '').toLowerCase()
const THEMES = ['dark', 'light']
const theme = process.argv[2] || 'dark'
const out = process.argv[3] || `/tmp/demo-collective-${theme}`
const VIEWPORT = { width: 1460, height: 1000, deviceScaleFactor: 1 }

const die = async (browser, message) => {
  await browser?.close()
  console.error(message)
  process.exit(1)
}

if (!THEMES.includes(theme)) {
  console.error(`ERROR [demo-helper-groups-shoot.mjs] theme "${theme}" is not one of ${THEMES.join(', ')}.`)
  process.exit(1)
}
if (!CHROME) {
  console.error(
    'ERROR [demo-helper-groups-shoot.mjs] CHROME_PATH is unset — set it to your Chrome/Chromium binary.',
  )
  process.exit(1)
}

const pinnedVersion = pinnedFairtradeVersion()
const PROOF = CHECKOUT ? 'exact-release' : PINNED_DIGEST ? 'digest-only' : ''
if (!PROOF) {
  console.error(
    `ERROR [demo-helper-groups-shoot.mjs] no exact release proof was supplied for the demo reference.\n` +
    `  What failed: neither FAIRTRADE_CHECKOUT nor DEMO_ASSET_SHA256 is set.\n` +
    `  Why: content markers alone cannot attribute a capture to the release this app pins (${pinnedVersion}).\n` +
    `  Where: demo-helper-groups-shoot.mjs startup, release-proof selection.\n` +
    `  Means: the reference pane would be unattributable, which is the claim this gate exists to make exact.\n` +
    `  Fix: set FAIRTRADE_CHECKOUT to the checkout you built the served dist from (preferred), or pin the\n` +
    `       served bytes with DEMO_ASSET_SHA256=<sha256> (recorded as digest-only).`,
  )
  process.exit(1)
}

const gitDescribeExact = (dir) => {
  try {
    return execFileSync('git', ['-C', dir, 'describe', '--tags', '--exact-match'], {
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'pipe'],
    }).trim()
  } catch {
    return ''
  }
}
const checkoutVersion = CHECKOUT ? JSON.parse(readFileSync(join(CHECKOUT, 'package.json'), 'utf8')).version : null
const describeTag = CHECKOUT ? gitDescribeExact(CHECKOUT) : ''
if (PROOF === 'exact-release') {
  const problem = releaseProofProblem({ checkout: CHECKOUT, pinnedVersion, checkoutVersion, describeTag })
  if (problem) {
    console.error(problem)
    process.exit(1)
  }
}

/* The demo must be SERVED before the browser starts: an empty page looks like a
   successful capture, which would silently corrupt the reference pane. */
const probePort = (host, port) =>
  new Promise((resolve) => {
    const socket = createConnection({ port, host })
    socket.on('connect', () => { socket.destroy(); resolve(true) })
    socket.on('error', () => resolve(false))
  })
const parsed = new URL(ORIGIN)
const port = parsed.port ? Number(parsed.port) : parsed.protocol === 'https:' ? 443 : 80
if (!(await probePort(parsed.hostname, port))) {
  console.error(
    `ERROR [demo-helper-groups-shoot.mjs] nothing is listening at ${ORIGIN}.\n` +
    `  What failed: TCP connect to ${parsed.hostname}:${port} was refused.\n` +
    `  Why: the demo build is not being served (or DEMO_ORIGIN points elsewhere).\n` +
    `  Where: demo-helper-groups-shoot.mjs startup, liveness check.\n` +
    `  Means: every capture would be of an empty page.\n` +
    `  Fix: serve the built demo from the proven checkout, e.g.\n` +
    `    cd ${CHECKOUT || '<fairtrade checkout>'} && node_modules/.bin/vite preview --port 5190 --strictPort --host 127.0.0.1\n` +
    `  then re-run with DEMO_ORIGIN=http://127.0.0.1:5190.`,
  )
  process.exit(1)
}

mkdirSync(out, { recursive: true })
const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', defaultViewport: { ...VIEWPORT } })
const page = await browser.newPage()
await applyDeterminism(page)
const errs = []
page.on('console', (m) => { if (m.type() === 'error' && !/favicon/.test(m.text())) errs.push(m.text()) })
page.on('pageerror', (e) => errs.push('pageerr: ' + e.message))

const pause = (ms) => new Promise((r) => setTimeout(r, ms))
const gate = new SurfaceGate(page)

const waitFor = async (sel, timeoutMs = 20000) => {
  const start = Date.now()
  while (Date.now() - start < timeoutMs) {
    const el = await page.$(sel)
    if (el) return el
    await pause(100)
  }
  return null
}

/* Wait on the demonstration's own measure: the connector reports how many mounted
   checkboxes it traced, and that count only settles after the disclosure change. */
const waitForAnchors = (wanted) =>
  page.waitForFunction(
    (target) => [...document.querySelectorAll('.helper-tree-rail')]
      .reduce((total, el) => total + Number(el.dataset.anchorCount), 0) === target,
    { timeout: 10000 },
    wanted,
  )

/* THE SERVED BYTES. Fetch every bundle the page loads, keep the ones carrying
   ALL of this side's markers, digest them, and require the SAME asset for every
   arm of the run — a page that mixed two builds could otherwise pass arm by arm.
   Returns the provenance record's `asset` field. */
const fetchedAssets = new Map()
const servedAssets = async () => {
  const sources = await page.evaluate(() => {
    const scripts = [...document.querySelectorAll('script[src]')].map((el) => el.getAttribute('src'))
    const resources = performance.getEntriesByType('resource').map((entry) => entry.name)
    return [...new Set([...scripts, ...resources].filter((url) => url && /\.js(\?|$)/.test(url)))]
  })
  const matches = []
  for (const src of sources) {
    const url = new URL(src, ORIGIN).href
    if (!fetchedAssets.has(url)) {
      try {
        const response = await fetch(url)
        if (!response.ok) {
          fetchedAssets.set(url, null)
          continue
        }
        const body = Buffer.from(await response.arrayBuffer())
        fetchedAssets.set(url, {
          url,
          file: basename(new URL(url).pathname),
          bytes: body.length,
          sha256: sha256Hex(body),
          text: body.toString('utf8'),
        })
      } catch {
        fetchedAssets.set(url, null)
        continue
      }
    }
    const record = fetchedAssets.get(url)
    if (record && DEMO_MARKERS.every((marker) => record.text.includes(marker))) matches.push(record)
  }
  return matches.map((record) => ({ url: record.url, file: record.file, bytes: record.bytes, sha256: record.sha256 }))
}
let provenAsset = null

/* One capture, live-composited: `.iu-stage` is a fixed-height internal scroller,
   so an off-screen raster (`captureBeyondViewport:true`) paints a blank rectangle
   instead of the snap-clipped subtree. Every pixel must therefore be on screen,
   which is asserted rather than assumed. */
const shot = async (name, sel, where) => {
  const el = await waitFor(sel)
  if (!el) {
    await die(browser,
      `ERROR [demo-helper-groups-shoot.mjs] ${where} selector "${sel}" never mounted at ${page.url()}.\n` +
      `  What failed: no element matched "${sel}".\n` +
      `  Why: the demo build predates the grouped-helper demonstration, or the arm's case/state did not apply.\n` +
      `  Where: demo-helper-groups-shoot.mjs shot("${name}").\n` +
      `  Fix: confirm DEMO_ORIGIN serves the proven build, then retry.`)
  }
  const box = await el.boundingBox()
  const viewport = page.viewport()
  if (!box || box.width < 4 || box.height < 4) {
    await die(browser,
      `ERROR [demo-helper-groups-shoot.mjs] ${where} "${sel}" resolved to a blank/zero-size box ${JSON.stringify(box)}.`)
  }
  if (box.y < -0.5 || box.y + box.height > viewport.height + 0.5) {
    await die(browser,
      `ERROR [demo-helper-groups-shoot.mjs] ${where} "${sel}" is not fully on screen (y=${box.y.toFixed(0)}..${(box.y + box.height).toFixed(0)}, viewport ${viewport.height}).\n` +
      `  Why: a live-compositor capture only sees on-screen pixels, so this surface would be clipped.\n` +
      `  Fix: raise the launch viewport height in demo-helper-groups-shoot.mjs, or settle the page before capture.`)
  }
  const file = `${out}/${name}`
  await el.screenshot({ path: file, captureBeyondViewport: false })
  const measured = await gate.assert(name, file, { sel, where: 'demo-helper-groups-shoot.mjs' })
  console.log('shot', name.padEnd(40), `${Math.round(box.width)}x${Math.round(box.height)}`.padEnd(11),
    `nonbg=${(measured.nonbgRatio * 100).toFixed(2)}% colors=${measured.distinctColors} ${(statSync(file).size / 1024).toFixed(1)}KB`)
  return measured
}

const provenanceArms = []
const styleArms = {}
for (const arm of COLLECTIVE_ARMS) {
  const url = `${ORIGIN}/?app=commons&helpers=${encodeURIComponent(arm.demoCase)}${theme === 'light' ? '&theme=light' : ''}#inuse`
  await page.goto(url, { waitUntil: 'networkidle2' })
  await page.evaluate(() => document.getElementById('inuse-stage')?.scrollIntoView({ block: 'center' }))
  await page.evaluate(() => document.fonts.ready)
  await pause(400)

  const demo = await waitFor(`[data-helper-demo="${arm.demoCase}"]`)
  if (!demo) {
    await die(browser,
      `ERROR [demo-helper-groups-shoot.mjs] the demo mounts no "${arm.demoCase}" demonstration at ${url}.\n` +
      `  What failed: no [data-helper-demo="${arm.demoCase}"] appeared within 20s.\n` +
      `  Why: the demo case was renamed or removed, or this is not the proven build.\n` +
      `  Where: demo-helper-groups-shoot.mjs arm "${arm.surface}" readiness wait.\n` +
      `  Means: the reference pane for this arm cannot be attributed to any canonical demonstration.\n` +
      `  Fix: point the arm's demoCase in collective-sxs.mjs at a case the served demo still mounts.`)
  }
  const shell = await page.$eval('.iu-subnav', (el) => el.textContent).catch(() => '')
  if (!shell.includes('explore')) {
    await die(browser,
      `ERROR [demo-helper-groups-shoot.mjs] the in-use demo shell did not mount for arm "${arm.surface}" (url ${url}).`)
  }
  const actualTheme = await page.evaluate(() => document.documentElement.getAttribute('data-theme'))
  if ((theme === 'light') !== (actualTheme === 'light')) {
    await die(browser,
      `ERROR [demo-helper-groups-shoot.mjs] the requested theme did not apply for arm "${arm.surface}".\n` +
      `  What failed: [data-theme]="${actualTheme}" after requesting "${theme}".\n` +
      `  Why: the demo reads the theme from ?theme=light; a dark reference paired with a light app (or vice versa) is not comparable.\n` +
      `  Where: demo-helper-groups-shoot.mjs theme preflight.\n` +
      `  Fix: confirm the proven build applies ?theme=light, then retry.`)
  }

  // Normalize the disclosure state first: `page.goto` to the same URL (as two
  // arms sharing one case + theme do) is a same-document no-op in Chrome, so an
  // arm could otherwise inherit the previous arm's open group and collapse it
  // instead of opening it.
  await page.evaluate(() => {
    for (const trigger of document.querySelectorAll('.helper-group-trigger')) {
      if (trigger.getAttribute('aria-expanded') === 'true') trigger.click()
    }
  })
  await pause(200)
  const controls = await page.$$('.helper-group-trigger')
  if (controls.length < 1) {
    await die(browser,
      `ERROR [demo-helper-groups-shoot.mjs] the "${arm.demoCase}" demonstration mounted no disclosure control.`)
  }
  for (const control of controls) await control.click()
  await page.waitForSelector('.helper-group-members .helper-thread-row', { timeout: 10000 }).catch(async () => {
    await die(browser,
      `ERROR [demo-helper-groups-shoot.mjs] opening the "${arm.demoCase}" disclosure revealed no member row.\n` +
      `  What failed: no ".helper-group-members .helper-thread-row" appeared after the control was pressed.\n` +
      `  Why: the disclosure did not open, or the case mounts no members.\n` +
      `  Where: demo-helper-groups-shoot.mjs arm "${arm.surface}" expansion wait.\n` +
      `  Fix: confirm the served demo mounts this case's members, then retry.`)
  })
  if (arm.demoState === 'selected') {
    const thread = await page.$(`.helper-group-members [data-thread-id="${arm.demoSelectThread}"] input[type="checkbox"]`)
    if (!thread) {
      await die(browser,
        `ERROR [demo-helper-groups-shoot.mjs] arm "${arm.surface}" cannot tick member "${arm.demoSelectThread}".\n` +
        `  What failed: no checkbox for [data-thread-id="${arm.demoSelectThread}"] in the open "${arm.demoCase}" group.\n` +
        `  Fix: update demoSelectThread in collective-sxs.mjs to a member this case mounts.`)
    }
    await thread.click()
    await page.waitForFunction(
      (id) => document.querySelector(`.helper-group-members [data-thread-id="${id}"] input[type="checkbox"]`)?.checked === true,
      { timeout: 10000 },
      arm.demoSelectThread,
    )
  }
  const label = await page.$eval('.helper-group-count', (el) => el.textContent.trim())
  await waitForAnchors(await page.$$eval('.helper-tree input[type="checkbox"]', (els) => els.length))

  const matches = await servedAssets()
  if (matches.length < 1) {
    await die(browser,
      `ERROR [demo-helper-groups-shoot.mjs] the served page carries no attributable bundle for arm "${arm.surface}".\n` +
      `  What failed: no <script src> body carried every marker (${DEMO_MARKERS.join(', ')}).\n` +
      `  Why: DEMO_ORIGIN is serving the unbundled dev server or a stale dist.\n` +
      `  Where: demo-helper-groups-shoot.mjs served-bytes check at ${ORIGIN}.\n` +
      `  Means: the reference pane could not be tied to a build someone can inspect.\n` +
      `  Fix: serve the dist built by the proven checkout (vite preview over dist/) and re-run.`)
  }
  const asset = matches.sort((a, b) => b.bytes - a.bytes)[0]
  if (provenAsset && provenAsset.sha256 !== asset.sha256) {
    await die(browser,
      `ERROR [demo-helper-groups-shoot.mjs] the served build changed between arms.\n` +
      `  What failed: arm "${arm.surface}" resolved ${asset.file} (sha256 ${asset.sha256.slice(0, 16)}…) but an earlier arm resolved ${provenAsset.file} (sha256 ${provenAsset.sha256.slice(0, 16)}…).\n` +
      `  Why: the server was rebuilt, restarted from another dist, or is answering two builds.\n` +
      `  Where: demo-helper-groups-shoot.mjs per-arm served-bytes check.\n` +
      `  Means: the reference panes of one run would not be the same build.\n` +
      `  Fix: stop the server, serve one dist, and re-run the whole theme.`)
  }
  provenAsset = asset
  const localFile = CHECKOUT ? join(CHECKOUT, 'dist', 'assets', asset.file) : null
  const local = PROOF === 'exact-release'
    ? (existsSync(localFile)
        ? { file: localFile, bytes: statSync(localFile).size, sha256: sha256Hex(readFileSync(localFile)) }
        : null)
    : { file: `DEMO_ASSET_SHA256`, bytes: null, sha256: PINNED_DIGEST }
  const digestIssue = digestProblem({ served: asset, local, source: PROOF === 'exact-release' ? `checkout dist ${localFile}` : 'DEMO_ASSET_SHA256' })
  if (digestIssue) {
    await die(browser, digestIssue)
  }

  await shot(arm.demo, '#inuse', `arm "${arm.surface}"`)
  await shot(arm.demoGrouped, '.helper-demo', `arm "${arm.surface}" grouped region`)
  const styles = await measureCollectiveStyles(page, arm.styleScope.reference)
  if (styles.missingScope) {
    await die(browser,
      `ERROR [demo-helper-groups-shoot.mjs] the demo style scope "${arm.styleScope.reference}" did not mount for arm "${arm.surface}".`)
  }
  styleArms[arm.surface] = styles

  const record = { surface: arm.surface, case: arm.demoCase, state: arm.demoState, label, asset, local }
  provenanceArms.push(record)
  console.log('arm', arm.surface.padEnd(34), `case=${arm.demoCase}`, `state=${arm.demoState}`, `label="${label}"`,
    `| wrap count ${describeWrap(styles.wrap.count)} · facts ${describeWrap(styles.wrap.facts)}`)
}

const provenance = {
  theme,
  proof: PROOF,
  pinnedVersion,
  tag: describeTag || null,
  checkout: CHECKOUT || null,
  checkoutVersion,
  remote: ORIGIN,
  markers: DEMO_MARKERS,
  asset: provenanceArms[0].asset,
  local: provenanceArms[0].local,
  arms: provenanceArms,
}
writeFileSync(`${out}/provenance.json`, JSON.stringify(provenance, null, 2))
const stylesPath = `${out}/styles.json`
const previousStyles = existsSync(stylesPath) ? JSON.parse(readFileSync(stylesPath, 'utf8')) : { arms: {} }
writeFileSync(stylesPath, JSON.stringify({ theme, arms: { ...previousStyles.arms, ...styleArms } }, null, 2))

console.log(`\n${provenanceLine(provenance)}`)
console.log('demo computed styles:', JSON.stringify(Object.fromEntries(Object.entries(styleArms).map(([arm, s]) => [arm, s.elements])), null, 2))
console.log('demo wrap measurements:', JSON.stringify(Object.fromEntries(Object.entries(styleArms).map(([arm, s]) => [arm, s.wrap])), null, 2))
console.log(`PROVENANCE_JSON=${JSON.stringify(provenance)}`)
console.log('console errors:', errs.length ? errs.slice(0, 6) : 'none')
await browser.close()
