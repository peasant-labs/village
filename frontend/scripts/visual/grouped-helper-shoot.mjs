/* Screenshot the grouped-helper surfaces on the REAL routes.

   Four arms, each a production state this change is about:

     discovery      `/explore` — a helper-only grouped result must mount exactly
                    ONE owner-unavailable disclosure (one context notice, one
                    control), and expanding it mounts exactly one individually
                    linked member. A second renderer for the same context would
                    double the group, which is what the single-count assertion
                    refuses.
     profile        `/users/{owner}` — the grouped read pages, so the
                    continuation control names the server's remaining count and
                    reaches the later helper-only container on the next page.
     project        `/users/{owner}/projects/{hash}` — the same continuation on
                    the project surface.
     member-failure `/` — the registered member endpoint refuses, so the open
                    group must state the failure and offer a retry instead of
                    claiming a successful empty result.
     collective-browse   `/groups/{id}` — the collective's own browse list
                    carries the saved helper group under its owner row, read
                    only (no per-member selection on a browse surface).
     collective-contribute `/groups/{id}/contribute` — expanding the group and
                    ticking ONE member arms the contribution action with exactly
                    one transcript; no group id is ever sent.
     collective-review   `/groups/{id}/review` — the same explicit per-member
                    selection on the owner's review queue.

   Build provenance is asserted BEFORE any PNG is written: the served build must
   carry the grouped-helper host, the generated scope-first member requests, and
   the continuation control this change introduces. A stale server, or one
   serving another worktree, exits nonzero instead of writing a misleading PNG.

   Computed styles are read from the live DOM too: a scaled PNG cannot tell two
   close token values apart, so each arm asserts the design system's square
   radius and mono chrome on the element the capture is judged on.

   env:
     GROUPED_SHOOT_SURFACE  discovery | profile | project | member-failure |
                            collective-browse | collective-contribute |
                            collective-review
     GROUPED_SHOOT_OWNER    profile/project owner handle (default alice-dev)
     GROUPED_SHOOT_HASH     64-hex project hash for the project arm
     GROUPED_SHOOT_COLLECTIVE     collective id for the collective arms
     GROUPED_SHOOT_COLLECTIVE_ROW the owner-row transcript id the group hangs under
     VILLAGE_ORIGIN         app origin (default http://localhost:3000)
     VILLAGE_URL            overrides the arm's URL
     CHROME_PATH            Chrome/Chromium binary (required)
     PUPPETEER_CORE         explicit module path to puppeteer-core (optional)
   usage: GROUPED_SHOOT_SURFACE=discovery CHROME_PATH=... node grouped-helper-shoot.mjs <theme> <outdir>
*/
import { mkdirSync, statSync } from 'node:fs'
import { SurfaceGate } from './surface-gate.mjs'
import { applyDeterminism } from './determinism.mjs'
const puppeteer = (await import(process.env.PUPPETEER_CORE || 'puppeteer-core')).default

const CHROME = process.env.CHROME_PATH
const SURFACE = process.env.GROUPED_SHOOT_SURFACE || 'discovery'
const SURFACES = [
  'discovery',
  'profile',
  'project',
  'member-failure',
  'collective-browse',
  'collective-contribute',
  'collective-review',
  'collective-browse-empty-flat',
  'collective-contribute-empty-flat',
  'collective-contribute-later',
]
const theme = process.argv[2] || 'dark'
const out = process.argv[3] || `/tmp/village-grouped-${theme}`
mkdirSync(out, { recursive: true })

if (!SURFACES.includes(SURFACE)) {
  console.error(`ERROR [grouped-helper-shoot.mjs] GROUPED_SHOOT_SURFACE=${SURFACE} is not one of ${SURFACES.join(', ')}.`)
  process.exit(1)
}
if (!CHROME) {
  console.error('ERROR [grouped-helper-shoot.mjs] CHROME_PATH is unset — set it to your Chrome/Chromium binary.')
  process.exit(1)
}

