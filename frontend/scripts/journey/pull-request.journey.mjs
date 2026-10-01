/* Journey: the pull request prompts page.
 *
 * The page the pull request's comment and check link to. It is driven three
 * ways through the real route: the author reading a preview (what matches, the
 * two actions, and who can read the transcripts), a reviewer reading it once
 * attached, and that reviewer moving through the split list with `j`. Each
 * asserts the semantic surface and the design-system contract, records the
 * accessible tree and an axe report, and keeps a still for review.
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

const [FIX, GUARD] = JOURNEY_PULL.transcripts

/** Record the page's accessible tree and axe report; fail on a serious violation. */
async function recordEvidence(page, testInfo, name) {
  const aria = await page.getByTestId('pull-request-page').ariaSnapshot()
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
    await expect(page.getByRole('button', { name: 'attach 2 transcripts' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'not now' })).toBeVisible()
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
      await expect(page.getByTestId('pull-request-coverage')).toHaveText('3 of 4 commits traced')
      await expect(page.getByRole('option')).toHaveCount(2)
      await expect(selectedOption(page)).toHaveText(FIX.title)
      await expect(page.getByRole('link', { name: 'open the transcript' })).toHaveAttribute(
        'href',
        `/transcripts/${FIX.id}`,
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
    })
  })
})
