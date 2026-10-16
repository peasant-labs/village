import { defineConfig } from '@playwright/test'

if (!process.env.COLLECTIVES_BUILT_ROOT || !process.env.COLLECTIVES_BUILT_URL) {
  throw new Error('Run journey:collectives-built so the owned production build and fixture start before Playwright')
}
export default defineConfig({
  testDir: './scripts/journey',
  testMatch: 'collectives-built.journey.mjs',
  workers: 1,
  retries: 0,
  fullyParallel: false,
  forbidOnly: true,
  timeout: 60_000,
  globalTimeout: 300_000,
  expect: { timeout: 10_000 },
  outputDir: `${process.env.COLLECTIVES_BUILT_ROOT}/results`,
  reporter: [['list'], ['json', { outputFile: `${process.env.COLLECTIVES_BUILT_ROOT}/report.json` }]],
  use: {
    baseURL: process.env.COLLECTIVES_BUILT_URL,
    viewport: { width: 1280, height: 900 },
    reducedMotion: 'reduce',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    launchOptions: { executablePath: process.env.CHROME_PATH || undefined },
  },
  projects: ['dark', 'light'].map((name) => ({ name, use: { colorScheme: name } })),
  // The wrapper owns next start. Never reuse a dev server or an existing build.
})