const ORIGIN = (process.env.VILLAGE_ORIGIN || 'http://localhost:3000').replace(/\/$/, '')
const OWNER = process.env.GROUPED_SHOOT_OWNER || 'alice-dev'
const HASH = process.env.GROUPED_SHOOT_HASH || '1'.repeat(64)
const COLLECTIVE = process.env.GROUPED_SHOOT_COLLECTIVE || '50000000-0000-4000-8000-000000000001'
const COLLECTIVE_ROW = process.env.GROUPED_SHOOT_COLLECTIVE_ROW || '22222222-2222-4222-8222-000000000001'
const COLLECTIVE_GROUP = process.env.GROUPED_SHOOT_COLLECTIVE_GROUP || 'hg_capture_owner'
const ARM_URL = {
  discovery: `${ORIGIN}/explore`,
  profile: `${ORIGIN}/users/${encodeURIComponent(OWNER)}`,
  project: `${ORIGIN}/users/${encodeURIComponent(OWNER)}/projects/${HASH}`,
  // The failure arm reuses the home mount, whose grouped read is single-page:
  // one disclosure to open, one failing member request to state.
  'member-failure': `${ORIGIN}/`,
  'collective-browse': `${ORIGIN}/groups/${COLLECTIVE}`,
  'collective-contribute': `${ORIGIN}/groups/${COLLECTIVE}/contribute`,
  'collective-review': `${ORIGIN}/groups/${COLLECTIVE}/review`,
  'collective-browse-empty-flat': `${ORIGIN}/groups/${COLLECTIVE}`,
  'collective-contribute-empty-flat': `${ORIGIN}/groups/${COLLECTIVE}/contribute`,
  'collective-contribute-later': `${ORIGIN}/groups/${COLLECTIVE}/contribute`,
}
const URL = process.env.VILLAGE_URL || ARM_URL[SURFACE]

const BASE_VP = { width: 1396, height: 939, deviceScaleFactor: 1 }
const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', defaultViewport: { ...BASE_VP } })
const page = await browser.newPage()
await page.setCookie({ name: 'peasant_token', value: 'demo-token', domain: 'localhost', path: '/' })
await applyDeterminism(page)
const errs = []
page.on('console', (m) => { if (m.type() === 'error' && !/favicon|404|hydrat/.test(m.text())) errs.push(m.text()) })
page.on('pageerror', (e) => errs.push('pageerr: ' + e.message))

const pause = (ms) => new Promise((r) => setTimeout(r, ms))
const gate = new SurfaceGate(page)

const fail = async (message, code) => {
  await browser.close()
  console.error(message)
  process.exit(code)
}

const waitFor = async (sel, timeoutMs = 15000) => {
  const start = Date.now()
  while (Date.now() - start < timeoutMs) {
    const el = await page.$(sel)
    if (el) return el
    await pause(100)
  }
  return null
}

/* Assert what a selector's COMPUTED style actually is, and fail closed when it
   is not: a surface can ship unstyled while every mount-and-served check stays
   green, and a scaled PNG cannot tell the difference. */
const assertComputed = async (sel, expect, label) => {
  const got = await page.evaluate(
    (selector, props) => {
      const el = document.querySelector(selector)
      if (!el) return null
      const style = getComputedStyle(el)
      return Object.fromEntries(props.map((p) => [p, style[p]]))
    },
    sel,
    Object.keys(expect),
  )
  if (!got) {
    await fail(`ERROR [grouped-helper-shoot.mjs] ${label} is not present, so its computed style cannot be checked.
  What failed: no element matched "${sel}".
  Where: grouped-helper-shoot.mjs computed-style probe.
  Fix: rebuild and restart the server from this worktree, then retry.`, 2)
  }
  const wrong = Object.entries(expect).filter(([prop, ok]) => !ok(got[prop]))
  if (wrong.length > 0) {
    await fail(`ERROR [grouped-helper-shoot.mjs] ${label} does not carry the design system's computed styles.
  What failed: ${wrong.map(([prop]) => `${prop}=${JSON.stringify(got[prop])}`).join(', ')}.
  Where: grouped-helper-shoot.mjs computed-style probe on "${sel}".
  Fix: confirm the served build includes the app stylesheet, then retry.`, 2)
  }
  return got
}
const isSquare = (value) => value === '0px'
const isMono = (value) => /atkinson/i.test(value ?? '')

/* Screenshot the named element (measured, not gated) plus the full page body,
   which IS gated: the body carries the panel the changed surface lives in, and
   its size keeps the non-empty floor meaningful for a whole page. */
