/**
 * Settings page E2E.
 *
 * Happy path:
 *   1. Create a fresh tenant via REST.
 *   2. Inject auth and navigate to /settings/profile.
 *   3. Verify the profile section is visible.
 *   4. Change the display name and verify it persists.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

test.describe('settings', () => {
  test('view profile section and update display name', async ({ page }) => {
    const { user2: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    await page.goto('/settings/profile');

    // Verify the profile page-level heading is visible. Use level=1 +
    // exact match to avoid matching the avatar's "Profile picture" h3.
    const profileHeading = page.getByRole('heading', {
      level: 1,
      name: /^(profile|プロフィール)$/i,
    });
    await expect(profileHeading).toBeVisible({ timeout: 10_000 });

    // Accessibility check on the settings/profile page
    await checkA11y(page, ['color-contrast', 'region']);

    // Verify the current display name is shown
    const nameInput = page.getByLabel(/display name|表示名/i);
    await expect(nameInput).toBeVisible({ timeout: 5_000 });
    await expect(nameInput).toHaveValue(tenant.displayName);

    // Change display name
    const newName = `Updated Name ${Date.now()}`;
    await nameInput.clear();
    await nameInput.fill(newName);

    // Save the change
    await page.getByRole('button', { name: /save|update|保存/i }).click();

    // Verify success feedback (toast, alert, or field retaining value after reload)
    // Reload to confirm persistence
    await page.reload();
    await expect(page.getByLabel(/display name|表示名/i)).toHaveValue(newName, {
      timeout: 10_000,
    });
  });
});
