/* Screenshot the own-profile contributed-collectives section on the REAL profile route.

   Four captures per theme, taken from one live page so they cannot mix builds:
     profile-collectives            the section with every contributed collective and its FOUR counters
     profile-collectives-submissions one collective open, its submissions (pairs) listed
     profile-collectives-history     one submission open, its full event log visible
     profile-collectives-history-page the mounted profile page (own header + body) with that log open

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
   the "none on record" copy genuinely absent. The history also carries a
   legacy terminal row with no decision time, so the unknown-time label is
   asserted on screen and a timestamp in that cell fails the run. All of this
   exists only in this change. A stale server, or one serving another worktree,
   fails with a nonzero exit instead of producing a misleading capture.

   Computed styles are read from the live DOM as well, because a scaled PNG
   cannot tell two close token values apart: the run reports the counter's
   computed font-family, font-variant-numeric (tabular figures are load-bearing
   with three counters side by side) and border-radius (the design system is
   square everywhere).

   env:
     VILLAGE_URL     app URL (default http://localhost:3000/users/alice-dev)
     CHROME_PATH     Chrome/Chromium binary (required)
     PUPPETEER_CORE  explicit module path to puppeteer-core (optional)
     NARROW          when "1", captures at a ~390px mobile viewport instead of
                      the default desktop one — nobody had looked at this
                      section below ~1040px before this flag existed, and the
                      four counters + toggle + name + description compete hard
                      for space at 390px.
   usage: VILLAGE_URL=... CHROME_PATH=... node profile-collectives-shoot.mjs <theme> <outdir>
          VILLAGE_URL=... CHROME_PATH=... NARROW=1 node profile-collectives-shoot.mjs <theme> <outdir>
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

const NARROW = process.env.NARROW === '1'
const BASE_VP = NARROW
  ? { width: 390, height: 1400, deviceScaleFactor: 1 }
  : { width: 1396, height: 939, deviceScaleFactor: 1 }

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

// A reader must be able to identify every collective WITHOUT expanding it —
// this is a structural check (rendered scrollWidth vs clientWidth), not a
// textContent check, because a CSS-only regression (min-width removed, the
// wrapper's shrink-0 dropped) leaves the name STRING unchanged in the DOM
// while visually crushing it to an ellipsis; only measuring the box the
// browser actually laid out catches that. This is the layout an earlier
// round of this exact gate FAILED to catch — the four-counter column
// squeezed the collapsed name cell to a few characters ("AI…") while every
// other assertion here (unit wording, row count, computed styles) stayed
// green, and only a human looking at the PNG caught it.
const nameLegibility = await page.evaluate(() =>
  [...document.querySelectorAll('[data-testid="contributed-collective"]')].map((row) => {
    const link = row.querySelector('a')
    if (!link) return { text: null, truncated: true }
    return {
      text: link.textContent.trim(),
      // +1 tolerates sub-pixel layout rounding, not real truncation.
      truncated: link.scrollWidth > link.clientWidth + 1,
    }
  }),
)
const truncatedNames = nameLegibility.filter((n) => n.truncated)
if (truncatedNames.length > 0) {
  await browser.close()
  die(2, `${truncatedNames.length} collapsed collective name(s) render truncated: ${JSON.stringify(truncatedNames.map((n) => n.text))}.`,
    'the name column has been squeezed below its guaranteed minimum width by its siblings (the counters, the toggle button) — most likely the name column lost its min-width, or the counters+button cluster lost the shrink-0 that keeps it from crushing the name instead of wrapping.',
    'a reader could not identify which collective a collapsed row belongs to from an ellipsis alone; this is the layout regression a real reviewer caught by eye after every automated check passed.',
    'confirm ProfileCollectives.tsx keeps the name column\'s min-width and the counters+button cluster\'s shrink-0 (see the comments at that JSX), rebuild from this worktree, and retry.')
}

// The fourth counter: withdrawn events, grouping retracted + revoked. Its
// presence is asserted here — this is the counter a real user acceptance
// test found missing. The per-counter unit FOOTER this gate used to assert
// here was removed at the user's explicit instruction (UAT impl-adjustment):
// each number no longer carries its own "transcripts"/"submission attempts"
// line. The distinction those units stated is still load-bearing copy, so it
// still has exactly one home — the units SENTENCE above the counters — and
// the mutation this block used to prove RED (the withdrawn counter's own
// footer mislabeling it as counting transcripts) no longer has a footer to
// target; it is retargeted below at that sentence instead.
const withdrawnCounter = await page.$('[data-testid="counter-withdrawn"]')
if (!withdrawnCounter) {
  await browser.close()
  die(2, 'the withdrawn counter never rendered.',
    'the served build predates the fourth counter, or the collective row filters it out.',
    'the capture would not show the counter this change adds.',
    'rebuild from this worktree, confirm the mock serves withdrawn_attempt_count, and retry.')
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
// change exists to fix. This is also where the withdrawn-counter mutation
// this gate used to prove RED via the (now-removed) per-counter unit footer
// is retargeted: the sentence, not a footer, is now the only place that
// states withdrawn counts submission attempts rather than transcripts, so a
// regression mislabeling it must show up here.
const explanationText = await page.$eval(
  '[data-testid="profile-collectives"] > div > p',
  (el) => el.textContent.trim(),
)
if (
  !/rejected and withdrawn count submission/.test(explanationText) ||
  /withdrawn count transcripts/.test(explanationText)
) {
  await browser.close()
  die(2, `the units sentence reads ${JSON.stringify(explanationText)}.`,
    'the served bytes still describe only three counters, or describe the withdrawn counter incorrectly — including labeling it as counting transcripts rather than submission attempts.',
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

const shoot = async (rawName, sel) => {
  // Auto-suffixed rather than left to the caller so a narrow run can never
  // silently overwrite the desktop capture it shares an outdir with.
  const name = NARROW ? `${rawName}-narrow` : rawName
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
await shoot('profile-prompt-settings', '[data-testid="profile-prompt-settings"]')
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
const UNKNOWN_DECISION_TIME = 'decision time not recorded'
const eventRows = await page.$$eval('[data-testid="share-event"]', (els) =>
  els.map((el) => `${el.dataset.eventNum}:${el.dataset.eventStatus}:${el.textContent.replace(/\s+/g, ' ').trim()}`),
)
const eventTimes = await page.$$eval('[data-testid="share-event"]', (els) =>
  els.map((el) => {
    const cell = el.querySelector('[data-testid="share-event-time"]')
    return {
      status: el.dataset.eventStatus,
      marked: cell?.hasAttribute('data-time-known') === true,
      known: cell?.dataset.timeKnown === 'true',
      text: cell?.textContent?.trim() ?? '',
    }
  }),
)
if (eventTimes.some((row) => !row.marked)) {
  await browser.close()
  die(2, 'the served event-time cells do not mark whether their time is known.',
    'the served build predates the legacy-unknown UI change, so the cell has no data-time-known marker.',
    'the capture would come from an older build and could not prove this change.',
    'rebuild and restart the app from THIS worktree, then retry.')
}
// The changed state must be on screen, not merely mocked: a legacy terminal
// attempt has no recorded decision time, so the cell must carry the explicit
// unknown label and no timestamp. A stale build that still substitutes
// recorded_at fails here before any PNG is written.
const unknownTimes = eventTimes.filter((row) => !row.known)
if (unknownTimes.length === 0) {
  await browser.close()
  die(2, 'the event log has no row with an unknown decision time.',
    'the served build predates the legacy-unknown UI, or the mock no longer serves a terminal row with decided_at null.',
    'the capture would not show the changed state and would prove nothing about the fix.',
    'rebuild from this worktree and confirm the mock serves the legacy Strict Curators history, then retry.')
}
for (const row of unknownTimes) {
  if (row.text !== UNKNOWN_DECISION_TIME) {
    await browser.close()
    die(2, `an unknown decision time renders ${JSON.stringify(row.text)}, expected ${JSON.stringify(UNKNOWN_DECISION_TIME)}.`,
      'the row is formatting a date for a terminal attempt whose decision time is null.',
      'the capture would show a fabricated decision time, which is the defect this change removes.',
      'restore the explicit unknown label in ShareEventLog and retry.')
  }
  if (/\d{4}-\d{2}-\d{2} \d{2}:\d{2}/.test(row.text)) {
    await browser.close()
    die(2, `an unknown decision time renders a timestamp: ${JSON.stringify(row.text)}.`,
      'recorded_at is still being substituted for the missing decision time.',
      'the capture would show invented evidence as a decision time.',
      'restore the explicit unknown label in ShareEventLog and retry.')
  }
}
// Any still-open attempt in this history must keep showing its submission
// time. (The mounted fixture corpus also carries a pending row beside a legacy
// unknown terminal row; this capture's history has none.)
const pendingRows = eventTimes.filter((row) => row.status === 'pending')
if (pendingRows.some((row) => !row.known || !/\d{4}-\d{2}-\d{2} \d{2}:\d{2}/.test(row.text))) {
  await browser.close()
  die(2, `pending events do not render their submission time: ${JSON.stringify(pendingRows)}.`,
    'the unknown-time change also removed the submission time from a still-open attempt.',
    'the capture would show a pending event with no honest time.',
    'keep the recorded_at display for pending events and retry.')
}
const knownTerminalRows = eventTimes.filter((row) => row.status !== 'pending' && row.known)
if (knownTerminalRows.length === 0) {
  await browser.close()
  die(2, 'no decided event with a recorded decision time rendered alongside the unknown row.',
    'the fixture no longer serves a normally decided row, or the row stopped reading decided_at.',
    'the capture could not show that a known decision time still renders correctly.',
    'restore the decided rows in the mock fixture and retry.')
}
// The log claims to read oldest first. Unknown rows carry no decision time to
// order by; every row that does carry one must still be at or after the
// previous known row.
let previousKnown = null
const backwards = eventTimes.findIndex((row) => {
  if (!row.known) return false
  const worse = previousKnown !== null && row.text < previousKnown
  previousKnown = row.text
  return worse
})
if (backwards >= 0) {
  await browser.close()
  die(1, `the event log shows a time running backwards at row ${backwards + 1}: ${JSON.stringify(eventTimes[backwards].text)}.`,
    'either the fixture stamps events with times that cannot occur in that order, or the row renders the wrong time field.',
    'the capture would show an audit log contradicting its own oldest-first claim, which is misleading evidence.',
    'fix the fixture so each attempt is recorded after the previous one closed, or fix the field the row reads, then recapture.')
}
const shownTimes = eventTimes.map((row) => row.text)
// Nothing in the log may bleed past its box. At a narrow viewport the row used
// to keep its timestamp unshrinkable and squeeze the label word-per-line while
// the time ran off the right edge; the row now wraps instead. This is a
// structural check (scrollWidth vs clientWidth on the list and every row), not
// a text check, because an overflow leaves textContent untouched.
const overflow = await page.$$eval('[data-testid="share-event-log"], [data-testid="share-event"]', (els) =>
  els.filter((el) => el.scrollWidth > el.clientWidth + 1).map((el) => el.textContent.replace(/\s+/g, ' ').trim().slice(0, 60)),
)
if (overflow.length > 0) {
  await browser.close()
  die(1, `the event log bleeds past its box in ${overflow.length} element(s): ${JSON.stringify(overflow)}.`,
    'a row keeps an unshrinkable child wider than the space the viewport leaves it, so the content overflows instead of wrapping.',
    'the capture would show timestamps cut off at the edge of the card, which is what a person sees at this width.',
    'let the row wrap (flex-wrap) and let the timestamp fall to its own line, then recapture.')
}
// The legacy row must not replace or truncate the newer history: the mock
// serves four events for this pair, and every one must still render.
if (eventRows.length !== 4) {
  await browser.close()
  die(1, `the event log rendered ${eventRows.length} row(s), expected the fixture's 4.`,
    'the legacy unknown-time row replaced or shortened the newer history, or the build dropped rows.',
    'the capture would not show that the whole history survives an unknown decision time.',
    'confirm the mock serves all four Strict Curators events and that ShareEventLog maps every row, then retry.')
}
// The changed state belongs on the mounted production path, so carry the
// profile page's own header and body in one capture beside the section crop.
const pageHeader = await page.$('main#main header.on-head')
if (!pageHeader) {
  await browser.close()
  die(2, 'the profile page header did not render above the history.',
    'the page shell changed or the capture ran on a surface that is not the mounted profile route.',
    'the full-page evidence would not show the changed state inside the production page.',
    'confirm VILLAGE_URL is /users/alice-dev and that the profile route still renders its hero header, then retry.')
}
await pause(300)
await shoot('profile-collectives-history', '[data-testid="profile-collectives"]')
await shoot('profile-collectives-history-page', 'main#main')

console.log('provenance collapsed-name-legibility =', JSON.stringify(nameLegibility))
console.log('provenance withdrawn-value =', JSON.stringify(withdrawnCounterValue))
console.log('provenance units-sentence =', JSON.stringify(explanationText))
console.log('provenance opened-withdrawn-only-collective =', opened)
console.log('provenance collective-rows =', rows.length, '| pending-only listed =', pendingOnly)
console.log('provenance unknown-decision-times =', JSON.stringify(unknownTimes))
console.log('provenance known-terminal-times =', JSON.stringify(knownTerminalRows))
console.log('provenance event-times =', shownTimes.join('  ->  '))
console.log('provenance event-rows =')
for (const r of eventRows) console.log('   ', r)
console.log('computed styles =', JSON.stringify(probe, null, 2))
console.log('console errors:', errs.length ? errs.slice(0, 6) : 'none')
await browser.close()
