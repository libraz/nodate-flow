/**
 * Magic-link request flow e2e (G14).
 *
 * The login page exposes a "Sign in with email link" affordance that
 * swaps the password form for a single-field email request. Submitting
 * fires `POST /auth/magic-link/request` against auth-api which always
 * returns 200 (`ok=true`) regardless of whether the email belongs to a
 * real user — this is intentional, to prevent address enumeration.
 *
 * Cases:
 *   A. happy path — known user submits, success copy renders, back link
 *      returns to the password form.
 *   B. enumeration-safe — unknown email ALSO renders the success copy
 *      (no inline error revealing that the address is unregistered).
 *   C. POST is fired with the trimmed email — verifies the network call
 *      shape against the auth-api contract by inspecting the request
 *      body via `page.waitForRequest`.
 *
 * SMTP delivery is out of scope; the auth-api silently succeeds when no
 * sender is configured (dev compose). We assert on the UI transition
 * and the network call, not on inbox state.
 */

import { type Page, expect, test } from '@playwright/test';

import enAuth from '../locales/en/auth.json' with { type: 'json' };
import { AUTH_API_URL, createTestTenant } from './fixtures/tenant';

/**
 * Force `magicLink: true` in the capabilities feed. Dev compose runs
 * without an SMTP sender, so the real /auth/capabilities response sets
 * magicLink=false and the button is hidden — but the
 * /auth/magic-link/request endpoint itself is always wired in. Stubbing
 * just the capabilities feed lets us drive the real request handler.
 */
async function enableMagicLink(page: Page): Promise<void> {
  await page.route(`${AUTH_API_URL}/auth/capabilities`, async (route) => {
    const r = await route.fetch();
    const json = (await r.json()) as Record<string, unknown>;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...json, magicLink: true }),
    });
  });
}

const copy = {
  magicButton: enAuth.login.magic_link_button,
  magicTitle: enAuth.login.magic_link_title,
  magicSent: enAuth.login.magic_link_sent,
  magicBack: enAuth.login.magic_link_back,
  magicSubmit: enAuth.login.magic_link_submit,
  signIn: enAuth.login.submit,
  email: enAuth.login.email,
} as const;

test.describe('magic-link request', () => {
  test('A: registered email renders the inbox-check confirmation', async ({ page }) => {
    const tenant = await createTestTenant();

    await enableMagicLink(page);
    await page.goto('/login');
    await page.getByRole('button', { name: copy.magicButton, exact: true }).click();

    // Magic-link card is visible.
    await expect(
      page.getByRole('heading', { name: copy.magicTitle, level: 1, exact: true }),
    ).toBeVisible({ timeout: 10_000 });

    // Email field uses the localized "Email" label.
    const emailField = page.locator('input[type="email"]');
    await emailField.fill(tenant.email);
    await page.getByRole('button', { name: copy.magicSubmit, exact: true }).click();

    // Success copy interpolates the email, so match by substring around
    // the localized phrase.
    await expect(page.getByText(/Check your inbox/i)).toBeVisible({ timeout: 10_000 });
    await expect(page.getByText(tenant.email)).toBeVisible();

    // Back link returns to the password form: heading is the localized
    // sign-in title.
    await page.getByRole('button', { name: copy.magicBack, exact: true }).click();
    await expect(page.getByRole('button', { name: copy.signIn, exact: true })).toBeVisible({
      timeout: 5_000,
    });
  });

  test('B: unknown email also renders success (enumeration-safe)', async ({ page }) => {
    await enableMagicLink(page);
    await page.goto('/login');
    await page.getByRole('button', { name: copy.magicButton, exact: true }).click();
    await expect(
      page.getByRole('heading', { name: copy.magicTitle, level: 1, exact: true }),
    ).toBeVisible({ timeout: 10_000 });

    const ghost = `noone+${Date.now().toString(36)}@example.test`;
    await page.locator('input[type="email"]').fill(ghost);
    await page.getByRole('button', { name: copy.magicSubmit, exact: true }).click();

    // Same success copy as the happy path — the API does not leak
    // existence to the UI.
    await expect(page.getByText(/Check your inbox/i)).toBeVisible({ timeout: 10_000 });
    await expect(page.getByRole('alert')).toHaveCount(0);
  });

  test('C: POST /auth/magic-link/request fires with the entered email', async ({ page }) => {
    const tenant = await createTestTenant();

    await enableMagicLink(page);
    await page.goto('/login');
    await page.getByRole('button', { name: copy.magicButton, exact: true }).click();
    await expect(
      page.getByRole('heading', { name: copy.magicTitle, level: 1, exact: true }),
    ).toBeVisible({ timeout: 10_000 });

    // Trailing whitespace must be trimmed by the client before the POST.
    await page.locator('input[type="email"]').fill(`  ${tenant.email}  `);

    const reqPromise = page.waitForRequest(
      (req) => req.url() === `${AUTH_API_URL}/auth/magic-link/request` && req.method() === 'POST',
    );
    await page.getByRole('button', { name: copy.magicSubmit, exact: true }).click();
    const req = await reqPromise;

    const body = JSON.parse(req.postData() ?? '{}') as { email?: string };
    expect(body.email).toBe(tenant.email);
  });
});
