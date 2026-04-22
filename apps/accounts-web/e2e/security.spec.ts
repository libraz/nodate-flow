/**
 * Security page e2e tests.
 *
 * Verifies password change form, TOTP section, sessions list,
 * and back navigation to profile.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

test.describe('security page', () => {
  test('renders security sections', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/security');
    await page.waitForLoadState('networkidle');

    // Main heading
    await expect(page.getByRole('heading', { name: /^security$/i })).toBeVisible();

    // Password change section
    await expect(page.getByRole('heading', { name: /change password/i })).toBeVisible();
    await expect(page.getByLabel(/current password/i)).toBeVisible();
    await expect(page.getByLabel(/new password/i)).toBeVisible();

    // TOTP section
    await expect(page.getByRole('heading', { name: /two-factor/i })).toBeVisible();

    // Sessions section
    await expect(page.getByRole('heading', { name: /active sessions/i })).toBeVisible();
  });

  test('back to profile link works', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/security');
    await page.waitForLoadState('networkidle');

    const backLink = page.getByRole('link', { name: /back to profile|profile/i });
    await expect(backLink).toBeVisible();
    await backLink.click();

    await expect(page).toHaveURL(/\/profile/);
  });

  test('no i18n keys exposed', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/security');
    await page.waitForLoadState('networkidle');

    const body = await page.locator('body').textContent();
    expect(body).not.toMatch(/\bsecurity\.\w+\b/);
  });

  test('accessibility check', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/security');
    await page.waitForLoadState('networkidle');
    await checkA11y(page, ['color-contrast']);
  });
});
