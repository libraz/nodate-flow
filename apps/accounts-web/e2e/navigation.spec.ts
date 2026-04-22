/**
 * Cross-page navigation e2e tests.
 *
 * Verifies that all major navigation paths work end-to-end:
 * public routes (login <-> signup), authenticated routes
 * (profile <-> security), and redirects.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';

test.describe('public route navigation', () => {
  test('login -> signup -> login round-trip', async ({ page }) => {
    await page.goto('/login');
    await page.waitForLoadState('networkidle');

    // Login -> Signup
    await page.getByRole('link', { name: /create one/i }).click();
    await expect(page).toHaveURL(/\/signup/);
    await expect(page.getByRole('heading', { name: /create your account/i })).toBeVisible();

    // Signup -> Login
    await page.getByRole('link', { name: /sign in/i }).click();
    await expect(page).toHaveURL(/\/login/);
    await expect(page.getByRole('heading', { name: /sign in/i })).toBeVisible();
  });

  test('/ redirects to /login', async ({ page }) => {
    await page.goto('/');
    await expect(page).toHaveURL(/\/login/, { timeout: 10_000 });
  });

  test('unauthenticated /profile redirects to login', async ({ page }) => {
    await page.goto('/profile');
    await expect(page).toHaveURL(/\/login/, { timeout: 10_000 });
  });

  test('unauthenticated /admin redirects to login', async ({ page }) => {
    await page.goto('/admin');
    await expect(page).toHaveURL(/\/login/, { timeout: 10_000 });
  });
});

test.describe('authenticated navigation', () => {
  test('profile -> security -> profile', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    await page.goto('/profile');
    await page.waitForLoadState('networkidle');
    await expect(page.getByRole('heading', { name: /profile/i })).toBeVisible();

    // Profile -> Security
    await page.getByRole('link', { name: /security/i }).click();
    await expect(page).toHaveURL(/\/security/);
    await expect(page.getByRole('heading', { name: /security/i })).toBeVisible();

    // Security -> Profile
    await page.getByRole('link', { name: /profile/i }).click();
    await expect(page).toHaveURL(/\/profile/);
  });
});
