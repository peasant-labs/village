/* Journey: the pull request prompts page, and the list of pull requests
 * waiting on you.
 *
 * The prompts page is the one the pull request's comment and check link to. It
 * is driven three ways through the real route: the author reading a preview
 * (what matches, the two actions, and who can read the transcripts), a
 * reviewer reading it once attached, and that reviewer moving through the
 * split list with `j`. The list at `/pulls` is read with requests waiting and
 * with none. Each asserts the semantic surface and the design-system contract,
 * records the accessible tree and an axe report, and keeps a still for review.
 */
import { writeFileSync } from 'node:fs'
import { test, expect } from './lib/fixtures.mjs'
import { setScenario } from './lib/scenario.mjs'
import {
  scanAxe,
  seriousViolations,
  expectTheme,
  expectComputedTokens,
} from './lib/assertions.mjs'
import { JOURNEY_PULL, JOURNEY_PULL_PATH } from './lib/pull-request-fixtures.mjs'
import { PROMPT_REQUESTS } from './lib/home-fixtures.mjs'

const [FIX, GUARD] = JOURNEY_PULL.transcripts

/** Record the page's accessible tree and axe report; fail on a serious violation. */
async function recordEvidence(page, testInfo, name, testId = 'pull-request-page') {
  const aria = await page.getByTestId(testId).ariaSnapshot()
  writeFileSync(testInfo.outputPath(`${name}-aria.yml`), aria)
  await testInfo.attach(`${name}-aria.yml`, {
    path: testInfo.outputPath(`${name}-aria.yml`),
    contentType: 'text/yaml',
  })

  const axe = await scanAxe(page)
  writeFileSync(testInfo.outputPath('axe.json'), JSON.stringify(axe, null, 2))
  await testInfo.attach('axe.json', {
    path: testInfo.outputPath('axe.json'),
    contentType: 'application/json',
  })
  const blocking = seriousViolations(axe)
  expect(blocking, JSON.stringify(blocking, null, 2)).toEqual([])
}

async function still(page, testInfo, name) {
  // Assertions may scroll an option into view. Review evidence includes the
  // mounted header and page heading as well as the selected transcript.
  await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }))
  await expect(page.locator('header').first()).toBeInViewport()
  await expect(page.getByRole('heading', { level: 1, name: JOURNEY_PULL.title })).toBeInViewport()
  await page.screenshot({ path: testInfo.outputPath(`${name}.png`), fullPage: true })
  await testInfo.attach(`${name}.png`, {
    path: testInfo.outputPath(`${name}.png`),
    contentType: 'image/png',
  })
}

/** The split list's selected option, by its visible name. */
const selectedOption = (page) => page.locator('[role="option"][aria-selected="true"] .pd-split-option-label')

