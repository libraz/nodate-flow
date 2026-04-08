/**
 * Playwright config for the nodate-flow web app.
 *
 * The webServer block boots the Vite dev server on :5173. The backend
 * API must be running separately (see e2e/README.md). The base URL of
 * the backend is read from NF_API_URL inside the test fixtures.
 */

import { defineConfig, devices } from '@playwright/test';

const isCI = !!process.env.CI;

const WEB_BASE_URL = process.env.NF_WEB_URL ?? 'http://localhost:5173';

export default defineConfig({
  testDir: './e2e',
  testMatch: /.*\.spec\.ts$/,
  fullyParallel: true,
  forbidOnly: isCI,
  retries: isCI ? 2 : 0,
  ...(isCI ? { workers: 2 } : {}),
  reporter: [['html', { open: 'never' }]],
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
