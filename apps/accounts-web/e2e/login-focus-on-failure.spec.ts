/**
 * Login focus-on-failure e2e (F4).
 *
 * Verifies that:
 *   - submitting an empty form lands focus on the email input (the
 *     first invalid field),
 *   - submitting valid credentials against a stubbed 401 lands focus
 *     on the alert region rendered above the submit button.
 */

import { expect, test } from '@playwright/test';

test.describe('login focus management on failure', () => {
  test('focus moves to the email input when the form is submitted empty', async ({ page }) => {
    await page.goto('/login');
    await page.waitForLoadState('networkidle');

    // Click the submit button without filling anything.
    await page.getByRole('button', { name: /sign in/i }).click();

    await expect(page.getByLabel(/email/i)).toBeFocused();
  });

  test('focus moves to the alert region when the server rejects credentials', async ({ page }) => {
    await page.route('**/auth/login', async (route) => {
      await route.fulfill({
        status: 401,
        contentType: 'application/problem+json',
        body: JSON.stringify({
          type: 'https://nodate-flow.dev/errors/AUTH.LOGIN.INVALID_CREDENTIALS',
          title: 'Invalid email or password',
          status: 401,
          detail: 'Invalid email or password',
        }),
      });
    });

    await page.goto('/login');
    await page.waitForLoadState('networkidle');

    await page.locator('input[name="email"]').fill('user@example.test');
    await page.locator('input[name="password"]').fill('correct horse battery staple');
    await page.getByRole('button', { name: /sign in/i }).click();

    const alert = page.getByTestId('login-server-error');
    await expect(alert).toBeVisible();
    await expect(alert).toBeFocused();
  });
});
