/**
 * Workspace invite creation E2E.
 *
 * invite-accept.spec.ts already covers the accept flow by minting an
 * invite through REST. This spec exercises the **creation** half of the
 * journey, which currently has no UI coverage:
 *
 *   1. an admin navigates to the workspace Members tab, opens the
 *      "Create invite link" dialog, picks a role, and submits. The
 *      two-step dialog must flip to the reveal-once panel showing the
 *      tokenised accept URL in a read-only input alongside a Copy
 *      action.
 *   2. closing the dialog surfaces the newly created invite as a row
 *      in the active-invites DataGrid below the members table.
 *   3. clicking Revoke on that row removes it from the list.
 *
 * Each test owns its own tenant because invite creation mutates
 * workspace state (the shared tenant's invites would otherwise bleed
 * across parallel specs).
 */

import { expect, test } from '@playwright/test';

import { type TestTenant, cleanupTenant, createTestTenant, injectAuth } from './fixtures/tenant';

test.describe('invite create', () => {
  let tenant: TestTenant | null = null;

  test.beforeEach(async () => {
    tenant = await createTestTenant();
  });

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('admin creates an invite via the Members tab and reveals the tokenised URL', async ({
    page,
  }) => {
    const t = tenant;
    if (!t) throw new Error('tenant not initialised');
    await injectAuth(page.context(), t);

    // The Members tab is a `?tab=members` variant of the workspace
    // detail route — cheaper than driving the sidebar + tab click.
    await page.goto(`/workspaces/${t.workspaceId}?tab=members`);

    // Members section heading — regex tolerant to EN/JA copy.
    await expect(page.getByRole('heading', { name: /^(members|メンバー)$/i })).toBeVisible({
      timeout: 15_000,
    });

    // Open the creation dialog. The button sits in the Members tab
    // header next to "Add member".
    await page.getByRole('button', { name: /^(create invite link|招待リンクを作成)$/i }).click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible({ timeout: 5_000 });
    await expect(
      dialog.getByRole('heading', { name: /create invite link|招待リンクの作成/i }),
    ).toBeVisible();

    // Pick a non-default role so the spec proves the role select is
    // actually wired through the submission — default is "member".
    // The option `value` attributes are untranslated role slugs, so
    // selecting by value sidesteps locale-sensitive label matching.
    const roleSelect = dialog.getByLabel(/^(role|ロール)$/i);
    await roleSelect.selectOption('admin');

    // Label helps the list assertion below — the first column shows
    // the label when set and a placeholder when blank.
    const inviteLabel = `E2E invite ${Date.now()}`;
    await dialog.getByLabel(/^(label \(optional\)|ラベル（任意）)$/i).fill(inviteLabel);

    await dialog.getByRole('button', { name: /^(create invite link|招待リンクを作成)$/i }).click();

    // Step 2: reveal-once panel. The dialog swaps in a read-only input
    // carrying the fully qualified /invite/<token> URL plus a Copy
    // action. We assert the URL shape (path + non-empty token) rather
    // than pinning the exact token since it is generated server-side.
    await expect(
      dialog.getByRole('heading', { name: /^(invite link ready|招待リンクの準備ができました)$/i }),
    ).toBeVisible({ timeout: 10_000 });

    const tokenField = dialog.getByRole('textbox');
    await expect(tokenField).toBeVisible();
    const revealedUrl = await tokenField.inputValue();
    expect(revealedUrl).toMatch(/^https?:\/\/[^/]+\/invite\/[A-Za-z0-9._-]+$/);

    const copyButton = dialog.getByRole('button', { name: /^(copy link|リンクをコピー)$/i });
    await expect(copyButton).toBeVisible();
    await expect(copyButton).toBeEnabled();

    // Close the dialog via the Close action in the reveal panel.
    await dialog.getByRole('button', { name: /^(close|閉じる)$/i }).click();
    await expect(dialog).toBeHidden({ timeout: 5_000 });

    // The active-invites DataGrid (aria-label = "Invite links" / "招待
    // リンク") only renders once the workspace has at least one invite.
    const invitesGrid = page.getByRole('grid', { name: /^(invite links|招待リンク)$/i });
    await expect(invitesGrid).toBeVisible({ timeout: 10_000 });

    // The label we submitted should appear in the grid body.
    await expect(invitesGrid.getByText(inviteLabel, { exact: true })).toBeVisible();
    // And the role cell must reflect the non-default we picked.
    await expect(invitesGrid.getByRole('gridcell', { name: 'admin' })).toBeVisible();
  });

  test('revoking a created invite removes its row from the active list', async ({ page }) => {
    const t = tenant;
    if (!t) throw new Error('tenant not initialised');
    await injectAuth(page.context(), t);

    await page.goto(`/workspaces/${t.workspaceId}?tab=members`);

    await expect(page.getByRole('heading', { name: /^(members|メンバー)$/i })).toBeVisible({
      timeout: 15_000,
    });

    await page.getByRole('button', { name: /^(create invite link|招待リンクを作成)$/i }).click();

    const dialog = page.getByRole('dialog');
    const inviteLabel = `Revoke-me ${Date.now()}`;
    await dialog.getByLabel(/^(label \(optional\)|ラベル（任意）)$/i).fill(inviteLabel);
    await dialog.getByRole('button', { name: /^(create invite link|招待リンクを作成)$/i }).click();

    // Wait for reveal panel then dismiss the dialog.
    await expect(
      dialog.getByRole('heading', { name: /^(invite link ready|招待リンクの準備ができました)$/i }),
    ).toBeVisible({ timeout: 10_000 });
    await dialog.getByRole('button', { name: /^(close|閉じる)$/i }).click();
    await expect(dialog).toBeHidden({ timeout: 5_000 });

    const invitesGrid = page.getByRole('grid', { name: /^(invite links|招待リンク)$/i });
    const labelCell = invitesGrid.getByText(inviteLabel, { exact: true });
    await expect(labelCell).toBeVisible({ timeout: 10_000 });

    // Each row has its own Revoke button; scope to the row containing
    // the label we just created so parallel rows cannot confuse the
    // selector.
    const row = invitesGrid.getByRole('row').filter({ hasText: inviteLabel });
    await row.getByRole('button', { name: /^(revoke|無効化)$/i }).click();

    // The list hides itself entirely when empty (component returns an
    // empty fragment), so after revoke the grid should disappear.
    await expect(invitesGrid).toBeHidden({ timeout: 10_000 });
  });
});
