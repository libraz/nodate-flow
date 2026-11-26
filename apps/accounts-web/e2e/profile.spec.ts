/**
 * Profile page e2e tests.
 *
 * Verifies the authenticated profile form renders correctly with
 * user data, theme/locale selectors work, and security link navigates.
 */

import { expect, test } from '@playwright/test';

import enAuth from '../locales/en/auth.json' with { type: 'json' };
import { loadTenants } from './fixtures/load-tenants';
import {
  AUTH_API_URL,
  cleanupTenant,
  createTestTenant,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

test.describe('profile page', () => {
  test('renders profile form with user data', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('profile');
    await page.waitForLoadState('networkidle');

    // Heading
    await expect(page.getByRole('heading', { level: 1, name: /^profile$/i })).toBeVisible();

    // Display name field should be pre-filled
    const nameInput = page.getByLabel(/display name/i);
    await expect(nameInput).toBeVisible();
    await expect(nameInput).toHaveValue(tenant.displayName);

    // Language selector
    await expect(page.getByLabel(/language/i)).toBeVisible();

    // Theme family and colour mode controls
    await expect(page.getByRole('radiogroup', { name: enAuth.profile.theme })).toBeVisible();
    await expect(page.getByRole('radiogroup', { name: enAuth.profile.color_mode })).toBeVisible();

    // Save button
    await expect(page.getByRole('button', { name: /save/i })).toBeVisible();
  });

  test('theme controls offer every family and the three colour modes', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('profile');
    await page.waitForLoadState('networkidle');

    const p = enAuth.profile;
    await expect(page.getByRole('radiogroup', { name: p.theme }).getByRole('radio')).toHaveText([
      p.theme_glass,
      p.theme_modern,
      p.theme_washi,
      p.theme_sakura,
      p.theme_classic,
      p.theme_nothing,
    ]);
    await expect(
      page.getByRole('radiogroup', { name: p.color_mode }).getByRole('radio'),
    ).toHaveText([p.color_mode_light, p.color_mode_dark, p.color_mode_system]);
  });

  test('security link navigates to security page', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('profile');
    await page.waitForLoadState('networkidle');

    const secLink = page.getByRole('link', { name: /security/i });
    await expect(secLink).toBeVisible();
    await secLink.click();

    await expect(page).toHaveURL(/\/security/);
    await expect(page.getByRole('heading', { name: /security/i })).toBeVisible();
  });

  test('no i18n keys exposed', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('profile');
    await page.waitForLoadState('networkidle');

    const body = await page.locator('body').textContent();
    expect(body).not.toMatch(/\bprofile\.\w+\b/);
  });

  test('accessibility check', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('profile');
    await page.waitForLoadState('networkidle');
    await checkA11y(page, ['color-contrast']);
  });

  test.describe('with its own account', () => {
    let tenant: TestTenant | null = null;
    test.afterEach(async () => {
      if (tenant) await cleanupTenant(tenant);
      tenant = null;
    });

    test('uploads and removes the profile picture', async ({ page }) => {
      tenant = await createTestTenant();
      await injectAuth(page.context(), tenant);
      await page.goto('profile');
      await page.waitForLoadState('networkidle');

      const a = enAuth.profile.avatar;
      const alt = a.alt.replace('{name}', tenant.displayName);
      await expect(page.getByRole('button', { name: a.upload_aria })).toBeVisible();
      await expect(page.getByRole('img', { name: alt })).toHaveCount(0);

      // 1x1 transparent PNG.
      const png = Buffer.from(
        'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAwS2OUAAAAABJRU5ErkJggg==',
        'base64',
      );
      await page
        .locator('input[type="file"]')
        .setInputFiles({ name: 'avatar.png', mimeType: 'image/png', buffer: png });

      await expect(page.getByText(a.updated)).toBeVisible({ timeout: 15_000 });
      await expect(page.getByRole('button', { name: a.replace_aria })).toBeVisible();

      // The server now serves an avatar for this user.
      const me = await fetch(`${AUTH_API_URL}/me`, {
        headers: { accept: 'application/json', authorization: `Bearer ${tenant.accessToken}` },
      });
      expect(((await me.json()) as { avatarUrl?: string | null }).avatarUrl).toBeTruthy();

      await page.getByRole('button', { name: a.remove, exact: true }).click();
      const dialog = page.getByRole('dialog');
      await expect(dialog.getByText(a.remove_confirm)).toBeVisible();
      await dialog.getByRole('button', { name: a.remove, exact: true }).click();

      await expect(page.getByText(a.removed)).toBeVisible({ timeout: 15_000 });
      await expect(page.getByRole('button', { name: a.upload_aria })).toBeVisible();
      await expect(page.getByRole('button', { name: a.remove, exact: true })).toHaveCount(0);

      const after = await fetch(`${AUTH_API_URL}/me`, {
        headers: { accept: 'application/json', authorization: `Bearer ${tenant.accessToken}` },
      });
      expect(((await after.json()) as { avatarUrl?: string | null }).avatarUrl).toBeFalsy();
    });

    test('theme family and colour mode change data-theme and persist to the account', async ({
      page,
    }) => {
      tenant = await createTestTenant();
      await injectAuth(page.context(), tenant);
      await page.goto('profile');
      await page.waitForLoadState('networkidle');

      const p = enAuth.profile;
      await page
        .getByRole('radiogroup', { name: p.theme })
        .getByRole('radio', { name: p.theme_washi, exact: true })
        .click();
      await page
        .getByRole('radiogroup', { name: p.color_mode })
        .getByRole('radio', { name: p.color_mode_dark, exact: true })
        .click();
      await expect(page.locator('html')).toHaveAttribute('data-theme', 'washi-dark', {
        timeout: 5_000,
      });

      await expect
        .poll(
          async () => {
            const res = await fetch(`${AUTH_API_URL}/me`, {
              headers: {
                accept: 'application/json',
                authorization: `Bearer ${tenant?.accessToken}`,
              },
            });
            return ((await res.json()) as { themePreference?: string }).themePreference;
          },
          { timeout: 10_000 },
        )
        .toBe('washi-dark');

      await page
        .getByRole('radiogroup', { name: p.color_mode })
        .getByRole('radio', { name: p.color_mode_light, exact: true })
        .click();
      await expect(page.locator('html')).toHaveAttribute('data-theme', 'washi-light', {
        timeout: 5_000,
      });

      // The pick survives a reload and the controls reflect it.
      await page.reload();
      await page.waitForLoadState('networkidle');
      await expect(page.locator('html')).toHaveAttribute('data-theme', 'washi-light');
      await expect(
        page
          .getByRole('radiogroup', { name: p.theme })
          .getByRole('radio', { name: p.theme_washi, exact: true }),
      ).toBeChecked();
      await expect(
        page
          .getByRole('radiogroup', { name: p.color_mode })
          .getByRole('radio', { name: p.color_mode_light, exact: true }),
      ).toBeChecked();
    });
  });
});
