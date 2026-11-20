/* Journey: the transcript page over the positioned reader.
 *
 * The real Next route and the real browser request log, against the composed mock's positioned
 * scenarios (lib/positioned-transcript-fixtures.mjs). Every positioned body comes from the one shared
 * builder (lib/positioned-bodies.mjs), so each is one Schema's parsers accept. The window and
 * numbering semantics are proved against the real handler in the backend; these journeys prove the
 * page handles contract-valid answers: which reads it makes, what it never reads, and what it shows.
 */
import { readFileSync } from 'node:fs'
import { test, expect } from './lib/fixtures.mjs'
import { setScenario } from './lib/scenario.mjs'
import { POSITIONED } from './lib/positioned-transcript-fixtures.mjs'
import { JOURNEY_COLLECTIVES } from './lib/collective-fixtures.mjs'
import { OWNED_TRANSCRIPT } from './lib/transcript-fixtures.mjs'
import { TranscriptSegmentPartitionKind } from '@peasant-labs/schema'
import { SessionDetailV2TestId } from '../../src/components/session-detail/v2/readModeTestIds.ts'
import { TranscriptHeaderActionsLabel, TranscriptHeaderActionsTestId } from '../../src/components/transcript/transcriptHeaderActionsNames.ts'

// The header's actions sit in the header row at a desktop width.
const DESKTOP = { width: 1440, height: 900 }
const MAIN = POSITIONED.main
const PREVIEW = POSITIONED.preview
const page_ = (search = '') => `/transcripts/${MAIN.transcriptId}${search}`
const MOCK_PORT = String(process.env.JOURNEY_MOCK_PORT || 8799)
const POSITIONED_TESTID = SessionDetailV2TestId.ModePositioned.wire
const HEADER_ACTIONS = TranscriptHeaderActionsTestId.Root.wire
const MORE = { name: TranscriptHeaderActionsLabel.More.wire, exact: true }
const DOWNLOAD_JSON = { name: TranscriptHeaderActionsLabel.DownloadJSON.wire, exact: true }
const POSITIONED_PATH = /\/api\/v1\/transcripts\/([^/]+)\/(header|window|segments(?:\/locate|\/\d+)?)$/

/** Every API request the page makes, in order, as URL objects. */
function recordRequests(page) {
  const log = []
  page.on('request', (request) => {
    const url = new URL(request.url())
    if (url.port === MOCK_PORT) log.push({ url, referer: request.headers().referer ?? null })
  })
  return log
}
const contentReads = (log) => log.filter(({ url }) => url.pathname.endsWith('/content'))
const positionedReads = (log, id = MAIN.transcriptId) => log.filter(({ url }) => url.pathname.match(POSITIONED_PATH)?.[1] === id)
/** One positioned read as a comparable key: its route and the query that selects what it reads. */
const readKey = ({ url }) => {
  const route = url.pathname.match(POSITIONED_PATH)[2]
  const keep = ['partition', 'section', 'page', 'anchor'].filter((key) => url.searchParams.has(key)).map((key) => `${key}=${url.searchParams.get(key)}`)
  return [route, ...keep].join(' ')
}

const stream = (page) => page.locator('.txn-stream')
const rowText = (position) => {
  const step = Math.floor(position / 2)
  const pool = position % 2 === 0 ? MAIN.prompts : MAIN.replies
  return `${pool[step % pool.length]} [step ${step + 1}]`
}
const segmentOf = (recorded) => Math.floor(recorded / MAIN.rowsPerSegment)

/** Route one positioned read of the main transcript through `answer`, once or every time. */
async function failRead(page, matches, answer, { once = true } = {}) {
  let used = false
  await page.route((url) => POSITIONED_PATH.test(url.pathname), async (route) => {
    const url = new URL(route.request().url())
    if (!(once && used) && url.pathname.includes(MAIN.transcriptId) && matches(url)) {
      used = true
      return route.fulfill({ ...answer, headers: { 'access-control-allow-origin': '*', 'access-control-expose-headers': 'Retry-After', ...(answer.headers ?? {}) } })
    }
    return route.continue()
  })
}

