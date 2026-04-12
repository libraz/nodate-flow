/**
 * Workspace creation E2E.
 *
 * Happy path:
 *   1. Create a fresh tenant via REST.
 *   2. Inject auth and navigate to the authenticated root.
 *   3. Create a new workspace via the UI.
 *   4. Verify the workspace appears in the sidebar.
 */

import { expect, test } from '@playwright/test';

import { type TestTenant, cleanupTenant, createTestTenant, injectAuth } from './fixtures/tenant';

test.describe('workspace', () => {
  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('create workspace and verify it appears in sidebar', async ({ page }) => {
    tenant = await createTestTenant();
    await injectAuth(page.context(), tenant);

    await page.goto('/');
    await expect(page).toHaveURL(/\//, { timeout: 10_000 });

    // Open workspace creation dialog
    await page.getByRole('button', { name: /new workspace|create workspace/i }).click();

    const wsName = `Test WS ${Date.now()}`;
    await page.getByLabel(/name/i).fill(wsName);
    await page.getByRole('button', { name: /create/i }).click();

    // Verify workspace appears in the sidebar navigation
    const sidebar = page.getByRole('navigation', { name: /sidebar/i });
    await expect(sidebar.getByText(wsName)).toBeVisible({ timeout: 10_000 });
  });
});
