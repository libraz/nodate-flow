/**
 * Profile save e2e tests.
 *
 * Verifies that editing the display name on the profile page persists
 * across page reloads. Uses the dedicated `user2` tenant to avoid
 * conflicts with other tests that assert on user1's display name.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

test.describe('profile save', () => {
  test('changing display name persists after reload', async ({ page }) => {
    const { user2: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/profile');
    await page.waitForLoadState('networkidle');

    // Wait for the form to be populated
    const nameInput = page.getByLabel(/display name/i);
    await expect(nameInput).toBeVisible({ timeout: 10_000 });

    // Generate a unique name to avoid collisions
    const uniqueName = `E2E Updated ${Date.now().toString(36)}`;

    // Clear and type the new name
    await nameInput.clear();
    await nameInput.fill(uniqueName);

    // The save button should become enabled after editing
    const saveButton = page.getByRole('button', { name: /save/i });
    await expect(saveButton).toBeEnabled({ timeout: 10_000 });

    // Click save
    await saveButton.click();

    // Wait for success feedback. Scope to the form's live-region output: a
    // separate empty <output> exists inside #nf-toast-root as the toast
    // viewport, which would otherwise trigger a strict-mode violation.
    const successMessage = page
      .locator('output')
      .filter({ hasText: /Profile updated successfully\.|プロフィールを更新しました。/ });
    await expect(successMessage).toBeVisible({ timeout: 10_000 });

    // Reload the page to verify persistence
    await page.reload();
    await page.waitForLoadState('networkidle');

    // The name field should show the updated value
    const reloadedInput = page.getByLabel(/display name/i);
    await expect(reloadedInput).toBeVisible({ timeout: 10_000 });
    await expect(reloadedInput).toHaveValue(uniqueName, { timeout: 10_000 });
  });

  test('accessibility check on profile page', async ({ page }) => {
    const { user2: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/profile');
    await page.waitForLoadState('networkidle');
    await checkA11y(page, ['color-contrast', 'region']);
  });
});
