/**
 * Admin user-detail action mutations e2e (G9).
 *
 * The previous admin spec only covered the *cancel* path of the themed
 * confirm dialog. This spec drives the full mutation lifecycle on a
 * non-admin tenant so the user-detail page's destructive actions are
 * exercised end to end:
 *
 *   - suspend → confirm → status badge flips Active → Suspended
 *   - enable  → confirm → status badge flips Suspended → Active
 *   - grant admin (no confirm) → admin field flips No → Yes
 *   - revoke admin → confirm → admin field flips Yes → No
 *
 * Targets the shared `user2` tenant (a freshly registered, non-admin
 * user) so the mutations cannot affect the admin tenant's own privileges
 * — and so re-runs land deterministically because the seeded developer
 * admin (admin@example.com) is never a subject of these mutations.
 *
 * Skipped when `adminGranted` is false — the admin can only mutate
 * other users when itself an instance admin.
 */

import { expect, test } from '@playwright/test';

import enAdmin from '../locales/en/admin.json' with { type: 'json' };
import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';

const copy = {
  detailHeading: enAdmin.users.detail,
  status: enAdmin.users.status,
  admin: enAdmin.users.admin,
  enabled: enAdmin.users.enabled,
  disabled: enAdmin.users.disabled,
  yes: enAdmin.common.yes,
  no: enAdmin.common.no,
  suspend: enAdmin.users.suspend,
  enable: enAdmin.users.enable,
  grant: enAdmin.users.grant_admin,
  revoke: enAdmin.users.revoke_admin,
} as const;

test.describe('admin user-detail action mutations', () => {
  test.beforeEach(() => {
    const { adminGranted } = loadTenants();
    test.skip(!adminGranted, 'Admin grant failed — instance already has an admin from a prior run');
  });

  test('suspend then re-enable flips the status badge', async ({ page }) => {
    const { admin, user2 } = loadTenants();
    await injectAuth(page.context(), admin);

    await page.goto(`/admin/users/${user2.userId}`);
    await expect(page.getByRole('heading', { name: copy.detailHeading })).toBeVisible({
      timeout: 10_000,
    });

    // The status badge sits next to the "Status" label. Initially Active.
    const statusBadge = page.locator('span').filter({ hasText: new RegExp(`^${copy.enabled}$`) });
    await expect(statusBadge).toBeVisible({ timeout: 10_000 });

    // Suspend → themed confirm dialog → confirm.
    await page.getByRole('button', { name: copy.suspend, exact: true }).click();
    const confirmDialog = page.getByRole('dialog');
    await expect(confirmDialog).toBeVisible({ timeout: 5_000 });
    await confirmDialog.getByRole('button', { name: new RegExp(`^${copy.suspend}$`, 'i') }).click();
    await expect(confirmDialog).toBeHidden({ timeout: 5_000 });

    // After mutation + refetch the badge text flips to "Suspended".
    // We assert the suspended badge appears AND the original Active
    // badge is gone — proves the row actually replaced its status
    // rather than additively rendering both labels.
    const suspendedBadge = page
      .locator('span')
      .filter({ hasText: new RegExp(`^${copy.disabled}$`) });
    await expect(suspendedBadge).toBeVisible({ timeout: 10_000 });
    await expect(statusBadge).toBeHidden();
    // The action button label must also have flipped to "Enable" so a
    // user landing on the page now sees the inverse mutation affordance.
    await expect(page.getByRole('button', { name: copy.enable, exact: true })).toBeVisible({
      timeout: 10_000,
    });

    // Click Enable → confirm → back to Active.
    await page.getByRole('button', { name: copy.enable, exact: true }).click();
    await expect(confirmDialog).toBeVisible({ timeout: 5_000 });
    await confirmDialog.getByRole('button', { name: new RegExp(`^${copy.enable}$`, 'i') }).click();
    await expect(confirmDialog).toBeHidden({ timeout: 5_000 });
    // Both directions of the status flip are verified now: Active
    // returns and Suspended is gone.
    await expect(statusBadge).toBeVisible({ timeout: 10_000 });
    await expect(suspendedBadge).toBeHidden();
    // And the destructive "Suspend" affordance is back.
    await expect(page.getByRole('button', { name: copy.suspend, exact: true })).toBeVisible({
      timeout: 10_000,
    });
  });

  test('grant then revoke admin toggles the admin field', async ({ page }) => {
    const { admin, user2 } = loadTenants();
    await injectAuth(page.context(), admin);

    await page.goto(`/admin/users/${user2.userId}`);
    await expect(page.getByRole('heading', { name: copy.detailHeading })).toBeVisible({
      timeout: 10_000,
    });

    // The Admin row shows "No" before the grant.
    const adminLabel = page
      .locator('div')
      .filter({ hasText: new RegExp(`^${copy.admin}$`) })
      .first();
    await expect(adminLabel).toBeVisible({ timeout: 10_000 });
    // Reading the field's value is fragile (the value div is just text);
    // assert via the action button label which reads "Grant Admin" while
    // the user is not yet admin.
    await expect(page.getByRole('button', { name: copy.grant, exact: true })).toBeVisible();

    // Grant admin — no confirm dialog by design (additive, reversible).
    await page.getByRole('button', { name: copy.grant, exact: true }).click();

    // The button label flips to "Revoke Admin" once the refetch lands.
    await expect(page.getByRole('button', { name: copy.revoke, exact: true })).toBeVisible({
      timeout: 10_000,
    });

    // Revoke admin — destructive, so the themed confirm dialog appears.
    await page.getByRole('button', { name: copy.revoke, exact: true }).click();
    const confirmDialog = page.getByRole('dialog');
    await expect(confirmDialog).toBeVisible({ timeout: 5_000 });
    await confirmDialog.getByRole('button', { name: /revoke|取り消し|撤销/i }).click();
    await expect(confirmDialog).toBeHidden({ timeout: 5_000 });

    // Back to "Grant Admin" once the revoke + refetch lands.
    await expect(page.getByRole('button', { name: copy.grant, exact: true })).toBeVisible({
      timeout: 10_000,
    });
  });
});
