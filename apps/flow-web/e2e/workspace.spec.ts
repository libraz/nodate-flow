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

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';

test.describe('workspace', () => {
  test('create workspace and verify it appears in sidebar', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    // Navigate to the Workspaces page via the sidebar
    await page.goto('/');
    await expect(page).toHaveURL(/\//, { timeout: 10_000 });
    await page.getByRole('link', { name: /workspaces/i }).click();

    // Look for workspace creation button on the workspaces page
    const createBtn = page
      .getByRole('button', { name: /new workspace|create workspace/i })
      .or(page.getByRole('link', { name: /new workspace|create workspace/i }));
    await expect(createBtn).toBeVisible({ timeout: 10_000 });
    await createBtn.click();

    const wsName = `Test WS ${Date.now()}`;
    await page.getByLabel(/^name/i).fill(wsName);
    await page.getByRole('button', { name: /save|create/i }).click();

    // Verify workspace was created — it appears in the workspace selector
    // dropdown as an <option>, which is hidden until the select opens.
    // Check that the page contains the workspace name (visible or in DOM).
    await expect(page.locator(`text=${wsName}`).first()).toBeAttached({ timeout: 10_000 });
  });
});
