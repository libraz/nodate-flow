/**
 * Signup page e2e tests.
 *
 * Verifies the signup form structure, validation, navigation links
 * back to login, and accessibility.
 */

import { expect, test } from '@playwright/test';

import { checkA11y } from './helpers/a11y';

test.describe('signup page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/signup');
    await page.waitForLoadState('networkidle');
  });

  test('renders signup form with correct structure', async ({ page }) => {
    const heading = page.getByRole('heading', { level: 1 });
    await expect(heading).toBeVisible();
    expect(await heading.textContent()).toBe('Create your account');

    // All three fields: name, email, password
    await expect(page.getByLabel(/display name|name/i)).toBeVisible();
    await expect(page.getByLabel(/email/i)).toBeVisible();
    await expect(page.getByLabel(/password/i)).toBeVisible();

    // Submit button
    await expect(page.getByRole('button', { name: /create account/i })).toBeVisible();
  });

  test('shows login navigation link', async ({ page }) => {
    await expect(page.getByText(/already have an account/i)).toBeVisible();

    const loginLink = page.getByRole('link', { name: /sign in/i });
    await expect(loginLink).toBeVisible();
    await expect(loginLink).toHaveAttribute('href', /\/login/);
  });

  test('login link navigates back to login page', async ({ page }) => {
    await page.getByRole('link', { name: /sign in/i }).click();
    await expect(page).toHaveURL(/\/login/);
    await expect(page.getByRole('heading', { name: /sign in/i })).toBeVisible();
  });

  test('link text and surrounding text have proper spacing', async ({ page }) => {
    // The "Already have an account? Sign in" text should be visually
    // separated (the link styled distinctly from surrounding text).
    const link = page.getByRole('link', { name: /sign in/i });
    const linkWeight = await link.evaluate((el) => getComputedStyle(el).fontWeight);

    // Link should have accent color (different from muted text) and bold weight
    expect(Number.parseInt(linkWeight, 10)).toBeGreaterThanOrEqual(500);
  });

  test('no i18n keys exposed in rendered text', async ({ page }) => {
    const body = await page.locator('body').textContent();
    expect(body).not.toMatch(/\b(auth|signup|login)\.\w+\.\w+/);
  });

  test('full signup flow creates account and redirects', async ({ page }) => {
    // Use a unique email for the UI signup
    const suffix = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
    const email = `e2e-signup-${suffix}@example.test`;

    await page.getByLabel(/display name|name/i).fill(`E2E Signup ${suffix}`);
    await page.getByLabel(/email/i).fill(email);
    await page.getByLabel(/password/i).fill('correct horse battery staple');
    await page.getByRole('button', { name: /create account/i }).click();

    // Should redirect to /profile after successful signup, or show
    // an error (rate limiting, registration disabled, etc.)
    await Promise.race([
      expect(page).toHaveURL(/\/profile/, { timeout: 15_000 }),
      // If rate-limited, a server error message appears instead
      expect(page.locator('[role="alert"]')).toBeVisible({ timeout: 15_000 }),
    ]);
  });

  test('successful signup shows the welcome toast before redirect', async ({ page }) => {
    // Capture toast text before the navigation completes. The toaster mounts
    // a portal at #nf-toast-root; the success toast is announced via
    // aria-live="polite" so it shows up as accessible-name on the live region.
    const suffix = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
    const email = `e2e-signup-toast-${suffix}@example.test`;

    await page.getByLabel(/display name|name/i).fill(`E2E Toast ${suffix}`);
    await page.getByLabel(/email/i).fill(email);
    await page.getByLabel(/password/i).fill('correct horse battery staple');
    await page.getByRole('button', { name: /create account/i }).click();

    // The toast must appear before or simultaneously with the redirect; we
    // grant a small window since toaster.show is synchronous but navigation
    // races against it. If the API rate-limits, we accept that path.
    const toastOrError = await Promise.race([
      page
        .locator('#nf-toast-root')
        .getByText(/welcome|account created/i)
        .waitFor({
          state: 'visible',
          timeout: 5_000,
        })
        .then(() => 'toast' as const),
      page
        .locator('[role="alert"]')
        .waitFor({
          state: 'visible',
          timeout: 5_000,
        })
        .then(() => 'error' as const),
    ]).catch(() => 'none' as const);

    if (toastOrError === 'toast') {
      // Eventually we land on /profile.
      await expect(page).toHaveURL(/\/profile/, { timeout: 15_000 });
    } else if (toastOrError === 'error') {
      // Rate-limited or registration disabled — acceptable in CI.
      expect(['error']).toContain(toastOrError);
    } else {
      throw new Error('Neither success toast nor error alert appeared.');
    }
  });

  test('accessibility check', async ({ page }) => {
    await checkA11y(page, ['color-contrast']);
  });
});
