/**
 * Security page e2e tests.
 *
 * Verifies password change form, TOTP section, sessions list,
 * and back navigation to profile.
 */

import { createHmac } from 'node:crypto';

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

/** Decode a (no-padding) base32 string per RFC 4648. */
function decodeBase32(input: string): Buffer {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = 0;
  let value = 0;
  const out: number[] = [];
  for (const ch of input.replace(/=+$/g, '').toUpperCase()) {
    const idx = alphabet.indexOf(ch);
    if (idx < 0) throw new Error(`invalid base32 char: ${ch}`);
    value = (value << 5) | idx;
    bits += 5;
    if (bits >= 8) {
      out.push((value >>> (bits - 8)) & 0xff);
      bits -= 8;
    }
  }
  return Buffer.from(out);
}

/** 6-digit TOTP (HMAC-SHA1, 30 s) for the given base32 secret. */
function totpCode(secretBase32: string, nowMs: number): string {
  const counter = Math.floor(nowMs / 1000 / 30);
  const buf = Buffer.alloc(8);
  buf.writeBigUInt64BE(BigInt(counter));
  const mac = createHmac('sha1', decodeBase32(secretBase32)).update(buf).digest();
  const offset = (mac[mac.length - 1] as number) & 0x0f;
  const bin =
    (((mac[offset] as number) & 0x7f) << 24) |
    ((mac[offset + 1] as number) << 16) |
    ((mac[offset + 2] as number) << 8) |
    (mac[offset + 3] as number);
  return (bin % 1_000_000).toString().padStart(6, '0');
}

async function authedJson<T>(tenant: TestTenant, method: string, path: string, body?: unknown) {
  const res = await fetch(`${AUTH_API_URL}${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  if (!res.ok) throw new Error(`${method} ${path} -> ${res.status} ${await res.text()}`);
  return (await res.json()) as T;
}

test.describe('security page', () => {
  test('renders security sections', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('security');
    await page.waitForLoadState('networkidle');

    // Main heading
    await expect(page.getByRole('heading', { name: /^security$/i })).toBeVisible();

    // Password change section
    await expect(page.getByRole('heading', { name: /change password/i })).toBeVisible();
    await expect(page.getByLabel(/current password/i)).toBeVisible();
    await expect(page.getByLabel(/new password/i)).toBeVisible();

    // TOTP section
    await expect(page.getByRole('heading', { name: /two-factor/i })).toBeVisible();

    // Sessions section
    await expect(page.getByRole('heading', { name: /active sessions/i })).toBeVisible();
  });

  test('back to profile link works', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('security');
    await page.waitForLoadState('networkidle');

    const backLink = page.getByRole('link', { name: /back to profile|profile/i });
    await expect(backLink).toBeVisible();
    await backLink.click();

    await expect(page).toHaveURL(/\/profile/);
  });

  test('no i18n keys exposed', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('security');
    await page.waitForLoadState('networkidle');

    const body = await page.locator('body').textContent();
    expect(body).not.toMatch(/\bsecurity\.\w+\b/);
  });

  test('accessibility check', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    await page.goto('security');
    await page.waitForLoadState('networkidle');
    await checkA11y(page, ['color-contrast']);
  });

  test.describe('with its own account', () => {
    let tenant: TestTenant | null = null;
    test.afterEach(async () => {
      if (tenant) await cleanupTenant(tenant);
      tenant = null;
    });

    test('signs out of all other devices after confirming', async ({ page }) => {
      tenant = await createTestTenant();
      // A second login creates a second session the browser does not own.
      const login = await fetch(`${AUTH_API_URL}/auth/login`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', accept: 'application/json' },
        body: JSON.stringify({ email: tenant.email, password: tenant.password }),
      });
      expect(login.ok, `second login -> ${login.status}`).toBeTruthy();
      await injectAuth(page.context(), tenant);
      await page.goto('security');
      await page.waitForLoadState('networkidle');

      const revokeAll = page.getByRole('button', {
        name: enAuth.security.session_revoke_all,
        exact: true,
      });
      await expect(revokeAll).toBeVisible({ timeout: 10_000 });
      const revokeOne = page.getByRole('button', {
        name: enAuth.security.session_revoke,
        exact: true,
      });
      await expect(revokeOne.first()).toBeVisible();

      // Cancelling the confirmation leaves the other sessions alone.
      await revokeAll.click();
      const dialog = page.getByRole('dialog');
      await expect(dialog.getByText(enAuth.security.session_revoke_all_confirm)).toBeVisible();
      await page.keyboard.press('Escape');
      await expect(dialog).toBeHidden();
      await expect(revokeAll).toBeVisible();

      await revokeAll.click();
      await dialog
        .getByRole('button', { name: enAuth.security.session_revoke_all, exact: true })
        .click();

      await expect(revokeAll).toBeHidden({ timeout: 10_000 });
      await expect(revokeOne).toHaveCount(0);
      await expect(page.getByText(/current/i).first()).toBeVisible();
    });

    test('shows the remaining recovery codes when two-factor is on', async ({ page }) => {
      tenant = await createTestTenant();
      // Sign in first: once two-factor is on, a fresh login stops at the challenge.
      await injectAuth(page.context(), tenant);
      const { secret } = await authedJson<{ secret: string }>(tenant, 'POST', '/me/totp/enroll', {
        password: tenant.password,
      });
      // Confirm with the previous window so a login code stays unused.
      const { recoveryCodes } = await authedJson<{ recoveryCodes: string[] }>(
        tenant,
        'POST',
        '/me/totp/confirm',
        { code: totpCode(secret, Date.now() - 30_000), password: tenant.password },
      );
      expect(recoveryCodes.length).toBeGreaterThan(0);

      await page.goto('security');
      await page.waitForLoadState('networkidle');

      await expect(page.getByTestId('recovery-remaining')).toHaveText(
        enAuth.security.totp.recovery.remaining.replace(
          /\{count, plural, one \{# (.+?)\} other \{# (.+?)\}\}/,
          (_m, one, other) =>
            recoveryCodes.length === 1
              ? `${recoveryCodes.length} ${one}`
              : `${recoveryCodes.length} ${other}`,
        ),
        { timeout: 10_000 },
      );
    });
  });
});
