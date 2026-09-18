/* Playwright journey harness for the village frontend.
 *
 * A journey is a scripted, agent-runnable feature flow that produces a
 * deterministic trace, an optional live clip, and machine-readable assertions
 * (routes, roles, computed design tokens, WCAG). It is the motion/interaction
 * arm the Puppeteer capture harness (scripts/visual) does not cover.
 *
 * Boots the app and its mock REST backend itself, so `pnpm journey` is the only
 * command needed. Chrome is the NixOS-packaged browser via CHROME_PATH (or the
 * default profile path); no Playwright browser download is required.
 *
 * Artifacts land in scripts/journey/.artifacts (gitignored):
 *   report.json      machine-readable per-test results
 *   html/            human report (pnpm journey:report)
 *   <test>/          trace.zip + video + screenshots for failing tests
 *   attachments/     the ARIA tree and axe report each journey records
 */
import { defineConfig } from '@playwright/test'

const APP_PORT = Number(process.env.JOURNEY_APP_PORT || 3010)
const MOCK_PORT = Number(process.env.JOURNEY_MOCK_PORT || 8799)
const APP_URL = `http://localhost:${APP_PORT}`
const API_URL = `http://localhost:${MOCK_PORT}/api/v1`
// Optional browser override. When unset, Playwright uses its own installed
// Chromium (as on CI); set CHROME_PATH to drive a platform browser instead.
const CHROME = process.env.CHROME_PATH || undefined
const ARTIFACTS = 'scripts/journey/.artifacts'

// Capture viewport. 720p keeps the desktop (lg/xl) layout while cutting screenshot
// pixels ~30% versus 1396x939. Override with JOURNEY_VIEWPORT=WxH.
const [VIEWPORT_W, VIEWPORT_H] = (process.env.JOURNEY_VIEWPORT || '1280x720').split('x').map(Number)

// Artifact policy: screenshots are cheap and on by default (useful for agents and
// the HTML report); traces and video are retained on failure, with opt-in
// switches for whole-run capture. The CLI's `--trace <mode>` overrides TRACE.
const SCREENSHOT = process.env.JOURNEY_SCREENSHOT === 'off' ? 'only-on-failure' : 'on'
const TRACE = process.env.JOURNEY_TRACE === '1' ? 'on' : 'retain-on-failure'
const VIDEO = process.env.JOURNEY_VIDEO === '1' ? 'on' : 'retain-on-failure'
const HTML = process.env.JOURNEY_HTML !== '0'

export default defineConfig({
  testDir: './scripts/journey',
  testMatch: '**/*.journey.mjs',
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  outputDir: `${ARTIFACTS}/results`,
  reporter: [
    ['list'],
    ...(HTML ? [['html', { outputFolder: `${ARTIFACTS}/html`, open: 'never' }]] : []),
    ['json', { outputFile: `${ARTIFACTS}/report.json` }],
  ],
  use: {
    baseURL: APP_URL,
    viewport: { width: VIEWPORT_W, height: VIEWPORT_H },
    reducedMotion: 'reduce',
    trace: TRACE,
    video: VIDEO,
    screenshot: SCREENSHOT,
    launchOptions: { executablePath: CHROME },
  },
  projects: [
    { name: 'dark', use: { colorScheme: 'dark' } },
    { name: 'light', use: { colorScheme: 'light' } },
  ],
  webServer: [
    {
      command: `MOCK_REST_PORT=${MOCK_PORT} node scripts/journey/mock.mjs`,
      url: `http://localhost:${MOCK_PORT}/api/v1/tags/popular`,
      reuseExistingServer: !process.env.CI,
      timeout: 30_000,
      stdout: 'ignore',
      stderr: 'pipe',
    },
    {
      command: `NEXT_PUBLIC_API_URL=${API_URL} PORT=${APP_PORT} pnpm dev`,
      url: `${APP_URL}/explore`,
      reuseExistingServer: !process.env.CI,
      timeout: 180_000,
      stdout: 'ignore',
      stderr: 'pipe',
    },
  ],
})