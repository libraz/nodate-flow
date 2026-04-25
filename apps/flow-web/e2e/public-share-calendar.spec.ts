/**
 * Public share calendar happy-path E2E.
 *
 * The existing public-lens.spec.ts only covers the invalid-token error
 * path on the lens feature. This spec covers the sister feature — public
 * calendar shares at /share/cal/{token} — and exercises the full create
 * then anonymous-view round trip:
 *
 *   1. Create a workspace-owned public share via REST (the POST
 *      returns the plaintext token exactly once).
 *   2. Open the /share/cal/{token} route in a fresh, unauthenticated
 *      browser context to ensure the token alone grants access.
 *   3. Assert the share page renders the share title as an h1 without
 *      crashing or leaking i18n keys.
 *
 * We skip driving the admin UI to create the share because the
 * settings/public-shares surface is already covered by
 * workspace-settings.spec.ts's render check, and the value here is the
 * anonymous-view path, not the admin form.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { API_BASE_URL } from './fixtures/tenant';

test.describe('public share calendar', () => {
  test('create via REST, view anonymously, title renders', async ({ browser }) => {
    const { user: tenant } = loadTenants();

    const title = `E2E Share ${Date.now().toString(36)}`;
    const createRes = await fetch(
      `${API_BASE_URL}/workspaces/${tenant.workspaceId}/public-shares`,
      {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          accept: 'application/json',
          authorization: `Bearer ${tenant.accessToken}`,
        },
        body: JSON.stringify({ title }),
      },
    );
    if (!createRes.ok) {
      throw new Error(`create public share failed: ${createRes.status} ${await createRes.text()}`);
    }
    const body = (await createRes.json()) as { token: string; title: string };
    expect(body.token.length).toBeGreaterThan(0);

    // Fresh context with no auth cookies — the token alone must suffice.
    const anon = await browser.newContext();
    try {
      const page = await anon.newPage();
      await page.goto(`/share/cal/${body.token}`);
      await page.waitForLoadState('domcontentloaded');

      // The share page renders the title in an h1.
      await expect(page.getByRole('heading', { level: 1, name: title })).toBeVisible({
        timeout: 15_000,
      });

      // Verify no i18n key leaks in the rendered page.
      const bodyText = await page.locator('body').innerText();
      expect(bodyText).not.toMatch(/\bshare\.\w+\.\w+/);
      expect(bodyText).not.toMatch(/\bsharing\.\w+\.\w+/);
    } finally {
      await anon.close();
    }
  });
});
