/**
 * Personal settings entry points E2E.
 *
 * Profile and security live in accounts-web; flow-web keeps notifications
 * and integrations. These cases pin the redirect, the external account link
 * in the settings sub-nav and the account item in the user menu.
 */

import { expect, test } from '@playwright/test';

import enCommon from '../locales/en/common.json' with { type: 'json' };
import enSettings from '../locales/en/settings.json' with { type: 'json' };
import { cleanupTenant, createTestTenant, injectAuth, type TestTenant } from './fixtures/tenant';

const ACCOUNT_PROFILE_HREF = /^https?:\/\/[^/]+(\/accounts)?\/profile$/;

test.describe('settings', () => {
  let tenant: TestTenant | null = null;
  test.afterEach(async () => {
    if (tenant) await cleanupTenant(tenant);
    tenant = null;
  });

  test('/settings lands on /settings/notifications', async ({ page }) => {
    tenant = await createTestTenant({ locale: 'en' });
    await injectAuth(page.context(), tenant);

    await page.goto('/settings');
    await expect(page).toHaveURL(/\/settings\/notifications$/, { timeout: 15_000 });
    await expect(page.getByRole('heading', { level: 1 })).toHaveText(enSettings.nav.notifications);
  });

  test('sub-nav starts with an external account link, then notifications and integrations', async ({
    page,
  }) => {
    tenant = await createTestTenant({ locale: 'en' });
    await injectAuth(page.context(), tenant);

    await page.goto('/settings/notifications');
    const nav = page.getByRole('navigation', { name: enSettings.settings_sections_label });
    await expect(nav).toBeVisible({ timeout: 15_000 });

    const items = nav.getByRole('link');
    await expect(items).toHaveText([
      enSettings.nav.account,
      enSettings.nav.notifications,
      enSettings.nav.integrations,
    ]);
    await expect(items.first()).toHaveAttribute('href', ACCOUNT_PROFILE_HREF);
    await expect(items.nth(1)).toHaveAttribute('aria-current', 'page');

    await expect(page.getByText(enSettings.nav.account_hint)).toBeVisible();
  });

  test('user menu account item is a link to accounts-web /profile', async ({ page }) => {
    tenant = await createTestTenant({ locale: 'en' });
    await injectAuth(page.context(), tenant);

    await page.goto('/');
    await expect(page.getByRole('main')).toBeVisible({ timeout: 15_000 });
    await page.getByRole('button', { name: enCommon.topbar.user_menu.open }).click();

    const account = page.getByRole('menuitem', { name: enCommon.topbar.user_menu.account });
    await expect(account).toBeVisible();
    expect(await account.evaluate((el) => el.tagName)).toBe('A');
    await expect(account).toHaveAttribute('href', ACCOUNT_PROFILE_HREF);

    const notifications = page.getByRole('menuitem', {
      name: enCommon.topbar.user_menu.notification_settings,
    });
    await notifications.click();
    await expect(page).toHaveURL(/\/settings\/notifications$/, { timeout: 10_000 });
  });

  test('removed /settings/profile and /settings/security routes render not-found', async ({
    page,
  }) => {
    tenant = await createTestTenant({ locale: 'en' });
    await injectAuth(page.context(), tenant);

    for (const path of ['/settings/profile', '/settings/security']) {
      await page.goto(path);
      await expect(page).toHaveURL(new RegExp(`${path}$`));
      await expect(
        page.getByRole('heading', { level: 1, name: enCommon.not_found.title }),
      ).toBeVisible({ timeout: 15_000 });
    }
  });
});
