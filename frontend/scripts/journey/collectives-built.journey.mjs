import { readFileSync, writeFileSync } from 'node:fs'
import { test, expect } from '@playwright/test'
import { loadLegacyCollectiveFixtures } from '../../src/test/collectivesLegacyFixtures.ts'
import { expectTheme, expectComputedTokens, scanAxe, seriousViolations } from './lib/assertions.mjs'
import { zVillageGroupedContributableResponse, zVillageSessionListPayload } from '@peasant-labs/schema'

test('built collective navigation and every legacy inbound link', async ({ page, context, request }, info) => {
  const theme = info.project.name
  const manifest = JSON.parse(readFileSync(`${process.env.COLLECTIVES_BUILT_ROOT}/build.json`, 'utf8'))
  expect(manifest.servedVerified).toBe(true)
  await context.addInitScript((value) => localStorage.setItem('peasant-theme', value), theme)
  await context.addCookies([{ name: 'peasant_token', value: 'demo-token', url: process.env.COLLECTIVES_BUILT_URL }])
  const requests = []
  page.on('request', (req) => {
    if (req.url().startsWith(manifest.apiBase)) requests.push(new URL(req.url()).pathname)
  })
  expect((await request.post(`${manifest.apiBase}/__built/contribution-context?enabled=0`)).ok()).toBe(true)
  await page.goto('/collectives')
  const nav = page.getByRole('navigation', { name: 'main navigation' })
  const link = nav.getByRole('link', { name: 'collectives', exact: true })
  await expect(link).toHaveAttribute('href', '/collectives')
  await nav.getByRole('link', { name: 'home', exact: true }).click()
  await link.click()
  await expect(page).toHaveURL(/\/collectives$/)
  for (const row of loadLegacyCollectiveFixtures()) {
    const target = `/collectives${row.suffix}?${new URLSearchParams(row.query)}`
    const response = await request.get(`/groups${row.suffix}?${row.query}`, { maxRedirects: 0 })
    expect(response.status()).toBe(307)
    expect(response.headers().location).toBe(target)
    const queue = row.name === 'contribute' || row.name === 'review'
      ? page.waitForResponse((response) => response.url().includes(`/collectives${row.suffix.replace(/\/review$/, '/pending').replace(/\/contribute$/, '/contributable')}?view=grouped`))
      : undefined
    await page.goto(`/groups${row.suffix}?${row.query}`)
    await expect(page).toHaveURL(`${process.env.COLLECTIVES_BUILT_URL}${target}`)
    await expectTheme(page, theme)
    await expect(link).toHaveAttribute('aria-current', 'page')
    await expect(page.getByRole(row.role, { name: new RegExp(row.body) }).first()).toBeVisible()
    if (queue) {
      const response = await queue
      expect(response.ok()).toBe(true)
      const schema = row.name === 'contribute' ? zVillageGroupedContributableResponse : zVillageSessionListPayload
      expect(schema.safeParse(await response.json()).success).toBe(true)
    }
    for (const assertion of row.assertions) {
      const locator = assertion.testId ? page.getByTestId(assertion.testId) : page.getByRole(assertion.role, { name: assertion.name, exact: true })
      await expect(locator).toBeVisible()
      if (assertion.text) await expect(locator).toContainText(assertion.text)
      if (assertion.disabled === true) await expect(locator).toBeDisabled()
      if (assertion.disabled === false) await expect(locator).toBeEnabled()
    }
    await expectComputedTokens(page, 'main', { fontFamilyIncludes: 'atkinson', borderRadius: '0px', minFontSize: 16 })
    await page.waitForLoadState('networkidle')
    await page.evaluate(() => document.fonts.ready)
    await page.screenshot({ path: `${process.env.COLLECTIVES_BUILT_ROOT}/${theme}-${row.name}.png`, fullPage: true, animations: 'disabled' })
    if (row.back) {
      const back = page.getByRole('link', { name: row.back.name, exact: true })
      await expect(back).toHaveAttribute('href', row.back.href)
      await back.click()
      await expect(page).toHaveURL(`${process.env.COLLECTIVES_BUILT_URL}${row.back.href}`)
      await expect(page.getByRole(row.back.role, { name: row.back.body, exact: true })).toBeVisible()
    }
  }
  // A nonempty grouped-only exit proves the real hook consumed the canonical
  // envelope successfully; an empty flat body cannot hide a decode error.
  expect((await request.post(`${manifest.apiBase}/__built/contribution-context?enabled=1`)).ok()).toBe(true)
  await page.goto('/collectives/c011ec71-0000-4000-8000-000000000001/contribute')
  await expect(page.getByTestId('grouped-helper-fallback').getByTestId('helper-group-toggle')).toBeVisible()
  await expect(page.getByRole('button', { name: 'contribute 0 transcripts', exact: true })).toBeDisabled()
  await page.goto('/collectives')
  const table = page.getByRole('table', { name: 'collectives 3' })
  await expect(table).toBeVisible()
  await table.getByRole('button', { name: 'Acme Platform', exact: true }).click()
  await expect(page).toHaveURL(/\/collectives\/c011ec71-0000-4000-8000-000000000001$/)
  await expect(page.getByRole('heading', { name: 'Acme Platform', exact: true })).toBeVisible()
  const styles = await page.getByRole('main').evaluate((node) => {
    const css = getComputedStyle(node)
    return { fontFamily: css.fontFamily, fontSize: css.fontSize, radius: css.borderRadius, theme: document.documentElement.dataset.theme }
  })
  writeFileSync(`${process.env.COLLECTIVES_BUILT_ROOT}/${theme}-computed.json`, JSON.stringify(styles, null, 2))
  const axe = await scanAxe(page)
  writeFileSync(`${process.env.COLLECTIVES_BUILT_ROOT}/${theme}-axe.json`, JSON.stringify(axe, null, 2))
  expect(seriousViolations(axe)).toEqual([])
  expect(requests).toContain('/api/v1/collectives/visible')
  expect(requests).toContain('/api/v1/collectives/c011ec71-0000-4000-8000-000000000001')
  expect(requests.some((path) => path.includes('/groups') || path.includes('/api/v1/api/v1'))).toBe(false)
  writeFileSync(`${process.env.COLLECTIVES_BUILT_ROOT}/${theme}-requests.json`, JSON.stringify(requests, null, 2))
})
