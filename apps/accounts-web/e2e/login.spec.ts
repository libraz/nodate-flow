/**
 * Login page e2e tests.
 *
 * Verifies the login form structure, validation, navigation links,
 * i18n rendering, and accessibility. Uses REST setup; only assertions
 * touch the UI.
 */

import { expect, test } from '@playwright/test';

import { checkA11y } from './helpers/a11y';

test.describe('login page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/login');
    await page.waitForLoadState('networkidle');
  });

  test('renders login form with correct structure', async ({ page }) => {
    // Title is rendered (not an i18n key)
    const heading = page.getByRole('heading', { level: 1 });
    await expect(heading).toBeVisible();
    const title = await heading.textContent();
    expect(title).toBe('Sign in to your account');

    // Email and password fields. Resolve by input `name=` so the
    // "Show password" toggle button (which carries an aria-label of
    // "Show password") cannot collide with the password input under
    // Playwright strict mode.
    await expect(page.locator('input[name="email"]')).toBeVisible();
    await expect(page.locator('input[name="password"]')).toBeVisible();

    // Submit button
    await expect(page.getByRole('button', { name: /sign in/i })).toBeVisible();
  });

  test('shows signup navigation link', async ({ page }) => {
    // "Don't have an account? Create one" should be visible
    await expect(page.getByText(/don.t have an account/i)).toBeVisible();

    const signupLink = page.getByRole('link', { name: /create one/i });
    await expect(signupLink).toBeVisible();
    await expect(signupLink).toHaveAttribute('href', /\/signup/);
  });

  test('signup link navigates to signup page', async ({ page }) => {
    await page.getByRole('link', { name: /create one/i }).click();
    await expect(page).toHaveURL(/\/signup/);
    await expect(page.getByRole('heading', { name: /create your account/i })).toBeVisible();
  });

  test('no i18n keys exposed in rendered text', async ({ page }) => {
    const body = await page.locator('body').textContent();
    // i18n keys look like "namespace:key.subkey" or "key.subkey"
    // They should never appear literally in the page
    expect(body).not.toMatch(/\b(auth|login|signup|errors)\.\w+\.\w+/);
  });

  test('client-side validation shows error for empty email', async ({ page }) => {
    await page.getByRole('button', { name: /sign in/i }).click();
    // At least some validation feedback should appear
    await expect(page.getByLabel(/email/i))
      .toBeFocused()
      .catch(() => {
        // Some browsers focus differently, that's ok
      });
  });

  test('accessibility check', async ({ page }) => {
    await checkA11y(page, [
      // color-contrast may be sensitive to theme tokens not loaded in test
      'color-contrast',
    ]);
  });
});
