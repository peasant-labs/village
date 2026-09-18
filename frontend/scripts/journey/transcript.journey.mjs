/* Journey: village transcript viewer.
 *
 * Opens a stored transcript through the real route (REST list/detail/content ->
 * the SessionDetailV2 adapter -> Fairtrade's TranscriptViewer), which also
 * exercises the composed mock's transcript proxy. Records the viewer's
 * accessibility tree and axe report for agent review.
 */
import { writeFileSync } from 'node:fs'
import { test, expect } from './lib/fixtures.mjs'
import { scanAxe, seriousViolations, expectTheme, expectComputedTokens } from './lib/assertions.mjs'

test.describe('transcript viewer', () => {
  test('opens a stored transcript', async ({ page, theme }, testInfo) => {
    await page.goto('/transcripts/demo')

    const viewer = page.locator('.txn-app')
    await expect(viewer).toBeVisible()
    await expectTheme(page, theme)

    // Turns render from the proxied detail + content payloads.
    await expect(page.locator('.txn-turnwrap').first()).toBeVisible()

    const aria = await viewer.ariaSnapshot()
    writeFileSync(testInfo.outputPath('transcript-aria.yml'), aria)
    await testInfo.attach('transcript-aria.yml', {
      path: testInfo.outputPath('transcript-aria.yml'),
      contentType: 'text/yaml',
    })

    await expectComputedTokens(page, '.txn-body', {
      fontFamilyIncludes: 'atkinson',
      minFontSize: 16,
    })

    const axe = await scanAxe(page)
    writeFileSync(testInfo.outputPath('axe.json'), JSON.stringify(axe, null, 2))
    await testInfo.attach('axe.json', {
      path: testInfo.outputPath('axe.json'),
      contentType: 'application/json',
    })
    const blocking = seriousViolations(axe)
    expect(blocking, JSON.stringify(blocking, null, 2)).toEqual([])
  })
})