test.describe('pull request page', () => {
  test('the author sees what matches and who can read it', async ({ page, theme }, testInfo) => {
    await page.goto(JOURNEY_PULL_PATH)
    await expect(page.getByTestId('pull-request-page')).toBeVisible()
    await expectTheme(page, theme)

    await expect(page.getByRole('heading', { level: 1, name: JOURNEY_PULL.title })).toBeVisible()
    await expect(page.getByTestId('attachment-state-line')).toHaveText(
      '2 of your transcripts match this pull request. attach them so reviewers can read them next to the code.',
    )
    await expect(page.getByTestId('pull-request-state')).toHaveText('preview')
    await expect(page.getByRole('button', { name: 'attach 2 transcripts' })).toBeVisible()
    // Declining a preview detaches it, and the control says so.
    await expect(page.getByRole('button', { name: "don't attach" })).toBeVisible()
    // The card lists the two matching transcripts by title, each with what the
    // author's own reads say about its session.
    const rows = page.getByTestId('pull-request-transcript')
    await expect(rows).toHaveCount(2)
    await expect(rows.nth(0).locator('.is-title')).toHaveText(FIX.title)
    await expect(rows.nth(1).locator('.is-title')).toHaveText(GUARD.title)
    await expect(rows.nth(0).locator('.on-pr-rowmeta')).toContainText('@alice-dev, claude-code, 40 turns, 2 commits')
    // Who can read them comes from each transcript's own reads, once both answer.
    await expect(page.getByTestId('pull-request-actions')).toContainText(
      'who can read them: members of Acme Platform and Acme Company. attaching does not change that.',
    )
    await expect(page.getByTestId('pull-request-coverage')).toHaveText('3 of 4 commits traced')

    // The canonical in-use callout puts consent actions after its explanation.
    const audienceBox = await page.getByTestId('transcript-audience').boundingBox()
    const attachBox = await page.getByTestId('confirm-attachment').boundingBox()
    expect(audienceBox).not.toBeNull()
    expect(attachBox).not.toBeNull()
    expect(attachBox.y).toBeGreaterThan(audienceBox.y + audienceBox.height)

    // The split list names each transcript by its title, the first selected.
    const options = page.getByRole('option')
    await expect(options).toHaveCount(2)
    await expect(selectedOption(page)).toHaveText(FIX.title)
    await expect(page.getByText('the ingest test flakes on CI about 1 in 5 runs')).toBeVisible()

    await expectComputedTokens(page, '[data-testid="pull-request-page"]', {
      fontFamilyIncludes: 'atkinson',
      minFontSize: 16,
    })
    await expectComputedTokens(page, '[data-testid="pull-request-actions"]', { borderRadius: '0px' })
    await expectComputedTokens(page, '.pd', { borderRadius: '0px' })

    await recordEvidence(page, testInfo, 'pull-request-author')
    await still(page, testInfo, 'pr-preview-author')
  })

  test.describe('as a reviewer', () => {
    test.beforeEach(async ({ request }) => {
      await setScenario(request, 'pull-request-reader')
    })
    test.afterEach(async ({ request }) => {
      await setScenario(request, 'default')
    })

    test('reads the attached transcripts beside the code', async ({ page, theme }, testInfo) => {
      await page.goto(JOURNEY_PULL_PATH)
      await expect(page.getByTestId('pull-request-page')).toBeVisible()
      await expectTheme(page, theme)

      // A reviewer is shown no author controls and no audience statement.
      await expect(page.getByTestId('pull-request-actions')).toHaveCount(0)
      await expect(page.getByTestId('pull-request-state')).toHaveText('attached')
      await expect(page.getByTestId('pull-request-transcript')).toHaveCount(2)
      await expect(page.getByTestId('pull-request-coverage')).toHaveText('3 of 4 commits traced')
      await expect(page.getByRole('option')).toHaveCount(2)
      await expect(selectedOption(page)).toHaveText(FIX.title)
      // The transcript opens carrying this pull request as where it was opened.
      await expect(page.getByRole('link', { name: 'open the transcript' })).toHaveAttribute(
        'href',
        `/transcripts/${FIX.id}?in=pull:${JOURNEY_PULL.owner}/${JOURNEY_PULL.name}/${JOURNEY_PULL.number}`,
      )

      await recordEvidence(page, testInfo, 'pull-request-reader')
      await still(page, testInfo, 'pr-attached-reader')
    })

    test('moves through the split list with j and k', async ({ page, theme }, testInfo) => {
      await page.goto(JOURNEY_PULL_PATH)
      await expect(page.getByRole('option')).toHaveCount(2)
      await expectTheme(page, theme)
      await expect(selectedOption(page)).toHaveText(FIX.title)

      await page.keyboard.press('j')
      await expect(selectedOption(page)).toHaveText(GUARD.title)
      await expect(page.locator('.pd-split-pane-title')).toHaveText(GUARD.title)
      await expect(page.getByText('an empty turn breaks the digest builder')).toBeVisible()

      await still(page, testInfo, 'pr-split-selected')

      await page.keyboard.press('k')
      await expect(selectedOption(page)).toHaveText(FIX.title)

      // A row's prompts control selects its session and hands the keys to the list.
      await page.getByTestId('pull-request-jump').nth(1).click()
      await expect(selectedOption(page)).toHaveText(GUARD.title)
      await expect(page.locator('.pd-split-list')).toBeFocused()
    })
  })
})

