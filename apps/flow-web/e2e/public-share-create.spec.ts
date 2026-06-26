/**
 * Public share creation E2E.
 *
 * public-share-calendar.spec.ts covers the consumption side (anonymous
 * view of /share/cal/{token}) by minting the share through REST. This
 * spec exercises the **admin creation** half of the journey, which was
 * previously uncovered:
 *
 *   1. an admin navigates to the workspace Public share pages settings
 *      sub-route, opens the "Create share" dialog, fills the title,
 *      and submits. The two-stage dialog must flip to the reveal-once
 *      panel showing the fully qualified `/share/cal/<token>` URL in a
 *      read-only input alongside a Copy action.
 *   2. closing the dialog surfaces the newly created share as a row in
 *      the shares table.
 *   3. clicking Delete on that row and confirming the prompt removes
 *      the row. With no rows left the table collapses to the empty
 *      state copy.
 *
 * Each test owns its own tenant because share creation mutates
 * workspace state (a shared tenant would leak share rows across
 * parallel specs).
 */

import { expect, test } from '@playwright/test';

import { cleanupTenant, createTestTenant, injectAuth, type TestTenant } from './fixtures/tenant';

test.describe('public share create', () => {
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

  test('admin creates a share via Settings and reveals the tokenised URL', async ({ page }) => {
    const t = tenant;
    if (!t) throw new Error('tenant not initialised');
    await injectAuth(page.context(), t);

    await page.goto(`/workspaces/${t.workspaceId}/settings/public-shares`);

    // Section heading from ShareList — regex tolerant to EN/JA copy.
    await expect(
      page.getByRole('heading', { name: /^(public share pages|公開シェアページ)$/i, level: 1 }),
    ).toBeVisible({ timeout: 15_000 });

    // Fresh tenant, so the empty state copy should render first.
    await expect(
      page.getByText(/no public share pages yet\.|公開シェアページはまだありません。/i),
    ).toBeVisible();

    // Open the create dialog. The header button has its own testid so
    // we don't collide with the dialog's primary submit (which shares
    // the "Create" label).
    await page.getByTestId('public-share-create-open').click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible({ timeout: 5_000 });
    await expect(
      dialog.getByRole('heading', {
        name: /^(create public share page|公開シェアページを作成)$/i,
      }),
    ).toBeVisible();

    const shareTitle = `E2E Share ${Date.now().toString(36)}`;
    // Title field is identified by its placeholder — the rendered label
    // is "Title *" which confuses a strict label match because the `*`
    // span, though aria-hidden, is inside the <label> element.
    await dialog
      .getByPlaceholder(/e\.g\. Group A Official Schedule|例: グループ A 公式スケジュール/i)
      .fill(shareTitle);

    // Submit via the primary action inside the dialog.
    await dialog.getByTestId('public-share-create-submit').click();

    // Stage 2: reveal-once panel. The dialog swaps in a read-only URL
    // field and a Copy action. We match on the URL shape (origin +
    // `/share/cal/<token>`) rather than pinning the exact token since
    // it is generated server-side.
    await expect(
      dialog.getByRole('heading', { name: /^(share page created|シェアページを作成しました)$/i }),
    ).toBeVisible({ timeout: 10_000 });

    const urlField = dialog.getByLabel(/^(share url|シェア url)$/i);
    await expect(urlField).toBeVisible();
    const revealedUrl = await urlField.inputValue();
    expect(revealedUrl).toMatch(/^https?:\/\/[^/]+\/share\/cal\/[A-Za-z0-9._-]+$/);

    const copyButton = dialog.getByTestId('public-share-create-copy');
    await expect(copyButton).toBeVisible();
    await expect(copyButton).toBeEnabled();

    // Dismiss the dialog via the reveal-panel Done action.
    await dialog.getByTestId('public-share-create-done').click();
    await expect(dialog).toBeHidden({ timeout: 5_000 });

    // The newly created share should now appear as a row in the table.
    // The title is rendered as a link to the share detail editor.
    await expect(page.getByRole('link', { name: shareTitle, exact: true })).toBeVisible({
      timeout: 10_000,
    });

    // Empty-state copy must be gone now that a row exists.
    await expect(
      page.getByText(/no public share pages yet\.|公開シェアページはまだありません。/i),
    ).toBeHidden();
  });

  test('deleting a created share removes its row and restores the empty state', async ({
    page,
  }) => {
    const t = tenant;
    if (!t) throw new Error('tenant not initialised');
    await injectAuth(page.context(), t);

    await page.goto(`/workspaces/${t.workspaceId}/settings/public-shares`);

    await expect(
      page.getByRole('heading', { name: /^(public share pages|公開シェアページ)$/i, level: 1 }),
    ).toBeVisible({ timeout: 15_000 });

    // Create a share through the dialog so we exercise the UI path
    // end-to-end — no REST shortcut.
    await page.getByTestId('public-share-create-open').click();

    const dialog = page.getByRole('dialog');
    const shareTitle = `Delete-me ${Date.now().toString(36)}`;
    await dialog
      .getByPlaceholder(/e\.g\. Group A Official Schedule|例: グループ A 公式スケジュール/i)
      .fill(shareTitle);
    await dialog.getByTestId('public-share-create-submit').click();

    await expect(
      dialog.getByRole('heading', { name: /^(share page created|シェアページを作成しました)$/i }),
    ).toBeVisible({ timeout: 10_000 });
    await dialog.getByTestId('public-share-create-done').click();
    await expect(dialog).toBeHidden({ timeout: 5_000 });

    // Confirm the row landed in the table.
    const titleLink = page.getByRole('link', { name: shareTitle, exact: true });
    await expect(titleLink).toBeVisible({ timeout: 10_000 });

    // Scope to the table row carrying our share so parallel rows (if
    // any later) cannot confuse the selector. Two buttons live in the
    // actions cell — Rotate and Delete — so filter by testid.
    const row = page.getByRole('row').filter({ hasText: shareTitle });
    await row.getByTestId('public-share-delete').click();

    // Confirmation dialog (imperative confirm primitive) prompts before
    // delete. Click the themed Confirm button.
    const confirmDialog = page.getByRole('dialog');
    await expect(confirmDialog).toBeVisible({ timeout: 5_000 });
    await confirmDialog.getByTestId('confirm-dialog-confirm').click();
    await expect(confirmDialog).toBeHidden({ timeout: 5_000 });

    // Row should be gone and the empty-state copy should reappear.
    await expect(titleLink).toBeHidden({ timeout: 10_000 });
    await expect(
      page.getByText(/no public share pages yet\.|公開シェアページはまだありません。/i),
    ).toBeVisible();
  });
});