test.describe('positioned transcript reader', () => {
  test.beforeEach(async ({ page, request }) => {
    await page.setViewportSize(DESKTOP)
    await setScenario(request, 'positioned')
  })
  test.afterEach(async ({ request }) => { await setScenario(request, 'default') })

  test('journey-deeplink-request-set', async ({ page }) => {
    const log = recordRequests(page)
    await page.goto(page_(`?turn=${MAIN.farTurn}`))
    await expect(page.getByTestId(POSITIONED_TESTID)).toBeVisible()
    await expect(stream(page).getByText(rowText(MAIN.farTurn))).toBeVisible()
    await expect(stream(page).getByText(rowText(0))).toBeVisible()
    // The request log is exactly the open's reads: the header, the first directory page, the
    // default and the deep link's windows, and the two runs' segments, never one between them.
    const far = segmentOf(MAIN.farTurn)
    expect(new Set(positionedReads(log).map(readKey))).toEqual(new Set([
      'header',
      'segments partition=main page=1',
      'window partition=main',
      `window partition=main anchor=${MAIN.farTurn}`,
      `segments/${far} partition=main`,
      'segments/0 partition=main',
    ]))
    expect(contentReads(log)).toEqual([])
    // Opening the graph is a reader action that reads the whole stored copy, exactly once.
    await page.getByRole('button', { name: POSITIONED.viewButtons.graph, exact: true }).click()
    await expect(page.locator('.react-flow')).toBeVisible()
    await expect.poll(() => contentReads(log).length).toBe(1)
    await page.getByRole('button', { name: POSITIONED.viewButtons.list, exact: true }).click()
    await page.getByRole('button', { name: POSITIONED.viewButtons.graph, exact: true }).click()
    await expect(page.locator('.react-flow')).toBeVisible()
    expect(contentReads(log)).toHaveLength(1)
  })

  test('journey-v2-failure-no-content-request', async ({ page }) => {
    const log = recordRequests(page)
    await failRead(page, (url) => /\/segments\/\d+$/.test(url.pathname), { status: 500, json: { error: 'failed' } }, { once: false })
    await page.goto(page_())
    await expect(page.locator('[data-read-failure="failed"]')).toBeVisible()
    await page.getByRole('button', { name: POSITIONED.failureActions.retry }).click()
    await expect(page.locator('[data-read-failure="failed"]')).toBeVisible()
    // Nothing reads the whole document until the reader asks for a download.
    expect(contentReads(log)).toEqual([])
    const download = page.waitForEvent('download')
    await page.getByTestId(HEADER_ACTIONS).getByRole('button', MORE).click()
    await page.getByRole('menuitem', DOWNLOAD_JSON).click()
    await download
    expect(contentReads(log)).toHaveLength(1)
  })

  test('journey-number-far-target', async ({ page }) => {
    await page.goto(page_(`?turn=${MAIN.farTurn}`))
    const target = stream(page).locator('.txn-turn', { hasText: rowText(MAIN.farTurn) })
    // The served number, not a count of the rows in hand.
    await expect(target).toContainText(`turn ${MAIN.farTurn + 1}`)
    await expect(target).toHaveClass(/txn-active/)
    // The two runs are apart, and the boundary between them names what it skips.
    const gap = stream(page).locator('.txn-segboundary-between')
    await expect(gap).toContainText(`turns ${MAIN.rowsPerSegment + 1}–${segmentOf(MAIN.farTurn) * MAIN.rowsPerSegment}`)
    await expect(stream(page).locator('.txn-turn', { hasText: rowText(MAIN.numberAbsent[0]) })).toContainText('number not served')
  })

  test('journey-number-history-section', async ({ page }) => {
    const log = recordRequests(page)
    await page.goto(page_())
    const toggle = page.locator('.txn-earlier-toggle')
    await expect(toggle).toContainText(`${MAIN.history[0].turns.length} recorded turns`)
    expect(positionedReads(log).filter(({ url }) => url.searchParams.get('partition') === TranscriptSegmentPartitionKind.History)).toEqual([])
    await toggle.click()
    const section = page.locator('.txn-earlier')
    await expect(section.getByText(MAIN.history[0].turns[0].content)).toBeVisible()
    // The section numbers its own rows from its own first turn.
    await expect(section.locator('.txn-turn').first()).toContainText('turn 1')
    expect(positionedReads(log).some(({ url }) => url.searchParams.get('partition') === TranscriptSegmentPartitionKind.History && url.searchParams.get('section') === '0')).toBe(true)
  })

  test('journey-turn-requested-row-filtered', async ({ page }) => {
    await page.goto(page_(`?turn=${MAIN.unservedTurn}`))
    await expect(page.getByTestId(SessionDetailV2TestId.SnappedTurn.wire)).toHaveText(
      `turn ${MAIN.unservedTurn} is not in this transcript's served view, so the viewer opened turn 0.`,
    )
    await expect(stream(page).locator('.txn-turn', { hasText: rowText(0) })).toHaveClass(/txn-active/)
  })

  test('journey-download-after-segment-failure', async ({ page }) => {
    const log = recordRequests(page)
    await failRead(page, (url) => url.pathname.endsWith(`/segments/${segmentOf(MAIN.farTurn)}`), { status: 409, json: { error: 'changed' } }, { once: false })
    await page.goto(page_(`?turn=${MAIN.farTurn}`))
    await expect(page.locator('[data-read-failure="stale"]')).toBeVisible()
    expect(contentReads(log)).toEqual([])
    const download = page.waitForEvent('download')
    await page.getByTestId(HEADER_ACTIONS).getByRole('button', MORE).click()
    await page.getByRole('menuitem', DOWNLOAD_JSON).click()
    const file = JSON.parse(readFileSync(await (await download).path(), 'utf8'))
    // The whole stored copy, though the page holds none of it.
    expect(file.turns).toHaveLength(MAIN.rowsPerSegment * MAIN.segments)
    expect(contentReads(log)).toHaveLength(1)
  })

  test('journey-preview-readonly', async ({ page }) => {
    const log = recordRequests(page)
    await page.goto(`/collectives/${JOURNEY_COLLECTIVES.owner.id}/review`)
    await page.getByRole('button', { name: PREVIEW.title, exact: true }).click()
    const preview = page.locator('.on-contribute-preview')
    await expect(preview.getByTestId(POSITIONED_TESTID)).toBeVisible()
    await expect(preview.locator('.txn-stream').getByText(`${PREVIEW.prompts[0]} [step 1]`)).toBeVisible()
    await expect(preview.getByTestId(HEADER_ACTIONS)).toHaveCount(0)
    await expect(preview.getByRole('button', MORE)).toHaveCount(0)
    expect(positionedReads(log, PREVIEW.transcriptId).length).toBeGreaterThan(0)
    expect(contentReads(log)).toEqual([])
  })

  test('journey-referrer-policy-and-no-id-in-logs', async ({ page }) => {
    const log = recordRequests(page)
    const messages = []
    page.on('console', (message) => messages.push(message.text()))
    const entry = 'positioned-entry-21'
    const response = await page.goto(page_(`?entry=${entry}&entryKind=before_redacted_entry&entryRevision=${'c'.repeat(64)}`))
    expect(response.headers()['referrer-policy']).toBe('no-referrer')
    await expect(stream(page).getByText(rowText(21))).toBeVisible()
    expect(positionedReads(log).some(({ url }) => url.pathname.endsWith('/segments/locate'))).toBe(true)
    // No request names the page it came from, and nothing the page logs names the transcript.
    expect(log.filter(({ referer }) => referer).map(({ url, referer }) => `${url.pathname} <- ${referer}`)).toEqual([])
    expect(messages.filter((text) => text.includes(MAIN.transcriptId) || text.includes(entry))).toEqual([])
  })

  test('browser-revalidation-then-revoked', async ({ page, request }) => {
    // The far run answers busy once; while the reader waits, access is revoked, so its own
    // revalidation is answered 404.
    await failRead(page, (url) => url.pathname.endsWith(`/segments/${segmentOf(MAIN.farTurn)}`), { status: 503, json: { error: 'busy' }, headers: { 'Retry-After': '2' } })
    await page.goto(page_(`?turn=${MAIN.farTurn}`))
    await expect(stream(page).getByText(rowText(0))).toBeVisible()
    await expect(page.locator('[data-read-failure="busy"]')).toBeVisible()
    await setScenario(request, 'positioned-revoked')
    await expect(page.locator('[data-read-failure="denied"]')).toBeVisible({ timeout: 15_000 })
    // The denied state keeps nothing from the earlier read.
    await expect(stream(page).locator('.txn-turn')).toHaveCount(0)
    await expect(page.getByText(rowText(0))).toHaveCount(0)
  })

  test('withheld-fields-not-rendered', async ({ page, request }) => {
    // Served, the page names the project, so its absence below is the projection's doing.
    await page.goto(page_())
    await expect(stream(page).getByText(rowText(0))).toBeVisible()
    await expect(page.locator('body')).toContainText(MAIN.session.project)
    await setScenario(request, 'positioned-withheld')
    await page.goto(page_())
    await expect(stream(page).getByText(rowText(0))).toBeVisible()
    // Nothing the server withheld is rendered, and nothing is derived to replace it.
    for (const value of [MAIN.session.project, MAIN.session.gitBranch, MAIN.owner]) {
      await expect(page.locator('body')).not.toContainText(value)
    }
  })
})

test('journey-capability-absent-content-page', async ({ page }) => {
  const log = recordRequests(page)
  await page.goto(`/transcripts/${OWNED_TRANSCRIPT.id}`)
  await expect(page.getByTestId(SessionDetailV2TestId.ModeContent.wire)).toBeVisible()
  await expect(page.locator('.txn-stream')).toBeVisible()
  expect(log.filter(({ url }) => POSITIONED_PATH.test(url.pathname))).toEqual([])
  expect(contentReads(log).length).toBeGreaterThan(0)
})
