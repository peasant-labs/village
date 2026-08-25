/* Screenshot the own-profile contributed-collectives section on the REAL profile route.

   Three captures per theme, taken from one live page so they cannot mix builds:
     profile-collectives            the section with every contributed collective and its FOUR counters
     profile-collectives-submissions one collective open, its submissions (pairs) listed
     profile-collectives-history     one submission open, its full event log visible

   The collective opened for the submissions/history captures is deliberately
   the one whose only pair is a withdrawal ("Strict Curators": refused three
   times, then withdrawn) — that pair has NO current-state row, so it is the
   exact case a real user acceptance test caught contradicting itself: a
   nonzero "3 submission attempts" counter beside "no submissions of yours are
   on record". This capture proves the row now renders, with a chip reading
   "withdrawn", instead of the empty-state copy.

   Build provenance is asserted BEFORE any PNG is written: the served page must
   carry the section, FOUR counters (including "withdrawn"), the unit wording
   that states the attempts-versus-transcripts asymmetry, and — after opening
   the withdrawn-only collective — a submission row with a "withdrawn" chip and
   the "none on record" copy genuinely absent. All of this exists only in this
   change. A stale server, or one serving another worktree, fails with a
   nonzero exit instead of producing a misleading capture.

   Computed styles are read from the live DOM as well, because a scaled PNG
   cannot tell two close token values apart: the run reports the counter's
   computed font-family, font-variant-numeric (tabular figures are load-bearing
   with three counters side by side) and border-radius (the design system is
   square everywhere).

   env:
     VILLAGE_URL     app URL (default http://localhost:3000/users/alice-dev)
     CHROME_PATH     Chrome/Chromium binary (required)
     PUPPETEER_CORE  explicit module path to puppeteer-core (optional)
   usage: VILLAGE_URL=... CHROME_PATH=... node profile-collectives-shoot.mjs <theme> <outdir>
*/
import { mkdirSync, statSync } from 'node:fs'
import { SurfaceGate } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const CHROME = process.env.CHROME_PATH
const URL = process.env.VILLAGE_URL || 'http://localhost:3000/users/alice-dev'
const theme = process.argv[2] || 'dark'
const out = process.argv[3] || `/tmp/profile-collectives-${theme}`
mkdirSync(out, { recursive: true })

const BASE_VP = { width: 1396, height: 939, deviceScaleFactor: 1 }

if (!CHROME) {
  console.error('ERROR [profile-collectives-shoot.mjs] CHROME_PATH is unset — set it to your Chrome/Chromium binary.')
  process.exit(1)
}

const die = (code, what, why, means, fix) => {
  console.error(
    `ERROR [profile-collectives-shoot.mjs] ${what}
  Why: ${why}
  Where: profile-collectives-shoot.mjs, theme=${theme}, url=${URL}.
  Means: ${means}
  Fix: ${fix}`,
  )
  process.exit(code)
}

const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', defaultViewport: { ...BASE_VP } })
const page = await browser.newPage()
await page.setCookie({ name: 'peasant_token', value: 'demo-token', domain: 'localhost', path: '/' })
await applyDeterminism(page)
const errs = []
page.on('console', (m) => { if (m.type() === 'error' && !/favicon|404|hydrat/.test(m.text())) errs.push(m.text()) })
page.on('pageerror', (e) => errs.push('pageerr: ' + e.message))

await page.goto(URL, { waitUntil: 'networkidle0' })
await page.evaluate((next) => localStorage.setItem('peasant-theme', next), theme)
await page.reload({ waitUntil: 'networkidle0' })
await new Promise((r) => setTimeout(r, 900))

