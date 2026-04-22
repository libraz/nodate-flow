/**
 * Admin detail pages e2e tests.
 *
 * Verifies navigation to user detail and workspace detail pages from
 * the admin list views. Requires the `adminGranted` tenant to have
 * instance-admin privileges; skips otherwise.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

test.describe('admin detail pages', () => {
  test.beforeEach(() => {
    const { adminGranted } = loadTenants();
    test.skip(!adminGranted, 'Admin grant failed — instance already has an admin from a prior run');
  });

  test('user detail page renders from user list', async ({ page }) => {
    const { admin } = loadTenants();
    await injectAuth(page.context(), admin);
    await page.goto('/admin/users');
    await page.waitForLoadState('networkidle');

    // Wait for the user table to load
    await page.waitForSelector('td', { timeout: 10_000 });

    // Click the first user detail link
    const userLink = page.locator('a[href*="/admin/users/"]').first();
    await expect(userLink).toBeVisible({ timeout: 10_000 });
    await userLink.click();

    // Should navigate to the user detail page
    await expect(page).toHaveURL(/\/admin\/users\//, { timeout: 10_000 });

    // "User Details" heading
    await expect(page.getByRole('heading', { name: /user details/i })).toBeVisible({
      timeout: 10_000,
    });

    // User info fields should be present
    await expect(page.getByText(/email/i).first()).toBeVisible({ timeout: 10_000 });
    await expect(page.getByText(/status/i).first()).toBeVisible({ timeout: 10_000 });

    // Action buttons (suspend/enable, grant/revoke admin)
    const suspendOrEnable = page.getByRole('button', { name: /suspend|enable/i });
    await expect(suspendOrEnable.first()).toBeVisible({ timeout: 10_000 });

    const adminToggle = page.getByRole('button', { name: /grant admin|revoke admin/i });
    await expect(adminToggle.first()).toBeVisible({ timeout: 10_000 });
  });

  test('user detail page has sessions section', async ({ page }) => {
    const { admin } = loadTenants();
    await injectAuth(page.context(), admin);
    await page.goto('/admin/users');
    await page.waitForLoadState('networkidle');

    await page.waitForSelector('td', { timeout: 10_000 });

    const userLink = page.locator('a[href*="/admin/users/"]').first();
    await expect(userLink).toBeVisible({ timeout: 10_000 });
    await userLink.click();

    await expect(page).toHaveURL(/\/admin\/users\//, { timeout: 10_000 });

    // Sessions heading
    await expect(page.getByRole('heading', { name: /sessions/i })).toBeVisible({
      timeout: 10_000,
    });
  });

  test('user detail page back link returns to user list', async ({ page }) => {
    const { admin } = loadTenants();
    await injectAuth(page.context(), admin);
    await page.goto('/admin/users');
    await page.waitForLoadState('networkidle');

    await page.waitForSelector('td', { timeout: 10_000 });

    const userLink = page.locator('a[href*="/admin/users/"]').first();
    await expect(userLink).toBeVisible({ timeout: 10_000 });
    await userLink.click();

    await expect(page).toHaveURL(/\/admin\/users\//, { timeout: 10_000 });

    // Click back link
    const backLink = page.getByRole('link', { name: /back/i });
    await expect(backLink).toBeVisible({ timeout: 10_000 });
    await backLink.click();

    await expect(page).toHaveURL(/\/admin\/users$/, { timeout: 10_000 });
  });

  test('workspace detail page renders from workspace list', async ({ page }) => {
    const { admin } = loadTenants();
    await injectAuth(page.context(), admin);
    await page.goto('/admin/workspaces');
    await page.waitForLoadState('networkidle');

    // Wait for the workspace table to load (may have no rows)
    const hasRows = await page.waitForSelector('td', { timeout: 10_000 }).then(
      () => true,
      () => false,
    );
    test.skip(!hasRows, 'No workspaces in the admin list to test detail navigation');

    // Click the first workspace detail link
    const wsLink = page.locator('a[href*="/admin/workspaces/"]').first();
    await expect(wsLink).toBeVisible({ timeout: 10_000 });
    await wsLink.click();

    // Should navigate to the workspace detail page
    await expect(page).toHaveURL(/\/admin\/workspaces\//, { timeout: 10_000 });

    // "Workspace Details" heading
    await expect(page.getByRole('heading', { name: /workspace details/i })).toBeVisible({
      timeout: 10_000,
    });

    // Workspace info fields should be present
    await expect(page.getByText(/slug/i).first()).toBeVisible({ timeout: 10_000 });
    await expect(page.getByText(/members/i).first()).toBeVisible({ timeout: 10_000 });
    await expect(page.getByText(/status/i).first()).toBeVisible({ timeout: 10_000 });

    // Suspend/Enable action button
    const actionButton = page.getByRole('button', { name: /suspend|enable/i });
    await expect(actionButton.first()).toBeVisible({ timeout: 10_000 });
  });

  test('workspace detail page back link returns to workspace list', async ({ page }) => {
    const { admin } = loadTenants();
    await injectAuth(page.context(), admin);
    await page.goto('/admin/workspaces');
    await page.waitForLoadState('networkidle');

    const hasRows = await page.waitForSelector('td', { timeout: 10_000 }).then(
      () => true,
      () => false,
    );
    test.skip(!hasRows, 'No workspaces in the admin list to test detail navigation');

    const wsLink = page.locator('a[href*="/admin/workspaces/"]').first();
    await expect(wsLink).toBeVisible({ timeout: 10_000 });
    await wsLink.click();

    await expect(page).toHaveURL(/\/admin\/workspaces\//, { timeout: 10_000 });

    // Click back link
    const backLink = page.getByRole('link', { name: /back/i });
    await expect(backLink).toBeVisible({ timeout: 10_000 });
    await backLink.click();

    await expect(page).toHaveURL(/\/admin\/workspaces$/, { timeout: 10_000 });
  });

  test('accessibility check on user detail page', async ({ page }) => {
    const { admin } = loadTenants();
    await injectAuth(page.context(), admin);
    await page.goto('/admin/users');
    await page.waitForLoadState('networkidle');

    await page.waitForSelector('td', { timeout: 10_000 });

    const userLink = page.locator('a[href*="/admin/users/"]').first();
    await expect(userLink).toBeVisible({ timeout: 10_000 });
    await userLink.click();

    await page.waitForLoadState('networkidle');
    await checkA11y(page, ['color-contrast', 'region']);
  });

  test('accessibility check on workspace detail page', async ({ page }) => {
    const { admin } = loadTenants();
    await injectAuth(page.context(), admin);
    await page.goto('/admin/workspaces');
    await page.waitForLoadState('networkidle');

    const hasRows = await page.waitForSelector('td', { timeout: 10_000 }).then(
      () => true,
      () => false,
    );
    test.skip(!hasRows, 'No workspaces in the admin list to test detail navigation');

    const wsLink = page.locator('a[href*="/admin/workspaces/"]').first();
    await expect(wsLink).toBeVisible({ timeout: 10_000 });
    await wsLink.click();

    await page.waitForLoadState('networkidle');
    await checkA11y(page, ['color-contrast', 'region']);
  });
});
