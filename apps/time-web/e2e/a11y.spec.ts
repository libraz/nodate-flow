/**
 * Time-web accessibility e2e tests.
 *
 * Runs axe-core checks on key pages.
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

  test('calendar page passes a11y checks', async ({ page }) => {
    await injectAuth(page.context(), tenant);
    await page.goto('/calendar');
    await page.waitForLoadState('networkidle');
    // Allow extra time for calendar to render
    await page.waitForTimeout(1000);
    await checkA11y(page, ['color-contrast']);
  });
});
