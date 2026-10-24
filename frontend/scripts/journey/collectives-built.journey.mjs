import { readFileSync, writeFileSync } from 'node:fs'
import { test, expect } from '@playwright/test'
import { loadLegacyCollectiveFixtures } from '../../src/test/collectivesLegacyFixtures.ts'
import { expectTheme, expectComputedTokens, scanAxe, seriousViolations } from './lib/assertions.mjs'
import { signedInCookies } from './lib/collectives-built-auth.mjs'
import { zVillageGroupedContributableResponse, zVillageSessionListPayload } from '@peasant-labs/schema'

test('built collective navigation and every legacy inbound link', async ({ page, context, request }, info) => {
  const theme = info.project.name
  const app = process.env.COLLECTIVES_BUILT_URL
  expect(app, 'COLLECTIVES_BUILT_URL must name the built app this journey drives').toBeTruthy()
  const manifest = JSON.parse(readFileSync(`${process.env.COLLECTIVES_BUILT_ROOT}/build.json`, 'utf8'))
  expect(manifest.servedVerified).toBe(true)
  await context.addInitScript((value) => localStorage.setItem('peasant-theme', value), theme)
  // The same signed-in session the runner's readiness page load used. The proxy,
  // the app frame and the API all read this cookie, so a journey that starts
  // without it is sent to the marketing site before any page renders.
  await context.addCookies(signedInCookies(app))
  const requests = []
  page.on('request', (req) => {
    if (req.url().startsWith(manifest.apiBase)) requests.push(new URL(req.url()).pathname)
  })
  expect((await request.post(`${manifest.apiBase}/__built/contribution-context?enabled=0`)).ok()).toBe(true)
  await page.goto('/collectives')
  // The signed-in chrome: fairtrade's sidebar, with a sections navigation that
  // names the place each page belongs to. The page body is asserted apart from
  // it, so a control that moved into or out of the chrome cannot pass for the
  // other.
  const nav = page.getByRole('navigation', { name: 'peasant sections' })
  const main = page.getByRole('main')
  const link = nav.getByRole('link', { name: 'collectives', exact: true })
  await expect(link).toHaveAttribute('href', '/collectives')
  await nav.getByRole('link', { name: 'home', exact: true }).click()
  await link.click()
  await expect(page).toHaveURL(/\/collectives$/)
  for (const row of loadLegacyCollectiveFixtures()) {
    const target = `/collectives${row.suffix}?${new URLSearchParams(row.query)}`
    // A legacy address is a page load, so the probe sends the context's own
    // session cookie: without it the proxy answers the marketing site's log-in
    // popup and the redirecting page is never reached.
    const response = await context.request.get(`/groups${row.suffix}?${row.query}`, { maxRedirects: 0 })
    expect(response.status(), `the legacy ${row.name} address must redirect, not render`).toBe(307)
    expect(response.headers().location, `the legacy ${row.name} address must land on its canonical route`).toBe(target)
    const queue = row.name === 'contribute' || row.name === 'review'
      ? page.waitForResponse((response) => response.url().includes(`/collectives${row.suffix.replace(/\/review$/, '/pending').replace(/\/contribute$/, '/contributable')}?view=grouped`))
      : undefined
    await page.goto(`/groups${row.suffix}?${row.query}`)
    await expect(page).toHaveURL(`${app}${target}`)
    await expectTheme(page, theme)
    // Exactly one sidebar entry is current, and it is the row's own place: the
    // section on the list, the collective itself on each canonical collective
    // route (the sidebar marks the person's collective, not the list above it).
    await expect(nav.locator('[aria-current="page"]')).toHaveCount(1)
    await expect(nav.getByRole('link', { name: row.navCurrent, exact: true })).toHaveAttribute('aria-current', 'page')
    await expect(main.getByRole(row.role, { name: new RegExp(row.body) }).first()).toBeVisible()
    if (queue) {
      const response = await queue
      expect(response.ok()).toBe(true)
      const schema = row.name === 'contribute' ? zVillageGroupedContributableResponse : zVillageSessionListPayload
      expect(schema.safeParse(await response.json()).success).toBe(true)
    }
    for (const assertion of row.assertions) {
      const locator = assertion.testId ? main.getByTestId(assertion.testId) : main.getByRole(assertion.role, { name: assertion.name, exact: true })
      await expect(locator).toBeVisible()
      if (assertion.text) await expect(locator).toContainText(assertion.text)
      if (assertion.disabled === true) await expect(locator).toBeDisabled()
      if (assertion.disabled === false) await expect(locator).toBeEnabled()
    }
    if (row.name === 'settings') {
      // Execute the real mounted edit/save callback, not merely its button.
      // Restore the fixture's name through that SAME callback for later routes.
      for (const { value } of row.nameEdits) {
        await main.getByRole('button', { name: 'edit name', exact: true }).click()
        const input = main.getByRole('textbox', { name: 'name', exact: true })
        await expect(input).toBeFocused()
        await input.fill(value)
        const saved = page.waitForResponse((response) => response.request().method() === 'PATCH' && response.url() === `${manifest.apiBase}/collectives/c011ec71-0000-4000-8000-000000000001`)
        await main.getByRole('button', { name: 'save', exact: true }).click()
        const response = await saved
        expect(response.ok()).toBe(true)
        expect(response.request().postDataJSON()).toEqual({ name: value })
        // The settings page's title is its section; the collective's name is in the breadcrumb.
        await expect(page.getByRole('navigation', { name: 'breadcrumb' })).toContainText(value)
      }
    }
    await expectComputedTokens(page, 'main', { fontFamilyIncludes: 'atkinson', borderRadius: '0px', minFontSize: 16 })
    await page.waitForLoadState('networkidle')
    await page.evaluate(() => document.fonts.ready)
    const surfaceAxe = await scanAxe(page)
    writeFileSync(`${process.env.COLLECTIVES_BUILT_ROOT}/${theme}-${row.name}-axe.json`, JSON.stringify(surfaceAxe, null, 2))
    expect(seriousViolations(surfaceAxe), `WCAG AA findings on ${row.name}`).toEqual([])
    await page.screenshot({ path: `${process.env.COLLECTIVES_BUILT_ROOT}/${theme}-${row.name}.png`, fullPage: true, animations: 'disabled' })
    if (row.back) {
      // The canonical way back is the page's own breadcrumb: the trail that
      // names where the route sits, the same one the chrome and every inbound
      // link land beside. It is scoped to the breadcrumb because the sidebar
      // offers a link with the same name.
      const back = page.getByRole('navigation', { name: 'breadcrumb' }).getByRole('link', { name: row.back.name, exact: true })
      await expect(back).toHaveAttribute('href', row.back.href)
      await back.click()
      await expect(page).toHaveURL(`${app}${row.back.href}`)
      await expect(main.getByRole(row.back.role, { name: row.back.body, exact: true })).toBeVisible()
    }
  }
  // A nonempty grouped-only exit proves the real hook consumed the canonical
  // envelope successfully; an empty flat body cannot hide a decode error.
  expect((await request.post(`${manifest.apiBase}/__built/contribution-context?enabled=1`)).ok()).toBe(true)
  await page.goto('/collectives/c011ec71-0000-4000-8000-000000000001/contribute')
  await expect(page.getByTestId('grouped-helper-fallback').getByTestId('helper-group-toggle')).toBeVisible()
  await expect(page.getByRole('button', { name: 'contribute 0 transcripts', exact: true })).toBeDisabled()
  await page.goto('/collectives')
  const table = page.getByRole('table', { name: 'collectives you belong to' })
  await expect(table).toBeVisible()
  await table.getByRole('link', { name: 'Acme Platform', exact: true }).click()
  await expect(page).toHaveURL(/\/collectives\/c011ec71-0000-4000-8000-000000000001$/)
  await expect(page.getByRole('heading', { name: 'Acme Platform', exact: true })).toBeVisible()
  const styles = await page.getByRole('main').evaluate((node) => {
    const css = getComputedStyle(node)
    const primary = css.fontFamily.split(',')[0].trim().replaceAll('"', '')
    const faceLoaded = [...document.fonts].some((face) => face.family.replaceAll('"', '') === primary && face.status === 'loaded')
    return { fontFamily: css.fontFamily, fontSize: css.fontSize, radius: css.borderRadius, theme: document.documentElement.dataset.theme, faceLoaded }
  })
  expect(styles.faceLoaded).toBe(true)
  await page.getByRole('link', { name: 'home', exact: true }).first().focus()
  await page.keyboard.press('Tab')
  const focus = await page.locator(':focus').evaluate((node) => {
    const css = getComputedStyle(node)
    return { tag: node.tagName, outline: css.outlineStyle, width: css.outlineWidth, shadow: css.boxShadow }
  })
  expect((focus.outline !== 'none' && parseFloat(focus.width) > 0) || focus.shadow !== 'none').toBe(true)
  styles.keyboardFocus = focus
  writeFileSync(`${process.env.COLLECTIVES_BUILT_ROOT}/${theme}-computed.json`, JSON.stringify(styles, null, 2))
  const axe = await scanAxe(page)
  writeFileSync(`${process.env.COLLECTIVES_BUILT_ROOT}/${theme}-axe.json`, JSON.stringify(axe, null, 2))
  expect(seriousViolations(axe)).toEqual([])
  expect(requests).toContain('/api/v1/collectives/visible')
  expect(requests).toContain('/api/v1/collectives/c011ec71-0000-4000-8000-000000000001')
  expect(requests.some((path) => path.includes('/groups') || path.includes('/api/v1/api/v1'))).toBe(false)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/collectives')
  await expectTheme(page, theme)
  await expect(page.getByRole('table', { name: 'collectives you belong to' })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({ path: `${process.env.COLLECTIVES_BUILT_ROOT}/${theme}-mobile-list.png`, fullPage: true, animations: 'disabled' })
  writeFileSync(`${process.env.COLLECTIVES_BUILT_ROOT}/${theme}-requests.json`, JSON.stringify(requests, null, 2))
})
