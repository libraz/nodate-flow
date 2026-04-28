/**
 * Login rate-limit countdown banner e2e (F3).
 *
 * Mocks the `/auth/login` POST with a 429 response carrying a
 * `Retry-After: 60` header and an RFC 7807 problem body that maps to
 * `auth:errors.rate_limited`. Verifies that:
 *   - the cooldown banner appears with a formatted MM:SS countdown,
 *   - the email + password inputs become disabled,
 *   - the submit button becomes disabled.
 */

import { expect, test } from '@playwright/test';

test.describe('login rate-limit countdown', () => {
  test('renders banner and disables inputs after a 429', async ({ page }) => {
    // Stub the login endpoint with a 429. The auth-api would normally
    // tag the body with the canonical AUTH.LOGIN.RATE_LIMITED_AFTER_RETRIES
    // type; we mirror that here so `mapAuthError` resolves the right copy.
    //
    // The browser would normally hide `Retry-After` from cross-origin JS
    // unless the server lists it under `Access-Control-Expose-Headers`.
    // We mirror that contract here so `response.headers.get('Retry-After')`
    // resolves the value the same way it does in production.
    await page.route('**/auth/login', async (route) => {
      await route.fulfill({
        status: 429,
        headers: {
          'retry-after': '60',
          'access-control-expose-headers': 'retry-after',
        },
        contentType: 'application/problem+json',
        body: JSON.stringify({
          type: 'https://nodate-flow.dev/errors/AUTH.LOGIN.RATE_LIMITED_AFTER_RETRIES',
          title: 'Too many failed login attempts',
          status: 429,
          detail: 'Too many failed login attempts',
        }),
      });
    });

    await page.goto('/login');
    await page.waitForLoadState('networkidle');

    await page.locator('input[name="email"]').fill('user@example.test');
    await page.locator('input[name="password"]').fill('correct horse battery staple');
    await page.getByRole('button', { name: /sign in/i }).click();

    // Banner appears with a MM:SS-formatted countdown.
    const banner = page.getByTestId('login-rate-limit-banner');
    await expect(banner).toBeVisible();
    await expect(banner).toContainText(/01:00|0?1:0?0/);

    // Inputs and submit are disabled while the cooldown is active.
    await expect(page.locator('input[name="email"]')).toBeDisabled();
    await expect(page.locator('input[name="password"]')).toBeDisabled();
    await expect(page.getByRole('button', { name: /sign in/i })).toBeDisabled();
  });
});
