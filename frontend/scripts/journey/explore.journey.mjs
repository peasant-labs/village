/* Journey: village Explore browse.
 *
 * The first fundamental-feature journey: land on /explore, validate the semantic
 * surface and the design-system contract in both themes, run a WCAG scan, then
 * search and open a transcript through the real route. A failing run keeps its
 * trace and video; a passing run still records the ARIA tree and axe report as
 * evidence for agent review.
 */
import { writeFileSync } from 'node:fs'
import { test, expect } from './lib/fixtures.mjs'
import {
  scanAxe,
  seriousViolations,
  expectTheme,
  expectComputedTokens,
} from './lib/assertions.mjs'

test.describe('explore', () => {
  test('browses, searches, and opens a transcript', async ({ page, theme }, testInfo) => {
    await page.goto('/explore')

    const body = page.locator('.cex-explore-body').first()
    await expect(body).toBeVisible()
    await expect(page).toHaveTitle(/village/i)
    await expectTheme(page, theme)

    // Semantic surface: roles and names a screen reader exposes, not class strings.
    await expect(page.getByRole('complementary', { name: 'filters' })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'explore transcripts' })).toBeVisible()

    // Record the full accessibility tree as an agent-reviewable artifact.
    const aria = await body.ariaSnapshot()
    writeFileSync(testInfo.outputPath('explore-aria.yml'), aria)
    await testInfo.attach('explore-aria.yml', {
      path: testInfo.outputPath('explore-aria.yml'),
      contentType: 'text/yaml',
    })

    // Element design-system contract, asserted on computed values.
    await expectComputedTokens(page, '.cex-explore-body', {
      fontFamilyIncludes: 'atkinson',
      borderRadius: '0px',
      minFontSize: 16,
    })

    // WCAG: no critical or serious violations; the full report is written for agents.
    const axe = await scanAxe(page)
    writeFileSync(testInfo.outputPath('axe.json'), JSON.stringify(axe, null, 2))
    await testInfo.attach('axe.json', {
      path: testInfo.outputPath('axe.json'),
      contentType: 'application/json',
    })
    const blocking = seriousViolations(axe)
    expect(blocking, JSON.stringify(blocking, null, 2)).toEqual([])

    // Search narrows the browse to the matching collective.
    const search = page.locator('.cex-searchbar input').first()
    await search.fill('ai')
    await expect(page.getByText('AI Research Team').first()).toBeVisible()

    // Open the first result through the real client route.
    const first = page.locator('a[href^="/transcripts/"]').first()
    const href = await first.getAttribute('href')
    await first.click()
    await expect(page).toHaveURL(
      new RegExp(href.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')),
    )
  })

  test('filters the list by provider', async ({ page }) => {
    await page.goto('/explore')
    await expect(page.locator('.cex-explore-body').first()).toBeVisible()

    const providers = page.getByRole('group', { name: 'providers' })
    await providers.getByRole('button', { name: 'opencode' }).click()

    // The claude-code row leaves the grid once the server-side filter applies...
    await expect(
      page.getByRole('heading', { name: 'Building a REST API from scratch' }),
    ).toHaveCount(0)
    // ...and both opencode rows remain.
    await expect(page.getByRole('heading', { name: 'Greenfield React app setup' })).toBeVisible()
    await expect(
      page.getByRole('heading', { name: 'Started by a session this response does not carry' }),
    ).toBeVisible()
  })

  test('reorders the list by turn count', async ({ page }) => {
    await page.goto('/explore')
    await expect(page.locator('.cex-explore-body').first()).toBeVisible()

    // Recent order puts the newest session first.
    const firstTitle = page.locator('a[href^="/transcripts/"]').first().getByRole('heading')
    await expect(firstTitle).toHaveText('Building a REST API from scratch')

    // The radio's input is covered by its own decorative dot; a user clicks the
    // label, so click the label text.
    await page.getByText('most turns', { exact: true }).click()

    // Highest turn count wins: "Multi-agent debugging session" has 203 turns.
    await expect(firstTitle).toHaveText('Multi-agent debugging session')
  })

  test('switches between grid and list', async ({ page }) => {
    await page.goto('/explore')
    await expect(page.locator('.cex-explore-body').first()).toBeVisible()

    const grid = page.getByRole('button', { name: 'grid view' })
    const list = page.getByRole('button', { name: 'list view' })

    await expect(grid).toHaveAttribute('aria-pressed', 'true')
    await list.click()
    await expect(list).toHaveAttribute('aria-pressed', 'true')
    await expect(grid).toHaveAttribute('aria-pressed', 'false')

    // Transcripts stay present in both layouts.
    await expect(page.locator('a[href^="/transcripts/"]').first()).toBeVisible()
  })

  test('search surfaces a matching collective and opens it', async ({ page }) => {
    await page.goto('/explore')
    await expect(page.locator('.cex-explore-body').first()).toBeVisible()

    await page.locator('.cex-searchbar input').first().fill('ai')

    const collective = page
      .getByRole('region', { name: 'matching collectives' })
      .getByRole('link', { name: /AI Research Team/ })
    await expect(collective).toBeVisible()
    await expect(collective).toHaveAttribute('href', '/groups/ai-research-team')

    await collective.click()
    await expect(page).toHaveURL(/\/groups\/ai-research-team/)
  })
})