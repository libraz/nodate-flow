/**
 * Workspace creation E2E.
 *
 * Happy path:
 *   1. Create a fresh tenant via REST.
 *   2. Inject auth and navigate to the authenticated root.
 *   3. Create a new workspace via the UI.
 *   4. Verify the workspace is the active one in the sidebar and listed in
 *      the sidebar workspace switcher menu.
 */

import { expect, test } from '@playwright/test';

import enCommon from '../locales/en/common.json' with { type: 'json' };
import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';

test.describe('workspace', () => {
  test('create workspace and verify it appears in sidebar', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    // Navigate to the Workspaces page via the sidebar workspace switcher menu
    await page.goto('/');
    await expect(page).toHaveURL(/\/$/, { timeout: 10_000 });
    await page.getByRole('button', { name: enCommon.nav.switchWorkspace }).click();
    await page.getByRole('menuitem', { name: enCommon.nav.allWorkspaces, exact: true }).click();
    await expect(page).toHaveURL(/\/workspaces$/, { timeout: 10_000 });

    // Look for workspace creation button on the workspaces page
    const createBtn = page
      .getByRole('button', { name: /new workspace|create workspace/i })
      .or(page.getByRole('link', { name: /new workspace|create workspace/i }));
    await expect(createBtn).toBeVisible({ timeout: 10_000 });
    await createBtn.click();

    const wsName = `Test WS ${Date.now()}`;
    await page.getByLabel(/^name/i).fill(wsName);
    await page.getByRole('button', { name: /save|create/i }).click();

    // Creation activates the new workspace and lands on `/`: the sidebar
    // heading names it, and the switcher menu lists it as the checked item.
    await expect(page).toHaveURL(/\/$/, { timeout: 15_000 });
    await expect(page.getByRole('heading', { name: wsName, level: 2 })).toBeVisible({
      timeout: 10_000,
    });
    await page.getByRole('button', { name: enCommon.nav.switchWorkspace }).click();
    await expect(page.getByRole('menuitemradio', { name: wsName, exact: true })).toHaveAttribute(
      'aria-checked',
      'true',
    );
  });
});
