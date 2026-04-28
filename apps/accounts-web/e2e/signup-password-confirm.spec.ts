/**
 * Signup password confirmation field — Playwright coverage.
 *
 * Asserts that submitting the form with two non-matching password values
 * surfaces the inline `Passwords do not match.` error against the
 * confirmation field, without firing a server request.
 */

import { expect, test } from '@playwright/test';

test.describe('signup password confirmation', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/signup');
    await page.waitForLoadState('networkidle');
  });

  test('shows inline mismatch error when passwords differ', async ({ page }) => {
    await page.getByLabel(/display name|name/i).fill('Mismatch User');
    await page.getByLabel(/email/i).fill(`mismatch-${Date.now().toString(36)}@example.test`);

    // Resolve each password field by the underlying input name so the
    // sibling "Show password" toggle button does not contaminate the
    // selector pool. RHF binds inputs by `name=...` so this stays stable.
    await page.locator('input[name="password"]').fill('correct horse battery staple');
    await page.locator('input[name="newPasswordConfirm"]').fill('a different secret');

    await page.getByRole('button', { name: /create account/i }).click();

    // Inline error should mention the mismatch copy.
    await expect(page.getByText(/passwords do not match/i)).toBeVisible();

    // We should not have navigated away.
    await expect(page).toHaveURL(/\/signup/);
  });
});