const actualTheme = await page.evaluate(() => document.documentElement.getAttribute('data-theme'))
if (actualTheme !== theme) {
  await browser.close()
  die(3, `the requested theme did not apply: [data-theme]="${actualTheme}" after requesting "${theme}".`,
    'the theme toggle / localStorage handshake did not settle.',
    'every capture would be the wrong theme.',
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
const section = await waitFor('[data-testid="profile-collectives"]')
if (!section) {
  await browser.close()
  die(2, 'the contributed-collectives section never rendered.',
    'the served build predates the section, the viewer is not the profile owner, or the contributions request failed.',
    'the capture would prove nothing about this change.',
    'rebuild and restart the app from THIS worktree, confirm the mock serves /users/me/collectives/contributions and /auth/me for the same handle, and retry.')
}

const rows = await page.$$('[data-testid="contributed-collective"]')
if (rows.length < 2) {
  await browser.close()
  die(2, `only ${rows.length} contributed collective row(s) rendered.`,
    'the served build lists fewer collectives than the fixture serves, so a collective is being filtered out.',
    'the capture could not show that a collective with nothing approved is still listed.',
    'confirm the mock serves the full contributions list and that no client-side filter drops rows, then retry.')
}

const unitText = await page.$eval('[data-testid="counter-rejected-attempts-unit"]', (el) => el.textContent.trim())
if (unitText !== 'submission attempts') {
  await browser.close()
  die(2, `the rejected counter's unit reads ${JSON.stringify(unitText)}.`,
    'the served bytes do not carry the wording that states what this counter measures.',
    'the capture would show two numbers that count different things with nothing saying so.',
    'rebuild from this worktree and retry.')
}

// The fourth counter: withdrawn events, grouping retracted + revoked. Its
// presence and unit are asserted the same way the rejected counter's is
// above — this is the counter a real user acceptance test found missing.
const withdrawnCounter = await page.$('[data-testid="counter-withdrawn"]')
if (!withdrawnCounter) {
  await browser.close()
  die(2, 'the withdrawn counter never rendered.',
    'the served build predates the fourth counter, or the collective row filters it out.',
    'the capture would not show the counter this change adds.',
    'rebuild from this worktree, confirm the mock serves withdrawn_attempt_count, and retry.')
}
const withdrawnUnitText = await page.$eval('[data-testid="counter-withdrawn-unit"]', (el) => el.textContent.trim())
if (withdrawnUnitText !== 'submission attempts') {
  await browser.close()
  die(2, `the withdrawn counter's unit reads ${JSON.stringify(withdrawnUnitText)}.`,
    'the served bytes label the withdrawn counter as counting transcripts rather than events.',
    'the capture would show a unit that contradicts the counter it labels — exactly the mutation this slice proves RED.',
    'rebuild from this worktree and retry.')
}
const withdrawnCounterValue = await page.evaluate(() =>
  [...document.querySelectorAll('[data-testid="contributed-collective"]')]
    .find((row) => row.textContent.includes('Strict Curators'))
    ?.querySelector('[data-testid="counter-withdrawn"]')?.children?.[1]?.textContent?.trim() ?? '',
)
if (withdrawnCounterValue !== '1') {
  await browser.close()
  die(2, `Strict Curators' withdrawn counter reads ${JSON.stringify(withdrawnCounterValue)}, expected to contain 1.`,
    'the served counter does not match the fixture, or the row is not the withdrawn-only collective expected here.',
    'the capture would misreport what this counter measures for the collective the capture is built around.',
    'confirm the mock serves withdrawn_attempt_count: 1 for Strict Curators and retry.')
}

// The units sentence above the counters must still describe the row now that
// FOUR counters render, two counting transcripts and two counting events. A
// sentence that stops matching its own numbers is the defect class this
// change exists to fix.
const explanationText = await page.$eval(
  '[data-testid="profile-collectives"] > div > p',
  (el) => el.textContent.trim(),
)
if (!/rejected and withdrawn count submission/.test(explanationText)) {
  await browser.close()
  die(2, `the units sentence reads ${JSON.stringify(explanationText)}.`,
    'the served bytes still describe only three counters, or describe the withdrawn counter incorrectly.',
    'the capture would show a copy line that no longer matches the four numbers beneath it — the exact contradiction UAT caught.',
    'rebuild from this worktree and retry.')
}

const pendingOnly = await page.evaluate(() =>
  [...document.querySelectorAll('[data-testid="contributed-collective"]')].some((row) => {
    const approved = row.querySelector('[data-testid="counter-approved"]')?.textContent ?? ''
    const pending = row.querySelector('[data-testid="counter-pending"]')?.textContent ?? ''
    return /approved\s*0/.test(approved.replace(/\s+/g, ' ')) && !/awaiting review\s*0/.test(pending.replace(/\s+/g, ' '))
  }),
)
if (!pendingOnly) {
  await browser.close()
  die(2, 'no listed collective has zero approved contributions and some awaiting review.',
    'either the fixture no longer contains one, or the served build hides it.',
    'the capture could not show the case the section exists to make visible.',
    'confirm the mock serves a collective with approved 0 and pending > 0, and that nothing filters it out.')
}

// ── Computed styles (a scaled PNG cannot tell two close token values apart) ──
const probe = await page.evaluate(() => {
  const pick = (el) => {
    if (!el) return null
    const cs = getComputedStyle(el)
    return {
      fontFamily: cs.fontFamily,
      fontSize: cs.fontSize,
      fontVariantNumeric: cs.fontVariantNumeric,
      color: cs.color,
      borderRadius: cs.borderRadius,
      lineHeight: cs.lineHeight,
    }
  }
  const row = document.querySelector('[data-testid="contributed-collective"]')
  const counter = row?.querySelector('[data-testid="counter-rejected-attempts"]')
  return {
    section: pick(document.querySelector('[data-testid="profile-collectives"]')),
    row: pick(row),
    counterLabel: pick(counter?.children?.[0]),
    counterValue: pick(counter?.children?.[1]),
    counterUnit: pick(counter?.children?.[2]),
  }
})

const shoot = async (name, sel) => {
  const el = await page.$(sel)
  if (!el) {
    await browser.close()
    die(1, `${sel} did not resolve for capture ${name}.`,
      'the surface never mounted at capture time.',
      'the PNG would be empty or missing.',
      'confirm the interaction that reveals it succeeded, then retry.')
  }
  const box = await el.boundingBox()
  if (!box || box.width < 4 || box.height < 4) {
    await browser.close()
    die(1, `${sel} resolved to a blank or zero-size box: ${JSON.stringify(box)}.`,
      'the surface did not lay out.',
      'the PNG would be empty.',
      'confirm the route is reachable and the fixtures loaded.')
  }
  const file = `${out}/${name}.png`
  await el.screenshot({ path: file, captureBeyondViewport: true })
  const r = await gate.assert(name, file, { sel, where: 'profile-collectives-shoot.mjs' })
  const bytes = statSync(file).size
  console.log('shot', name.padEnd(32), `${Math.round(box.width)}x${Math.round(box.height)}`.padEnd(11), `nonbg=${(r.nonbgRatio * 100).toFixed(1)}% colors=${r.distinctColors} ${(bytes / 1024).toFixed(1)}KB`)
  return file
}

await page.evaluate(() => document.querySelector('[data-testid="profile-collectives"]').scrollIntoView({ block: 'center' }))
await pause(300)
await shoot('profile-collectives', '[data-testid="profile-collectives"]')

// Open "Strict Curators" — the collective whose only pair was refused three
// times and then withdrawn — so the withdrawn row is what this capture shows,
// not an incidental approved/pending row. Falls back to the first row only if
// that name is not found, so the script still runs against an older fixture.
const opened = await page.evaluate(() => {
  const rows = [...document.querySelectorAll('[data-testid="contributed-collective"]')]
  const row = rows.find((r) => r.textContent.includes('Strict Curators')) ?? rows[0]
  row?.querySelector('button')?.click()
  return row?.textContent.includes('Strict Curators') ?? false
})
const submission = await waitFor('[data-testid="collective-submission"]')
if (!submission) {
  await browser.close()
  die(1, 'opening a collective produced no submissions.',
    'the submissions request failed or returned nothing for that collective.',
    'the history capture would have no entry point.',
    'confirm the mock serves /users/me/collectives/{groupId}/submissions for the opened collective and retry.')
}

// The pairs endpoint's whole point: a fully-withdrawn pair is a ROW, with a
// chip reading "withdrawn", not an empty list. Asserted structurally (the
// chip element and its text) rather than by scanning body text, and the
// empty-state node's ABSENCE is asserted too — a CSS-only defect can leave
// textContent intact while hiding nothing, so presence/absence of the node
// itself is what is checked.
if (opened) {
  const chipText = await page.$eval(
    '[data-testid="collective-submission-status"]',
    (el) => el.textContent.trim(),
  )
  if (chipText !== 'withdrawn') {
    await browser.close()
    die(2, `the withdrawn pair's chip reads ${JSON.stringify(chipText)}.`,
      'the served build does not group retracted/revoked pairs under a "withdrawn" chip.',
      'the capture would not show the state this endpoint exists to surface.',
      'rebuild from this worktree and retry.')
  }
  const emptyCopy = await page.$('[data-testid="collective-submissions-empty"]')
  if (emptyCopy) {
    await browser.close()
    die(2, 'the "no submissions of yours are on record" copy is present alongside a real row.',
      'the empty-state condition is not keyed to the pairs list actually being empty.',
      'the capture would show the exact contradiction UAT caught: a nonzero counter beside "none on record".',
      'confirm the empty condition is pairs.length === 0, not the current-state list, and retry.')
  }
}
await pause(300)
await shoot('profile-collectives-submissions', '[data-testid="profile-collectives"]')

await page.evaluate(() => {
  document.querySelector('[data-testid="collective-submission"]').querySelector('button').click()
})
const log = await waitFor('[data-testid="share-event-log"]')
if (!log) {
  await browser.close()
  die(1, 'opening a submission produced no event log.',
    'the events request failed or returned nothing for that (transcript, collective) pair.',
    'the capture could not show the audit log this change adds.',
    'confirm the mock serves the events route for that pair and retry.')
}
const eventRows = await page.$$eval('[data-testid="share-event"]', (els) =>
  els.map((el) => `${el.dataset.eventNum}:${el.dataset.eventStatus}:${el.textContent.replace(/\s+/g, ' ').trim()}`),
)
// The log claims to read oldest first. If the timestamps disagree, the capture
// would be evidence of a chronology that cannot happen, so the run fails rather
// than producing it — this is exactly the defect that reached a screenshot once.
const shownTimes = await page.$$eval('[data-testid="share-event-time"]', (els) =>
  els.map((el) => el.textContent.trim()),
)
const backwards = shownTimes.findIndex((t, i) => i > 0 && t < shownTimes[i - 1])
if (backwards > 0) {
  await browser.close()
  die(1, `the event log shows a time running backwards: row ${backwards + 1} reads ${JSON.stringify(shownTimes[backwards])} after row ${backwards} reads ${JSON.stringify(shownTimes[backwards - 1])}.`,
    'either the fixture stamps events with times that cannot occur in that order, or the row renders the wrong time field.',
    'the capture would show an audit log contradicting its own oldest-first claim, which is misleading evidence.',
    'fix the fixture so each attempt is recorded after the previous one closed, or fix the field the row reads, then recapture.')
}
if (eventRows.length < 2) {
  await browser.close()
  die(1, `the event log rendered ${eventRows.length} row(s).`,
    'the served build renders fewer events than the fixture supplies.',
    'the capture would not show the log as an ordered history.',
    'confirm the mock serves the full event list and retry.')
}
await pause(300)
await shoot('profile-collectives-history', '[data-testid="profile-collectives"]')

console.log('provenance rejected-unit =', JSON.stringify(unitText))
console.log('provenance withdrawn-unit =', JSON.stringify(withdrawnUnitText), '| withdrawn-value =', JSON.stringify(withdrawnCounterValue))
console.log('provenance units-sentence =', JSON.stringify(explanationText))
console.log('provenance opened-withdrawn-only-collective =', opened)
console.log('provenance collective-rows =', rows.length, '| pending-only listed =', pendingOnly)
console.log('provenance event-times =', shownTimes.join('  ->  '))
console.log('provenance event-rows =')
for (const r of eventRows) console.log('   ', r)
console.log('computed styles =', JSON.stringify(probe, null, 2))
console.log('console errors:', errs.length ? errs.slice(0, 6) : 'none')
await browser.close()
