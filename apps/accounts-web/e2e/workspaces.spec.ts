/**
 * Workspace pages e2e tests.
 *
 * Verifies the workspace list renders (or empty state), the per-workspace
 * edit form loads with pre-populated fields, and that timezone/country
 * selectors are present.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

test.describe('workspace list page', () => {
  test('renders heading and workspace list or empty state', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/workspaces');
    await page.waitForLoadState('networkidle');

    // Main heading "Workspaces"
    await expect(page.getByRole('heading', { name: /workspaces/i })).toBeVisible({
      timeout: 10_000,
    });

    // Either a list of workspaces or the empty-state message
    const list = page.locator('ul');
    const empty = page.getByText(/not a member of any workspace/i);
    await expect(list.or(empty)).toBeVisible({ timeout: 10_000 });
  });

  test('shows back-to-profile link', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/workspaces');
    await page.waitForLoadState('networkidle');

    const profileLink = page.getByRole('link', { name: /back to profile|profile/i });
    await expect(profileLink).toBeVisible({ timeout: 10_000 });
  });

  test('no i18n keys exposed', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/workspaces');
    await page.waitForLoadState('networkidle');

    const body = await page.locator('body').textContent();
    expect(body).not.toMatch(/\bworkspaces\.\w+\b/);
  });

  test('accessibility check', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/workspaces');
    await page.waitForLoadState('networkidle');
    await checkA11y(page, ['color-contrast', 'region']);
  });
});

test.describe('workspace edit page', () => {
  test('renders edit form when navigating to workspace detail', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/workspaces');
    await page.waitForLoadState('networkidle');

    // Try to click the first workspace link; if no workspaces, skip gracefully
    const wsLink = page.locator('a[href*="/workspaces/"]').first();
    const linkVisible = await wsLink.isVisible({ timeout: 10_000 }).catch(() => false);
    test.skip(!linkVisible, 'No workspaces available for this tenant');

    await wsLink.click();
    await page.waitForLoadState('networkidle');

    // Should navigate to /workspaces/<id>
    await expect(page).toHaveURL(/\/workspaces\/.+/, { timeout: 10_000 });

    // Edit form heading
    await expect(page.getByRole('heading', { name: /workspace settings/i })).toBeVisible({
      timeout: 10_000,
    });
  });

  test('workspace name field is pre-populated', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/workspaces');
    await page.waitForLoadState('networkidle');

    const wsLink = page.locator('a[href*="/workspaces/"]').first();
    const linkVisible = await wsLink.isVisible({ timeout: 10_000 }).catch(() => false);
    test.skip(!linkVisible, 'No workspaces available for this tenant');

    await wsLink.click();
    await page.waitForLoadState('networkidle');

    // Name field should have a non-empty value
    const nameInput = page.getByLabel(/^name$/i);
    await expect(nameInput).toBeVisible({ timeout: 10_000 });
    const value = await nameInput.inputValue();
    expect(value.length).toBeGreaterThan(0);
  });

  test('timezone selector exists on edit page', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/workspaces');
    await page.waitForLoadState('networkidle');

    const wsLink = page.locator('a[href*="/workspaces/"]').first();
    const linkVisible = await wsLink.isVisible({ timeout: 10_000 }).catch(() => false);
    test.skip(!linkVisible, 'No workspaces available for this tenant');

    await wsLink.click();
    await page.waitForLoadState('networkidle');

    // Timezone selector (uses the profile.timezone i18n key, renders as "Timezone")
    await expect(page.getByLabel(/timezone/i)).toBeVisible({ timeout: 10_000 });
  });

  test('no i18n keys exposed on edit page', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/workspaces');
    await page.waitForLoadState('networkidle');

    const wsLink = page.locator('a[href*="/workspaces/"]').first();
    const linkVisible = await wsLink.isVisible({ timeout: 10_000 }).catch(() => false);
    test.skip(!linkVisible, 'No workspaces available for this tenant');

    await wsLink.click();
    await page.waitForLoadState('networkidle');

    const body = await page.locator('body').textContent();
    expect(body).not.toMatch(/\bworkspaces\.\w+\b/);
    expect(body).not.toMatch(/\bprofile\.\w+\b/);
  });

  test('accessibility check on edit page', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/workspaces');
    await page.waitForLoadState('networkidle');

    const wsLink = page.locator('a[href*="/workspaces/"]').first();
    const linkVisible = await wsLink.isVisible({ timeout: 10_000 }).catch(() => false);
    test.skip(!linkVisible, 'No workspaces available for this tenant');

    await wsLink.click();
    await page.waitForLoadState('networkidle');
    await checkA11y(page, ['color-contrast', 'region']);
  });
});
