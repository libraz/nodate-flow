/**
 * Security page interaction e2e tests.
 *
 * Verifies UI elements and interactive states on the security page:
 * password change form fields, TOTP section toggle button, and active
 * sessions list. Does NOT mutate state (no actual password change or
 * TOTP enrollment) to avoid breaking other parallel tests.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

test.describe('security page interactions', () => {
  test('password change form has current and new password fields', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/security');
    await page.waitForLoadState('networkidle');

    // Password section heading
    await expect(page.getByRole('heading', { name: /change password/i })).toBeVisible({
      timeout: 10_000,
    });

    // Current password field
    const currentPw = page.getByLabel(/current password/i);
    await expect(currentPw).toBeVisible({ timeout: 10_000 });
    expect(await currentPw.getAttribute('type')).toBe('password');

    // New password field
    const newPw = page.getByLabel(/new password/i);
    await expect(newPw).toBeVisible({ timeout: 10_000 });
    expect(await newPw.getAttribute('type')).toBe('password');

    // Submit button for password change
    const submitButtons = page.getByRole('button', { name: /change password/i });
    await expect(submitButtons.first()).toBeVisible({ timeout: 10_000 });
  });

  test('TOTP section shows an action button for the current status', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/security');
    await page.waitForLoadState('networkidle');

    // TOTP heading
    await expect(page.getByRole('heading', { name: /two-factor/i })).toBeVisible({
      timeout: 10_000,
    });

    // The TOTP section renders one of three action buttons depending on the
    // server-side status: `security.totp.start_setup` ("Start setup" / "設定を開始")
    // when disabled, `security.totp.resume_restart` ("Restart setup" /
    // "最初からやり直す") when a pending enrollment exists, or
    // `security.totp.disable` ("Disable 2FA" / "2FAを無効化") when enabled. Match
    // any of them to stay resilient against the tenant's initial state.
    const totpButton = page.getByRole('button', {
      name: /Start setup|Restart setup|Disable 2FA|設定を開始|最初からやり直す|2FAを無効化/,
    });
    await expect(totpButton.first()).toBeVisible({ timeout: 10_000 });

    const text = (await totpButton.first().textContent())?.trim() ?? '';
    expect(text.length).toBeGreaterThan(0);
  });

  test('active sessions section shows at least one session', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/security');
    await page.waitForLoadState('networkidle');

    // Sessions heading
    await expect(page.getByRole('heading', { name: /active sessions/i })).toBeVisible({
      timeout: 10_000,
    });

    // Wait for sessions to load. We expect at least the current session
    // to be visible, marked with "(current)" or similar indicator.
    await expect(page.getByText(/current/i).first()).toBeVisible({ timeout: 10_000 });
  });

  test('no i18n keys exposed', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/security');
    await page.waitForLoadState('networkidle');

    // Wait for all sections to finish loading. The TOTP action button is the
    // last piece of async content to render; see the TOTP test above for the
    // three possible button labels (en + ja).
    await page
      .getByRole('button', {
        name: /Start setup|Restart setup|Disable 2FA|設定を開始|最初からやり直す|2FAを無効化/,
      })
      .first()
      .waitFor({ timeout: 10_000 });

    const body = await page.locator('body').textContent();
    expect(body).not.toMatch(/\bsecurity\.\w+\b/);
  });

  test('accessibility check', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('/security');
    await page.waitForLoadState('networkidle');

    // Wait for async content to settle. See the TOTP test above for why
    // we match three possible button labels across en + ja.
    await page
      .getByRole('button', {
        name: /Start setup|Restart setup|Disable 2FA|設定を開始|最初からやり直す|2FAを無効化/,
      })
      .first()
      .waitFor({ timeout: 10_000 });

    await checkA11y(page, ['color-contrast', 'region']);
  });
});
