/**
 * Profile page e2e tests.
 *
 * Verifies the authenticated profile form renders correctly with
 * user data, theme/locale selectors work, and security link navigates.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

test.describe('profile page', () => {
  test('renders profile form with user data', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/profile');
    await page.waitForLoadState('networkidle');

    // Heading
    await expect(page.getByRole('heading', { name: /profile/i })).toBeVisible();

    // Display name field should be pre-filled
    const nameInput = page.getByLabel(/display name/i);
    await expect(nameInput).toBeVisible();
    await expect(nameInput).toHaveValue(tenant.displayName);

    // Language selector
    await expect(page.getByLabel(/language/i)).toBeVisible();

    // Theme selector
    await expect(page.getByLabel(/theme/i)).toBeVisible();

    // Save button
    await expect(page.getByRole('button', { name: /save/i })).toBeVisible();
  });

  test('theme selector has all expected options', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/profile');
    await page.waitForLoadState('networkidle');

    const themeSelect = page.getByLabel(/theme/i);
    const options = themeSelect.locator('option');
    const values = await options.allTextContents();

    expect(values).toContain('System');
    expect(values).toContain('Aurora Light');
    expect(values).toContain('Aurora Dark');
    expect(values).toContain('Glass Light');
    expect(values).toContain('Glass Dark');
  });

  test('security link navigates to security page', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/profile');
    await page.waitForLoadState('networkidle');

    const secLink = page.getByRole('link', { name: /security/i });
    await expect(secLink).toBeVisible();
    await secLink.click();

    await expect(page).toHaveURL(/\/security/);
    await expect(page.getByRole('heading', { name: /security/i })).toBeVisible();
  });

  test('no i18n keys exposed', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/profile');
    await page.waitForLoadState('networkidle');

    const body = await page.locator('body').textContent();
    expect(body).not.toMatch(/\bprofile\.\w+\b/);
  });

  test('accessibility check', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/profile');
    await page.waitForLoadState('networkidle');
    await checkA11y(page, ['color-contrast']);
  });
});
