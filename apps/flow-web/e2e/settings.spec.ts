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

import { type TestTenant, cleanupTenant, createTestTenant, injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

test.describe('settings', () => {
  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('view profile section and update display name', async ({ page }) => {
    tenant = await createTestTenant();
    await injectAuth(page.context(), tenant);

    await page.goto('/settings/profile');

    // Verify the profile section is visible (match English or Japanese heading)
    const profileHeading = page.getByRole('heading', { name: /profile|プロフィール/i });
    await expect(profileHeading).toBeVisible({ timeout: 10_000 });

    // Accessibility check on the settings/profile page
    await checkA11y(page);

    // Verify the current display name is shown
    const nameInput = page.getByLabel(/display name|表示名|name/i);
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
    await expect(page.getByLabel(/display name|表示名|name/i)).toHaveValue(newName, {
      timeout: 10_000,
    });
  });
});
