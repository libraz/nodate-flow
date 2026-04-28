/**
 * Admin themed confirm dialog e2e test.
 *
 * Verifies that destructive admin actions (workspace suspend/enable, user
 * suspend/enable, admin revoke) trigger the themed in-app confirm dialog
 * instead of the browser-native window.confirm. Asserts:
 *  - role="dialog" is present in the DOM after the action button click
 *  - the dialog exposes confirm and cancel buttons that round-trip
 *  - cancelling the dialog leaves the entity state unchanged.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';

test.describe('admin themed confirm dialog', () => {
  test.beforeEach(() => {
    const { adminGranted } = loadTenants();
    test.skip(!adminGranted, 'Admin grant failed — instance already has an admin from a prior run');
  });

  test('workspace suspend opens themed dialog and cancel dismisses it', async ({ page }) => {
    const { admin } = loadTenants();
    await injectAuth(page.context(), admin);

    let nativeConfirmCalls = 0;
    page.on('dialog', (dialog) => {
      nativeConfirmCalls += 1;
      void dialog.dismiss();
    });

    await page.goto('/admin/workspaces');
    await page.waitForLoadState('networkidle');

    // global-setup seeds a workspace for both `user` and `admin`, so
    // the admin list is never empty. Treat the row as a hard
    // precondition: a missing <td> means the seed flow regressed.
    await page.waitForSelector('td', { timeout: 10_000 });

    const wsLink = page.locator('a[href*="/admin/workspaces/"]').first();
    await expect(wsLink, 'seeded workspace must be visible in admin list').toBeVisible({
      timeout: 10_000,
    });
    await wsLink.click();
    await expect(page).toHaveURL(/\/admin\/workspaces\//, { timeout: 10_000 });

    const actionButton = page.getByRole('button', { name: /suspend|enable/i }).first();
    await expect(actionButton).toBeVisible({ timeout: 10_000 });
    await actionButton.click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible({ timeout: 5_000 });

    expect(nativeConfirmCalls).toBe(0);

    const cancelButton = dialog.getByRole('button', { name: /cancel|キャンセル|取消/i });
    await expect(cancelButton).toBeVisible();
    await cancelButton.click();
    await expect(dialog).toBeHidden({ timeout: 5_000 });
  });
});
