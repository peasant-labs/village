/* Journey: project page orphan sessions.
 *
 * Opens a project page through the real route, expands the collapsed
 * `orphan sessions` group the page builds for ancestries it cannot resolve,
 * and opens one orphan session through the real transcript route. It asserts
 * the semantic surface (roles, labels, links) rather than class strings, and
 * records the accessible tree so an agent can review what the group and the
 * opened session expose.
 */
import { writeFileSync } from 'node:fs'
import { test, expect } from './lib/fixtures.mjs'
import { expectTheme } from './lib/assertions.mjs'
import {
  JOURNEY_PROJECT,
  JOURNEY_PROJECT_TOTAL,
} from './lib/project-fixtures.mjs'

test.describe('project orphan sessions', () => {
  test('expands the orphan group and opens a session', async ({ page, theme }, testInfo) => {
    await page.goto(`/users/${JOURNEY_PROJECT.owner}/projects/${JOURNEY_PROJECT.hash}`)
    await expectTheme(page, theme)

    // The panel count answers how much the project holds, heard by the page.
    await expect(page.getByTestId('project-transcript-count')).toHaveText(
      String(JOURNEY_PROJECT_TOTAL),
    )

    // The genuine root is in the list; the orphan rows are held back from it.
    await expect(
      page.locator(`a[href="/transcripts/${JOURNEY_PROJECT.rootTranscriptID}"]`),
    ).toBeVisible()
    await expect(
      page.locator(`a[href="/transcripts/${JOURNEY_PROJECT.orphanTranscriptIDs[0]}"]`),
    ).toHaveCount(0)

    // The one control for the group starts collapsed and names its own count.
    const toggle = page.getByTestId('project-orphan-session-group-toggle')
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await expect(page.getByTestId('project-orphan-session-group-label')).toHaveText(
      `orphan sessions ${JOURNEY_PROJECT.orphanTranscriptIDs.length}`,
    )

    await toggle.click()
    await expect(toggle).toHaveAttribute('aria-expanded', 'true')

    // Every orphan row is reachable once the group is open.
    const rows = page.getByTestId('project-orphan-session-rows')
    await expect(rows).toBeVisible()
    for (const id of JOURNEY_PROJECT.orphanTranscriptIDs) {
      await expect(rows.locator(`a[href="/transcripts/${id}"]`)).toBeVisible()
    }

    const aria = await rows.ariaSnapshot()
    writeFileSync(testInfo.outputPath('project-orphan-aria.yml'), aria)
    await testInfo.attach('project-orphan-aria.yml', {
      path: testInfo.outputPath('project-orphan-aria.yml'),
      contentType: 'text/yaml',
    })

    // The expanded group is the surface under test, so it is the still a reader
    // reviews; the clip and the trace carry the click into the session.
    await rows.scrollIntoViewIfNeeded()
    await page.screenshot({
      path: testInfo.outputPath('project-orphan-group.png'),
      fullPage: true,
    })
    await testInfo.attach('project-orphan-group.png', {
      path: testInfo.outputPath('project-orphan-group.png'),
      contentType: 'image/png',
    })

    // Open the first orphan through the real client route and land on the viewer.
    const first = rows.locator(`a[href="/transcripts/${JOURNEY_PROJECT.orphanTranscriptIDs[0]}"]`).first()
    await first.click()
    await expect(page).toHaveURL(
      new RegExp(`/transcripts/${JOURNEY_PROJECT.orphanTranscriptIDs[0]}$`),
    )
    await expect(page.locator('.txn-app')).toBeVisible()
  })
})
