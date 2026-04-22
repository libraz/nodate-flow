/**
 * Time-web accessibility e2e tests.
 *
 * Runs axe-core checks on the remaining time-web surfaces (setup +
 * share). The authenticated /calendar UX moved to flow-web in R5.6;
 * accessibility coverage for the unified calendar lives in
 * `apps/flow-web/e2e/` now.
 */

import { test } from '@playwright/test';

import { type TestTenant, cleanupTenant, createTestTenant, injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

test.describe('time-web accessibility', () => {
  let tenant: TestTenant;

  test.beforeAll(async () => {
    tenant = await createTestTenant();
  });

  test.afterAll(async () => {
    await cleanupTenant(tenant);
  });

  test('setup page passes a11y checks', async ({ page }) => {
    await injectAuth(page.context(), tenant);
    await page.goto('/setup');
    await page.waitForLoadState('networkidle');
    await checkA11y(page, ['color-contrast']);
  });
});
