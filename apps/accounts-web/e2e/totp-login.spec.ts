/**
 * TOTP login challenge e2e (G17).
 *
 * Drives the full enroll → log out → log in → TOTP challenge → success
 * loop on a freshly created tenant:
 *
 *   1. Register a new user via auth-api.
 *   2. Enroll TOTP via REST: POST /me/totp/enroll returns the secret;
 *      POST /me/totp/confirm with a code computed from that secret
 *      flips MFA into the "confirmed" state and emits recovery codes.
 *   3. Log out (so the next /auth/login starts a fresh session).
 *   4. Visit /login and submit email + password — server returns
 *      `{ step: 'totp_required', challengeToken }` and the UI swaps the
 *      form for the TOTP card.
 *   5. Compute the current TOTP code locally and submit it via the UI.
 *   6. Assert the SPA navigates to /profile (the post-login default).
 *
 * Recovery-code path is exercised in a sibling case using the codes
 * issued by `confirm`.
 *
 * The TOTP algorithm is implemented in Node here (HMAC-SHA1, 30s period,
 * 6 digits) so we don't need a dedicated dep — it mirrors the
 * authoritative implementation in `packages/go-shared/authn/totp.go`.
 */

import { createHmac } from 'node:crypto';

import { expect, test } from '@playwright/test';

import enAuth from '../locales/en/auth.json' with { type: 'json' };
import { AUTH_API_URL, createTestTenant } from './fixtures/tenant';

/** Decode a (no-padding) base32 string per RFC 4648. */
function decodeBase32(input: string): Buffer {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  const cleaned = input.replace(/=+$/g, '').toUpperCase();
  let bits = 0;
  let value = 0;
  const out: number[] = [];
  for (const ch of cleaned) {
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

/** Compute the current 6-digit TOTP code for the given base32 secret. */
function totpCode(secretBase32: string, nowMs: number = Date.now()): string {
  const secret = decodeBase32(secretBase32);
  const counter = Math.floor(nowMs / 1000 / 30);
  const buf = Buffer.alloc(8);
  buf.writeBigUInt64BE(BigInt(counter));
  const mac = createHmac('sha1', secret).update(buf).digest();
  const offset = mac[mac.length - 1] & 0x0f;
  const bin =
    ((mac[offset] & 0x7f) << 24) |
    (mac[offset + 1] << 16) |
    (mac[offset + 2] << 8) |
    mac[offset + 3];
  return (bin % 1_000_000).toString().padStart(6, '0');
}

interface EnrollOutput {
  otpauthUrl: string;
  secret: string;
}

interface ConfirmOutput {
  ok: boolean;
  recoveryCodes: string[];
}

async function enrollTotp(accessToken: string): Promise<EnrollOutput> {
  const res = await fetch(`${AUTH_API_URL}/me/totp/enroll`, {
    method: 'POST',
    headers: { authorization: `Bearer ${accessToken}`, accept: 'application/json' },
  });
  if (!res.ok) throw new Error(`POST /me/totp/enroll -> ${res.status} ${await res.text()}`);
  return (await res.json()) as EnrollOutput;
}

async function confirmTotp(accessToken: string, code: string): Promise<ConfirmOutput> {
  const res = await fetch(`${AUTH_API_URL}/me/totp/confirm`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      accept: 'application/json',
      authorization: `Bearer ${accessToken}`,
    },
    body: JSON.stringify({ code }),
  });
  if (!res.ok) throw new Error(`POST /me/totp/confirm -> ${res.status} ${await res.text()}`);
  return (await res.json()) as ConfirmOutput;
}

const copy = {
  email: enAuth.login.email,
  signIn: enAuth.login.submit,
  totpTitle: enAuth.login.totp_title,
  totpSubmit: enAuth.login.totp_submit,
  totpUseRecovery: enAuth.login.totp_use_recovery,
  recoverySubmit: enAuth.login.recovery_submit,
} as const;

test.describe('TOTP login', () => {
  test('challenges with the TOTP card after password and accepts a valid code', async ({
    page,
  }) => {
    const tenant = await createTestTenant();
    const enroll = await enrollTotp(tenant.accessToken);
    await confirmTotp(tenant.accessToken, totpCode(enroll.secret));

    // Drop the bearer-token session that confirm() left active so the
    // next login starts clean.
    await fetch(`${AUTH_API_URL}/auth/logout`, {
      method: 'POST',
      headers: { authorization: `Bearer ${tenant.accessToken}` },
    });

    await page.goto('/login');
    await page.locator('input[type="email"]').fill(tenant.email);
    await page.locator('input[type="password"]').fill(tenant.password);
    await page.getByRole('button', { name: copy.signIn, exact: true }).click();

    // The TOTP challenge card replaces the password form.
    await expect(
      page.getByRole('heading', { name: copy.totpTitle, level: 1, exact: true }),
    ).toBeVisible({ timeout: 10_000 });

    // Submit the current code. Compute right before fill so we always
    // hit the freshest 30-second window.
    const code = totpCode(enroll.secret);
    await page.locator('input[autocomplete="one-time-code"]').fill(code);
    await page.getByRole('button', { name: copy.totpSubmit, exact: true }).click();

    // Successful TOTP completion redirects to /profile (the default
    // post-login destination when no `?redirect=` was set).
    await expect(page).toHaveURL(/\/profile$/, { timeout: 15_000 });
  });

  test('recovery code completes the challenge and is consumed', async ({ page }) => {
    const tenant = await createTestTenant();
    const enroll = await enrollTotp(tenant.accessToken);
    const confirm = await confirmTotp(tenant.accessToken, totpCode(enroll.secret));
    expect(confirm.recoveryCodes.length).toBeGreaterThan(0);

    await fetch(`${AUTH_API_URL}/auth/logout`, {
      method: 'POST',
      headers: { authorization: `Bearer ${tenant.accessToken}` },
    });

    await page.goto('/login');
    await page.locator('input[type="email"]').fill(tenant.email);
    await page.locator('input[type="password"]').fill(tenant.password);
    await page.getByRole('button', { name: copy.signIn, exact: true }).click();

    await expect(
      page.getByRole('heading', { name: copy.totpTitle, level: 1, exact: true }),
    ).toBeVisible({ timeout: 10_000 });

    // Switch to the recovery-code variant.
    await page.getByRole('button', { name: copy.totpUseRecovery, exact: true }).click();

    const recovery = confirm.recoveryCodes[0];
    if (!recovery) throw new Error('recovery codes were unexpectedly empty');
    await page.locator('input[autocomplete="one-time-code"]').fill(recovery);
    await page.getByRole('button', { name: copy.recoverySubmit, exact: true }).click();

    await expect(page).toHaveURL(/\/profile$/, { timeout: 15_000 });
  });
});
