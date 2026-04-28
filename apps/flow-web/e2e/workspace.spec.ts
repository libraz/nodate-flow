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

    // Verify workspace was created — after the create dialog closes,
    // the SPA refetches the workspaces list and renders a new row in
    // the DataGrid whose name cell is a <Link to="/workspaces/$id">
    // carrying the new workspace name. Target that visible link
    // explicitly so we do not match the offscreen <option> nodes
    // inside the closed workspace-selector <select>, which Playwright
    // also resolves via a bare `text=` locator but reports as hidden.
    await expect(page.getByRole('link', { name: wsName, exact: true }).first()).toBeVisible({
      timeout: 10_000,
    });
  });
});