test.describe('pull requests waiting on you', () => {
  test.afterEach(async ({ request }) => {
    await setScenario(request, 'default')
  })

  test('lists them newest first, by the day they were asked for', async ({ page, theme }, testInfo) => {
    await page.goto('/pulls')
    await expect(page.getByTestId('pulls-page')).toBeVisible()
    await expectTheme(page, theme)
    await expect(page.getByRole('heading', { level: 1, name: 'pull requests' })).toBeVisible()

    const cards = page.getByTestId('pulls-request')
    await expect(cards).toHaveCount(PROMPT_REQUESTS.length)
    await expect(page.getByTestId('pulls-day').first().locator('h2')).toHaveText('today')
    // The newest request leads, and its heading opens its prompts page.
    const newest = PROMPT_REQUESTS[PROMPT_REQUESTS.length - 1]
    const first = cards.first().getByRole('link', { name: `${newest.owner}/${newest.name} #${newest.number}` })
    await expect(first).toHaveAttribute('href', `/pulls/${newest.owner}/${newest.name}/${newest.number}`)
    await expect(cards.first()).toContainText(`from ${newest.head_remote}`)
    await expect(cards.first().locator('.on-tag')).toHaveText('waiting')

    await expectComputedTokens(page, '[data-testid="pulls-request"]', { borderRadius: '0px' })
    await recordEvidence(page, testInfo, 'pulls-list', 'pulls-page')
    await page.screenshot({ path: testInfo.outputPath('pulls-list.png'), fullPage: true })
  })

  test('says nothing is waiting, and how a pull request comes to wait', async ({ page, request, theme }) => {
    await setScenario(request, 'empty')
    await page.goto('/pulls')
    await expectTheme(page, theme)
    const empty = page.getByTestId('pulls-empty')
    await expect(empty.getByRole('heading', { name: 'nothing is waiting on you' })).toBeVisible()
    // How a pull request comes to wait lives in the docs; the app links there
    // instead of restating the command.
    const docs = empty.getByRole('link', { name: /how pull requests get transcripts/ })
    await expect(docs).toHaveAttribute('href', 'https://peasantlabs.org/docs/guides/reviews')
    // It leaves the app: a new tab on the docs, with no window handle back.
    await expect(docs).toHaveAttribute('target', '_blank')
    await expect(docs).toHaveAttribute('rel', /noopener/)
    await expect(empty).not.toContainText('/peasant attach')
    await expect(page.getByTestId('pulls-request')).toHaveCount(0)
  })

  test('asks a signed-out visitor to sign in and come back', async ({ page, request }) => {
    await setScenario(request, 'signed-out')
    try {
      // The app keeps no signed-out view: the frame hands the visitor to the
      // marketing site's log-in popup, carrying this list as where to come back.
      await page.route('https://peasantlabs.org/**', (route) =>
        route.fulfill({ status: 200, contentType: 'text/html', body: '<title>marketing</title>' }),
      )
      await page.goto('/pulls')
      await expect(page).toHaveURL(`https://peasantlabs.org/?login&next=${encodeURIComponent('/pulls')}`)
      const destination = new URL(page.url())
      expect(destination.protocol).toBe('https:')
      expect(destination.host).toBe('peasantlabs.org')
      expect(destination.pathname).toBe('/')
      expect(destination.searchParams.has('login')).toBe(true)
      expect(destination.searchParams.get('next')).toBe('/pulls')
      // Neither the list nor its signed-out notice mounts: the visitor leaves
      // for the popup before the app draws a page of its own.
      await expect(page.getByTestId('pulls-page')).toHaveCount(0)
      await expect(page.getByTestId('pulls-signed-out')).toHaveCount(0)
    } finally {
      await setScenario(request, 'default')
    }
  })
})
