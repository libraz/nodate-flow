/**
 * Playwright config for time-web (calendar UI).
 *
 * The webServer block boots the Vite dev server on :5174. The time-api
 * backend must be running separately on :8081. Auth goes through
 * auth-api on :8082.
 */

import { defineConfig, devices } from '@playwright/test';

const isCI = !!process.env.CI;

const WEB_BASE_URL = process.env.NF_TIME_WEB_URL ?? 'http://localhost:5174';

export default defineConfig({
  testDir: './e2e',
  testMatch: /.*\.spec\.ts$/,
  fullyParallel: true,
  forbidOnly: isCI,
  retries: isCI ? 2 : 0,
  ...(isCI ? { workers: 2 } : {}),
  reporter: [['html', { open: 'never' }]],
  expect: {
    toHaveScreenshot: {
      maxDiffPixelRatio: 0.01,
    },
  },
  use: {
    baseURL: WEB_BASE_URL,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
  webServer: {
    command: 'bun run dev',
    url: WEB_BASE_URL,
    reuseExistingServer: !isCI,
    timeout: 120_000,
    stdout: 'pipe',
    stderr: 'pipe',
  },
});