const capture = async (name, panelSel, where) => {
  await page.evaluate(() => window.scrollTo(0, 0))
  await pause(150)
  const body = await page.$('body')
  const box = await body.boundingBox()
  if (!box || box.width < 4 || box.height < 4) {
    await fail(`ERROR [grouped-helper-shoot.mjs] body resolved to a blank box at ${URL}.`, 1)
  }
  const panelFile = `${out}/${name}-panel.png`
  if (panelSel != null) {
    const panel = await page.$(panelSel)
    if (!panel) {
      await fail(`ERROR [grouped-helper-shoot.mjs] ${where} panel "${panelSel}" is not on screen, so it cannot be captured.`, 2)
    }
    await panel.screenshot({ path: panelFile, captureBeyondViewport: true })
    const measured = await gate.measure(panelFile)
    console.log('shot', `${name}-panel`.padEnd(34), `${measured.w}x${measured.h}`.padEnd(11), `nonbg=${(measured.nonbgRatio * 100).toFixed(2)}% colors=${measured.distinctColors} ${(statSync(panelFile).size / 1024).toFixed(1)}KB (focused, measured)`)
  }
  const file = `${out}/${name}.png`
  await body.screenshot({ path: file, captureBeyondViewport: true })
  const r = await gate.assert(name, file, { sel: 'body', where: 'grouped-helper-shoot.mjs' })
  console.log('shot', name.padEnd(34), `${Math.round(box.width)}x${Math.round(box.height)}`.padEnd(11), `nonbg=${(r.nonbgRatio * 100).toFixed(2)}% colors=${r.distinctColors} ${(statSync(file).size / 1024).toFixed(1)}KB`)
  return r
}

await page.goto(URL, { waitUntil: 'networkidle0' })
await page.evaluate((nextTheme) => localStorage.setItem('peasant-theme', nextTheme), theme)
await page.reload({ waitUntil: 'networkidle0' })
await pause(900)

const actualTheme = await page.evaluate(() => document.documentElement.getAttribute('data-theme'))
if (actualTheme !== theme) {
  await fail(`ERROR [grouped-helper-shoot.mjs] the requested theme did not apply.
  What failed: [data-theme]="${actualTheme}" after requesting theme="${theme}".
  Where: grouped-helper-shoot.mjs theme preflight.
  Fix: confirm the root layout uses the shared theme hook and retry.`, 3)
}

