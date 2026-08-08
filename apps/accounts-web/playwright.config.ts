/**
 * Playwright config for accounts-web (auth UI).
 *
 * The webServer block boots the Vite dev server on :5175. The auth-api
 * backend must be running separately on :8082.
 */

import { defineConfig, devices } from '@playwright/test';

const isCI = !!process.env.CI;

const WEB_BASE_URL = process.env.NF_ACCOUNTS_WEB_URL ?? 'http://localhost:5175';
const WEB_PORT = (() => {
  try {
    return new URL(WEB_BASE_URL).port || '5175';
  } catch {
    return '5175';
  }
})();

export default defineConfig({
  globalSetup: './e2e/fixtures/global-setup.ts',
  globalTeardown: './e2e/fixtures/global-teardown.ts',
  testDir: './e2e',
  testMatch: /.*\.spec\.ts$/,
  fullyParallel: true,
  globalTimeout: 10 * 60 * 1000, // 10 min — includes rate-limit waits in global setup
  forbidOnly: isCI,
  retries: isCI ? 2 : 0,
  workers: isCI ? 2 : 3,
  reporter: [['html', { open: 'never' }]],
  // No visual-regression setup here on purpose — see the same note in
  // apps/flow-web/playwright.config.ts. Screenshot baselines need a
  // pinned rendering environment this suite does not have.
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
    command: `bun run dev -- --port=${WEB_PORT} --strictPort`,
    url: WEB_BASE_URL,
    reuseExistingServer: !isCI,
    timeout: 120_000,
    stdout: 'pipe',
    stderr: 'pipe',
  },
});
