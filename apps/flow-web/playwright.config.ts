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
const WEB_PORT = (() => {
  try {
    return new URL(WEB_BASE_URL).port || '5173';
  } catch {
    return '5173';
  }
})();
const BROWSER_CHANNEL = process.env.PW_BROWSER_CHANNEL;

export default defineConfig({
  testDir: './e2e',
  testMatch: /.*\.spec\.ts$/,
  globalSetup: './e2e/fixtures/global-setup.ts',
  globalTeardown: './e2e/fixtures/global-teardown.ts',
  globalTimeout: 10 * 60_000,
  fullyParallel: true,
  forbidOnly: isCI,
  retries: isCI ? 2 : 1,
  ...(isCI ? { workers: 2 } : { workers: 3 }),
  reporter: [['html', { open: 'never' }]],
  snapshotDir: './e2e/snapshots',
  snapshotPathTemplate: '{snapshotDir}/{arg}{ext}',
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
      use: {
        ...devices['Desktop Chrome'],
        ...(BROWSER_CHANNEL ? { channel: BROWSER_CHANNEL } : {}),
      },
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