if (SURFACE === 'discovery') {
  const section = await waitFor('[data-testid="grouped-helper-section"]')
  const groupSel = '.helper-group[data-group-id="hg_demo_discovery"]'
  const control = await waitFor(`${groupSel} button.helper-group-trigger`)
  if (!section || !control) {
    await fail(`ERROR [grouped-helper-shoot.mjs] the discovery helper-only section never mounted at ${URL}.
  What failed: no [data-testid="grouped-helper-section"] / "${groupSel}" appeared.
  Why: the served build predates the grouped section, or the mock answered no grouped items.
  Where: grouped-helper-shoot.mjs discovery-arm readiness wait.
  Fix: restart the mock with MOCK_GROUPED_SURFACES=1, rebuild from this worktree, and retry.`, 2)
  }
  const closed = await page.evaluate((sel) => {
    const scope = document.querySelector('[data-testid="grouped-helper-section"]')
    return {
      groupItems: scope?.querySelectorAll('.helper-group-item').length ?? 0,
      controls: scope?.querySelectorAll('[data-testid="helper-group-label"]').length ?? 0,
      contextNotices: (scope?.textContent ?? '').split('owner is unavailable').length - 1,
      label: document.querySelector(`${sel} [data-testid="helper-group-label"]`)?.textContent?.trim() ?? '',
      memberLinks: scope?.querySelectorAll('a.helper-thread-open').length ?? 0,
    }
  }, groupSel)
  if (
    closed.groupItems !== 1 ||
    closed.controls !== 1 ||
    closed.contextNotices !== 1 ||
    closed.memberLinks !== 0 ||
    !closed.label.includes('1 helper thread')
  ) {
    await fail(`ERROR [grouped-helper-shoot.mjs] the discovery helper-only result does not render exactly once.
  What failed: ${JSON.stringify(closed)}.
  Why: one scoped result produced more than one owner-unavailable group, or the control's count is wrong.
  Where: grouped-helper-shoot.mjs discovery-arm provenance check.
  Means: the capture would evidence the duplicated disclosure this change removes.
  Fix: rebuild and restart the server from this worktree, then retry.`, 2)
  }
  await control.click()
  const memberLink = await waitFor(`${groupSel} a.helper-thread-open`)
  if (!memberLink) {
    await fail(`ERROR [grouped-helper-shoot.mjs] expanding the discovery group loaded no individually linked member.
  What failed: no a.helper-thread-open appeared after the disclosure opened.
  Where: grouped-helper-shoot.mjs discovery-arm expansion check.
  Fix: confirm the mock answers /transcript-groups/{id}/members, then retry.`, 2)
  }
  const openedLinks = await page.evaluate((sel) =>
    [...document.querySelectorAll(`${sel} a.helper-thread-open`)].map((a) => a.getAttribute('href')), groupSel)
  if (openedLinks.length !== 1 || !/^\/transcripts\/[0-9a-f-]+$/.test(openedLinks[0] ?? '')) {
    await fail(`ERROR [grouped-helper-shoot.mjs] the expanded member is not one explicit transcript link.
  What failed: ${JSON.stringify(openedLinks)}.
  Where: grouped-helper-shoot.mjs discovery-arm member-link check.
  Fix: confirm the host links each member with its own transcript id, then retry.`, 2)
  }
  const style = await assertComputed(
    `${groupSel} .helper-group-count`,
    { fontFamily: isMono, borderRadius: isSquare },
    "the discovery helper group's count",
  )
  await capture('village-discovery-grouped-helpers', '[data-testid="grouped-helper-section"]', 'discovery')
  console.log('discovery provenance:', JSON.stringify({ closed, openedLinks }))
  console.log('computed helper-group count style:', JSON.stringify(style))
} else if (SURFACE === 'profile' || SURFACE === 'project') {
  const laterSel = '.helper-group[data-group-id="hg_demo_later"]'
  const continuation = await waitFor('[data-testid="grouped-helper-continuation"]')
  if (!continuation) {
    await fail(`ERROR [grouped-helper-shoot.mjs] the ${SURFACE} grouped continuation never mounted at ${URL}.
  What failed: no [data-testid="grouped-helper-continuation"] appeared.
  Why: the served build still reads a single grouped page, or the mock reported no later page.
  Where: grouped-helper-shoot.mjs ${SURFACE}-arm readiness wait.
  Fix: restart the mock with MOCK_GROUPED_SURFACES=1, rebuild from this worktree, and retry.`, 2)
  }
  const before = await page.evaluate((sel) => ({
    text: document.querySelector('[data-testid="grouped-helper-continuation"]')?.textContent?.trim() ?? '',
    button: [...document.querySelectorAll('[data-testid="grouped-helper-continuation"] button')].map((b) => (b.textContent ?? '').trim()),
    laterGroups: document.querySelectorAll(sel).length,
  }), laterSel)
  if (!before.text.includes('1 more grouped result') || !before.button.includes('load more') || before.laterGroups !== 0) {
    await fail(`ERROR [grouped-helper-shoot.mjs] the ${SURFACE} continuation is not the reachable next-page control.
  What failed: ${JSON.stringify(before)}.
  Why: the remaining count, the control, or the not-yet-loaded later page is wrong.
  Where: grouped-helper-shoot.mjs ${SURFACE}-arm provenance check.
  Means: the capture would not evidence the continuation this change adds.
  Fix: rebuild and restart the server from this worktree, then retry.`, 2)
  }
  await page.click('[data-testid="grouped-helper-continuation"] button')
  const laterControl = await waitFor(`${laterSel} button.helper-group-trigger`)
  if (!laterControl) {
    await fail(`ERROR [grouped-helper-shoot.mjs] the ${SURFACE} continuation reached no later grouped container.
  What failed: no "${laterSel}" appeared after pressing load more.
  Where: grouped-helper-shoot.mjs ${SURFACE}-arm next-page check.
  Fix: confirm the mock serves a second grouped page, then retry.`, 2)
  }
  await laterControl.click()
  const memberLink = await waitFor(`${laterSel} a.helper-thread-open`)
  if (!memberLink) {
    await fail(`ERROR [grouped-helper-shoot.mjs] the later ${SURFACE} group loaded no individually linked member.
  What failed: no member link appeared after the later disclosure opened.
  Where: grouped-helper-shoot.mjs ${SURFACE}-arm member check.
  Fix: confirm the mock answers the later scope's member request, then retry.`, 2)
  }
  const after = await page.evaluate((sel) => ({
    continuation: document.querySelector('[data-testid="grouped-helper-continuation"]') != null,
    laterGroups: document.querySelectorAll(sel).length,
    memberLinks: [...document.querySelectorAll(`${sel} a.helper-thread-open`)].map((a) => a.getAttribute('href')),
  }), laterSel)
  if (after.continuation || after.laterGroups !== 1 || after.memberLinks.length !== 1) {
    await fail(`ERROR [grouped-helper-shoot.mjs] the ${SURFACE} later group is not the single reachable disclosure.
  What failed: ${JSON.stringify(after)}.
  Where: grouped-helper-shoot.mjs ${SURFACE}-arm post-continuation check.
  Fix: rebuild and restart the server from this worktree, then retry.`, 2)
  }
  const style = await assertComputed(
    `${laterSel} .helper-group-count`,
    { fontFamily: isMono, borderRadius: isSquare },
    `the later ${SURFACE} helper group's count`,
  )
  await capture(`village-${SURFACE}-grouped-continuation`, '.helper-group-item', SURFACE)
  console.log(`${SURFACE} provenance:`, JSON.stringify({ before, after }))
  console.log('computed helper-group count style:', JSON.stringify(style))
} else if (SURFACE === 'member-failure') {
  const ownerSel = '.helper-group[data-group-id="hg_demo_owner"]'
  const panel = await waitFor('[data-testid="home-page"]')
  const control = await waitFor(`${ownerSel} button.helper-group-trigger`)
  if (!panel || !control) {
    await fail(`ERROR [grouped-helper-shoot.mjs] the home grouped helper disclosure never mounted at ${URL}.
  What failed: no [data-testid="home-page"] / "${ownerSel}" appeared.
  Where: grouped-helper-shoot.mjs member-failure-arm readiness wait.
  Fix: restart the mock with MOCK_HELPER_GROUPS=1 MOCK_MEMBER_STATUS=403, rebuild from this worktree, and retry.`, 2)
  }
  await control.click()
  const notice = await waitFor('[data-testid="helper-group-load-failed"]', 10000)
  if (!notice) {
    await fail(`ERROR [grouped-helper-shoot.mjs] a refused member request produced no failure state.
  What failed: no [data-testid="helper-group-load-failed"] appeared after expanding the group.
  Why: the host rendered the primitive's empty-result copy for a request that failed.
  Where: grouped-helper-shoot.mjs member-failure-arm provenance check.
  Means: the capture would evidence the silent failure this change removes.
  Fix: restart the mock with MOCK_MEMBER_STATUS=403, rebuild from this worktree, and retry.`, 2)
  }
  const shape = await page.evaluate((sel) => {
    const notice = document.querySelector('[data-testid="helper-group-load-failed"]')
    return {
      noticeText: (notice?.textContent ?? '').replace(/\s+/g, ' ').trim(),
      retry: [...(notice?.querySelectorAll('button') ?? [])].map((b) => (b.textContent ?? '').trim()),
      claimsEmptyResult: document.body.textContent.includes('no saved helpers match'),
      memberLinks: document.querySelectorAll(`${sel} a.helper-thread-open`).length,
    }
  }, ownerSel)
  if (
    !shape.noticeText.includes('could not be loaded') ||
    !shape.retry.includes('retry') ||
    shape.claimsEmptyResult ||
    shape.memberLinks !== 0
  ) {
    await fail(`ERROR [grouped-helper-shoot.mjs] the refused member request is not stated honestly.
  What failed: ${JSON.stringify(shape)}.
  Why: the group still claims an empty successful result, offers no retry, or rendered members it never loaded.
  Where: grouped-helper-shoot.mjs member-failure-arm provenance check.
  Fix: rebuild and restart the server from this worktree, then retry.`, 2)
  }
  const style = await assertComputed(
    '[data-testid="helper-group-load-failed"]',
    { borderRadius: isSquare },
    'the member-load failure notice',
  )
  await capture('village-home-member-load-failed', '[data-testid="home-recent-sessions"]', 'member-failure')
  console.log('member-failure provenance:', JSON.stringify(shape))
  console.log('computed failure-notice style:', JSON.stringify(style))
} else if (SURFACE === 'collective-browse-empty-flat' || SURFACE === 'collective-contribute-empty-flat') {
  const ownerSel = '.helper-group[data-group-id="hg_capture_owner"]'
  const contextSel = '.helper-group[data-group-id="hg_capture_context"]'
  const fallback = await waitFor('[data-testid="grouped-helper-fallback"]')
  if (!fallback) {
    await fail(`ERROR [grouped-helper-shoot.mjs] the ${SURFACE} grouped fallback never mounted at ${URL}.
  What failed: no [data-testid="grouped-helper-fallback"] appeared.
  Why: the served build still gates the grouped content on a non-empty flat list, or the mock answered no grouped items.
  Where: grouped-helper-shoot.mjs ${SURFACE}-arm readiness wait.
  Fix: restart the mock with MOCK_COLLECTIVE_EMPTY_FLAT=1, rebuild from this worktree, and retry.`, 2)
  }
  // The flat empty state must NOT replace the grouped content the server sent.
  const emptyPanel = SURFACE === 'collective-contribute-empty-flat'
    ? await page.$('[data-testid="contribute-member-panel"]')
    : null
  const state = await page.evaluate(
    (ownerSelector, contextSelector) => ({
      ownerGroups: document.querySelectorAll(ownerSelector).length,
      contextGroups: document.querySelectorAll(contextSelector).length,
      inFallback: document.querySelector('[data-testid="grouped-helper-fallback"]')
        ?.querySelectorAll('.helper-group').length ?? -1,
      emptyText: (document.body.textContent ?? '').includes('No transcripts shared yet.'),
    }),
    ownerSel,
    contextSel,
  )
  if (
    state.ownerGroups !== 1 ||
    state.contextGroups !== 1 ||
    state.inFallback !== 2 ||
    state.emptyText ||
    emptyPanel != null
  ) {
    await fail(`ERROR [grouped-helper-shoot.mjs] the ${SURFACE} empty flat result did not defer to the grouped content.
  What failed: ${JSON.stringify({ state, emptyPanel: emptyPanel != null })}.
  Why: the grouped owner or helper-only context is missing, or the flat empty state replaced it.
  Where: grouped-helper-shoot.mjs ${SURFACE}-arm provenance check.
  Means: the capture would evidence the empty-state gate this change removes.
  Fix: rebuild and restart the server from this worktree, restart the mock with MOCK_COLLECTIVE_EMPTY_FLAT=1, and retry.`, 2)
  }
  const ownerControl = await waitFor(`${ownerSel} button.helper-group-trigger`)
  await ownerControl.click()
  await waitFor(`${ownerSel} a.helper-thread-open`)
  const memberLinks = await page.$$eval(`${ownerSel} a.helper-thread-open`, (links) =>
    links.map((a) => a.getAttribute('href')),
  )
  if (memberLinks.length !== 2 || memberLinks.some((href) => !/^\/transcripts\/[0-9a-f-]+$/.test(href ?? ''))) {
    await fail(`ERROR [grouped-helper-shoot.mjs] the ${SURFACE} fallback owner row is not explicit per-member links.
  What failed: ${JSON.stringify(memberLinks)}.
  Where: grouped-helper-shoot.mjs ${SURFACE}-arm member-link check.
  Fix: confirm the host links each member with its own transcript id, then retry.`, 2)
  }
  if (SURFACE === 'collective-contribute-empty-flat') {
    const box = await page.$(`${ownerSel} input[aria-label^="select "]`)
    if (!box) {
      await fail(`ERROR [grouped-helper-shoot.mjs] the ${SURFACE} fallback disclosure offers no per-member selection.`, 2)
    }
    await box.click()
    const armed = await page.evaluate(() => (document.body.textContent ?? '').replace(/\s+/g, ' '))
    if (!armed.includes('contribute 1 transcript')) {
      await fail(`ERROR [grouped-helper-shoot.mjs] ticking one fallback member did not arm the contribute action.
  What failed: the page does not state "contribute 1 transcript" after one member was ticked.
  Where: grouped-helper-shoot.mjs ${SURFACE}-arm selection check.
  Fix: confirm the page's selection count includes helper members, then retry.`, 2)
    }
  }
  const style = await assertComputed(
    `${ownerSel} .helper-group-count`,
    { fontFamily: isMono, borderRadius: isSquare },
    `the ${SURFACE} helper group count`,
  )
  await capture(`village-${SURFACE}`, '[data-testid="grouped-helper-fallback"]', SURFACE)
  console.log(`${SURFACE} provenance:`, JSON.stringify({ state, memberLinks }))
  console.log('computed helper-group count style:', JSON.stringify(style))
} else if (SURFACE === 'collective-contribute-later') {
  const ownerSel = '.helper-group[data-group-id="hg_capture_owner"]'
  const continuation = await waitFor('[data-testid="grouped-helper-continuation"]')
  if (!continuation) {
    await fail(`ERROR [grouped-helper-shoot.mjs] the ${SURFACE} grouped continuation never mounted at ${URL}.
  What failed: no [data-testid="grouped-helper-continuation"] appeared.
  Why: the served build still reads a single grouped page, or the mock reported no later page.
  Where: grouped-helper-shoot.mjs ${SURFACE}-arm readiness wait.
  Fix: restart the mock with MOCK_COLLECTIVE_LATER_PAGE=1, rebuild from this worktree, and retry.`, 2)
  }
  const before = await page.evaluate(
    (sel) => ({
      text: document.querySelector('[data-testid="grouped-helper-continuation"]')?.textContent?.trim() ?? '',
      laterGroups: document.querySelectorAll(sel).length,
    }),
    ownerSel,
  )
  if (!before.text.includes('1 more grouped result') || before.laterGroups !== 0) {
    await fail(`ERROR [grouped-helper-shoot.mjs] the ${SURFACE} continuation is not the reachable next-page control.
  What failed: ${JSON.stringify(before)}.
  Where: grouped-helper-shoot.mjs ${SURFACE}-arm provenance check.
  Means: the capture would not evidence the continuation this change adds.
  Fix: confirm the mock serves a second grouped page, then retry.`, 2)
  }
  await page.click('[data-testid="grouped-helper-continuation"] button')
  const ownerControl = await waitFor(`${ownerSel} button.helper-group-trigger`)
  if (!ownerControl) {
    await fail(`ERROR [grouped-helper-shoot.mjs] the ${SURFACE} continuation reached no later grouped owner.
  What failed: no "${ownerSel}" appeared after pressing load more.
  Where: grouped-helper-shoot.mjs ${SURFACE}-arm next-page check.
  Fix: confirm the mock serves a second grouped page, then retry.`, 2)
  }
  const after = await page.evaluate(
    (sel) => ({
      continuation: document.querySelector('[data-testid="grouped-helper-continuation"]') != null,
      inFallback: document.querySelector('[data-testid="grouped-helper-fallback"]')
        ?.querySelectorAll(sel).length ?? -1,
    }),
    ownerSel,
  )
  if (after.continuation || after.inFallback !== 1) {
    await fail(`ERROR [grouped-helper-shoot.mjs] the ${SURFACE} later group is not the single reachable disclosure.
  What failed: ${JSON.stringify(after)}.
  Where: grouped-helper-shoot.mjs ${SURFACE}-arm post-continuation check.
  Fix: rebuild and restart the server from this worktree, then retry.`, 2)
  }
  await ownerControl.click()
  await waitFor(`${ownerSel} a.helper-thread-open`)
  const laterLinks = await page.$$eval(`${ownerSel} a.helper-thread-open`, (links) =>
    links.map((a) => a.getAttribute('href')),
  )
  if (laterLinks.length !== 2) {
    await fail(`ERROR [grouped-helper-shoot.mjs] expanding the later ${SURFACE} group loaded no individually linked members.
  What failed: ${JSON.stringify(laterLinks)}.
  Where: grouped-helper-shoot.mjs ${SURFACE}-arm member check.
  Fix: confirm the mock answers the later scope's member request, then retry.`, 2)
  }
  const style = await assertComputed(
    `${ownerSel} .helper-group-count`,
    { fontFamily: isMono, borderRadius: isSquare },
    `the later ${SURFACE} helper group count`,
  )
  await capture(`village-${SURFACE}`, '[data-testid="grouped-helper-fallback"]', SURFACE)
  console.log(`${SURFACE} provenance:`, JSON.stringify({ before, after, laterLinks }))
  console.log('computed helper-group count style:', JSON.stringify(style))
} else {
  const groupSel = `.helper-group[data-group-id="${COLLECTIVE_GROUP}"]`
  const panelSel =
    SURFACE === 'collective-contribute'
      ? '[data-testid="contribute-member-panel"]'
      : SURFACE === 'collective-review'
        ? '[data-testid="review-panel"]'
        : null
  const readinessSel =
    SURFACE === 'collective-browse'
      ? '[data-testid="owner-helper-groups"]'
      : panelSel
  const panel = await waitFor(readinessSel)
  if (!panel) {
    await fail(`ERROR [grouped-helper-shoot.mjs] the ${SURFACE} surface never mounted at ${URL}.
  What failed: no ${readinessSel} appeared.
  Why: the served build predates the grouped collective mount, or the mock answered no grouped items.
  Where: grouped-helper-shoot.mjs ${SURFACE}-arm readiness wait.
  Fix: restart the mock (mock-rest-grouped-collective.mjs), rebuild from this worktree, and retry.`, 2)
  }

  // BEFORE expanding: exactly ONE group, under the owner row the server
  // grouped it with, and no member request yet. A page that doubled the group,
  // moved it, or fetched members eagerly fails here.
  const closed = await page.evaluate((rowSel, sel) => {
    const slot = document.querySelector(rowSel)
    return {
      rowSlots: document.querySelectorAll(rowSel).length,
      groups: document.querySelectorAll(sel).length,
      inRow: slot == null ? -1 : slot.querySelectorAll(sel).length,
      memberRows: [...document.querySelectorAll(`${sel} .helper-thread-row`)].length,
      memberRowsInRow: slot == null ? -1 : slot.querySelectorAll(`${sel} .helper-thread-row`).length,
    }
  }, SURFACE === 'collective-browse'
    ? '[data-testid="owner-helper-groups"]'
    : `[data-helper-group-owner="${COLLECTIVE_ROW}"]`, groupSel)
  if (closed.inRow !== 1 || closed.groups !== 1 || closed.memberRows !== 0 || closed.memberRowsInRow !== 0) {
    await fail(`ERROR [grouped-helper-shoot.mjs] the ${SURFACE} group is not the single undisclosed mount.
  What failed: ${JSON.stringify(closed)}.
  Why: the group rendered more than once, rendered outside its owner row, or loaded members before expansion.
  Where: grouped-helper-shoot.mjs ${SURFACE}-arm provenance check.
  Fix: rebuild and restart the server from this worktree, then retry.`, 2)
  }

  const control = await waitFor(`${groupSel} button.helper-group-trigger`)
  if (!control) {
    await fail(`ERROR [grouped-helper-shoot.mjs] the ${SURFACE} group carries no disclosure control.`, 2)
  }
  await control.click()
  const memberLink = await waitFor(`${groupSel} a.helper-thread-open`)
  const box = await page.$(`${groupSel} input[aria-label^="select "]`)
  // A browse surface is read-only: it draws the individually linked members but
  // offers no per-member selection.
  const selectionExpected = SURFACE !== 'collective-browse'
  if (!memberLink || (selectionExpected && !box)) {
    await fail(`ERROR [grouped-helper-shoot.mjs] expanding the ${SURFACE} group loaded no ${selectionExpected ? 'individually selectable' : 'linked'} member.
  What failed: link=${memberLink != null} checkbox=${box != null} after the disclosure opened.
  Where: grouped-helper-shoot.mjs ${SURFACE}-arm expansion check.
  Fix: confirm the mock answers /transcript-groups/{id}/members with the right route arm, then retry.`, 2)
  }
  if (!selectionExpected) {
    const strayBox = await page.$(`${groupSel} input[type="checkbox"]`)
    if (strayBox) {
      await fail(`ERROR [grouped-helper-shoot.mjs] the ${SURFACE} group offers a selection control on a read-only surface.`, 2)
    }
  }

  const openedLinks = await page.evaluate((sel) =>
    [...document.querySelectorAll(`${sel} a.helper-thread-open`)].map((a) => a.getAttribute('href')), groupSel)
  if (openedLinks.length !== 2 || openedLinks.some((href) => !/^\/transcripts\/[0-9a-f-]+$/.test(href ?? ''))) {
    await fail(`ERROR [grouped-helper-shoot.mjs] the expanded ${SURFACE} group is not explicit per-member links.
  What failed: ${JSON.stringify(openedLinks)}.
  Where: grouped-helper-shoot.mjs ${SURFACE}-arm member-link check.
  Fix: confirm the host links each member with its own transcript id, then retry.`, 2)
  }

  // The BROWSE list is read-only: no selection control belongs on it.
  if (SURFACE === 'collective-browse') {
    const style = await assertComputed(
      `${groupSel} .helper-group-count`,
      { fontFamily: isMono, borderRadius: isSquare },
      'the collective browse helper group count',
    )
    await capture('village-collective-grouped-browse', null, 'collective-browse')
    console.log('collective-browse provenance:', JSON.stringify({ closed, openedLinks }))
    console.log('computed helper-group count style:', JSON.stringify(style))
  } else {
    // Selecting ONE member must arm the route's own action bar with exactly
    // one transcript; the server never receives a group id.
    await box.click()
    const armed = await page.evaluate((linkCount) => ({
      memberLinks: document.querySelectorAll('a.helper-thread-open').length,
      sameCount: linkCount === document.querySelectorAll('a.helper-thread-open').length,
      action: [...document.querySelectorAll('button')]
        .map((b) => (b.textContent ?? '').replace(/\s+/g, ' ').trim())
        .filter((label) => /transcript|selected/i.test(label)),
    }), openedLinks.length)
    const armedText = await page.evaluate(() =>
      (document.body.textContent ?? '').replace(/\s+/g, ' '),
    )
    const expectedWord = SURFACE === 'collective-contribute' ? 'contribute 1 transcript' : '1 selected'
    if (!armedText.includes(expectedWord)) {
      await fail(`ERROR [grouped-helper-shoot.mjs] selecting one member did not arm the ${SURFACE} action.
  What failed: the page does not state "${expectedWord}" after one member was ticked; action labels=${JSON.stringify(armed.action)}.
  Where: grouped-helper-shoot.mjs ${SURFACE}-arm selection check.
  Means: the capture would not evidence an explicit per-helper selection.
  Fix: confirm the page's selection count includes helper members, then retry.`, 2)
    }
    const style = await assertComputed(
      `${groupSel} .helper-group-count`,
      { fontFamily: isMono, borderRadius: isSquare },
      `the ${SURFACE} helper group count`,
    )
    await capture(`village-${SURFACE}-grouped-helper`, panelSel, SURFACE)
    console.log(`${SURFACE} provenance:`, JSON.stringify({ closed, openedLinks, armed }))
    console.log('computed helper-group count style:', JSON.stringify(style))
  }
}

console.log('console errors:', errs.length ? errs.slice(0, 6) : 'none')
await browser.close()
