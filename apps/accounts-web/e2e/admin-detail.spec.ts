/**
 * Admin detail pages e2e tests.
 *
 * Verifies navigation to user detail and workspace detail pages from
 * the admin list views. The shared admin tenant is granted
 * instance-admin in global setup, which fails the run if it cannot.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

test.describe('admin detail pages', () => {
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

    // Click back link (page-level, distinct from layout sidebar's
    // "Back to profile" link).
    const backLink = page.getByRole('link', { name: /back to users/i });
    await expect(backLink).toBeVisible({ timeout: 10_000 });
    await backLink.click();

    await expect(page).toHaveURL(/\/admin\/users$/, { timeout: 10_000 });
  });

  test('workspace detail page renders from workspace list', async ({ page }) => {
    const { admin } = loadTenants();
    await injectAuth(page.context(), admin);
    await page.goto('/admin/workspaces');
    await page.waitForLoadState('networkidle');

    // globalSetup seeds a workspace for the admin tenant, so the list
    // has at least one row by construction. This used to skip when the
    // table came up empty, which meant an admin list that stopped
    // rendering rows was reported as "nothing to test" rather than as
    // the regression it is.
    await page.waitForSelector('td', { timeout: 10_000 });

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

    await page.waitForSelector('td', { timeout: 10_000 });

    const wsLink = page.locator('a[href*="/admin/workspaces/"]').first();
    await expect(wsLink).toBeVisible({ timeout: 10_000 });
    await wsLink.click();

    await expect(page).toHaveURL(/\/admin\/workspaces\//, { timeout: 10_000 });

    // Click back link (page-level, distinct from layout sidebar's
    // "Back to profile" link).
    const backLink = page.getByRole('link', { name: /back to workspaces/i });
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

    await page.waitForSelector('td', { timeout: 10_000 });

    const wsLink = page.locator('a[href*="/admin/workspaces/"]').first();
    await expect(wsLink).toBeVisible({ timeout: 10_000 });
    await wsLink.click();

    await page.waitForLoadState('networkidle');
    await checkA11y(page, ['color-contrast', 'region']);
  });
});
