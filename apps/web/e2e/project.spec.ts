/**
 * Project creation E2E.
 *
 * Happy path:
 *   1. Create a fresh tenant via REST (workspace pre-seeded).
 *   2. Inject auth and navigate to the workspace projects page.
 *   3. Create a new project via the UI.
 *   4. Verify the project appears and navigate to it.
 */

import { expect, test } from '@playwright/test';

import { type TestTenant, cleanupTenant, createTestTenant, injectAuth } from './fixtures/tenant';

test.describe('project', () => {
  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('create project in workspace and navigate to it', async ({ page }) => {
    tenant = await createTestTenant();
    await injectAuth(page.context(), tenant);

    await page.goto(`/workspaces/${tenant.workspaceId}/projects`);

    // Open project creation dialog
    await page.getByRole('button', { name: /new project|create project/i }).click();

    const projectName = `Test Project ${Date.now()}`;
    await page.getByLabel(/name/i).fill(projectName);
    await page.getByRole('button', { name: /create/i }).click();

    // Verify project appears in the list
    await expect(page.getByText(projectName)).toBeVisible({ timeout: 10_000 });

    // Navigate to the project
    await page.getByText(projectName).click();

    // Verify we landed on the project page (URL contains project identifier)
    await expect(page).toHaveURL(/\/projects\//, { timeout: 5_000 });
  });
});
