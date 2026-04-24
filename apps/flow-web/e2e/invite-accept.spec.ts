/**
 * Workspace invite accept happy-path E2E.
 *
 * The existing invite.spec.ts only covers the invalid-token error state.
 * This spec covers the cross-user happy path:
 *
 *   1. user (owner of their workspace) creates an invite via REST
 *      against POST /workspaces/{wsId}/invites. The plaintext token is
 *      returned once.
 *   2. user2 (a separate tenant, NOT a member of user's workspace) logs
 *      in in a fresh browser context and visits /invite/{token}.
 *   3. The invite landing page shows "Join <workspace>" with a Join
 *      button. Clicking Join posts POST /invites/{token}/accept and the
 *      router redirects to /workspaces/{id}, confirming membership.
 *
 * This is the single UI path that the existing specs leave uncovered:
 * the accept mutation wired into the router-driven redirect after a
 * successful join.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { AUTH_API_URL, injectAuth } from './fixtures/tenant';

test.describe('invite accept', () => {
  test('second tenant accepts an invite and lands on the joined workspace', async ({ browser }) => {
    const { user: inviter, user2: invitee } = loadTenants();

    // 1. Inviter creates a member-role invite against their own workspace.
    const createRes = await fetch(`${AUTH_API_URL}/workspaces/${inviter.workspaceId}/invites`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        accept: 'application/json',
        authorization: `Bearer ${inviter.accessToken}`,
      },
      body: JSON.stringify({ role: 'member' }),
    });
    if (!createRes.ok) {
      throw new Error(`create invite failed: ${createRes.status} ${await createRes.text()}`);
    }
    const { token } = (await createRes.json()) as { token: string };
    expect(token.length).toBeGreaterThan(0);

    // 2. Second tenant logs in in an isolated context and visits the
    //    invite URL. Isolated context ensures no cookie bleed from other
    //    parallel specs that may have mounted a different session for
    //    user2.
    const inviteeContext = await browser.newContext();
    try {
      await injectAuth(inviteeContext, invitee);
      const page = await inviteeContext.newPage();
      await page.goto(`/invite/${token}`);

      // The invite landing renders the join title with the workspace name
      // interpolated. We don't pin the exact interpolation (i18n-friendly
      // search).
      await expect(page.getByRole('heading', { level: 1, name: /join/i })).toBeVisible({
        timeout: 10_000,
      });

      // 3. Click the Join button. After the mutation resolves the router
      //    replaces the URL with /workspaces/{id}.
      await page.getByRole('button', { name: /join workspace/i }).click();

      await expect(page).toHaveURL(/\/workspaces\/[^/]+/, { timeout: 15_000 });

      // Sanity: the workspace page actually loaded something (avoids a
      // false-pass where the URL changed but the page crashed).
      const main = page.getByRole('main');
      await expect(main).toBeVisible({ timeout: 10_000 });
    } finally {
      await inviteeContext.close();
    }
  });
});
