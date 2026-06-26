/**
 * Pages (wiki) CRUD E2E.
 *
 * Exercises the full create / edit / delete cycle for wiki pages. A dedicated
 * tenant is provisioned per test so that pages created and destroyed here do
 * not bleed into specs that only assert on the shared seeded tenant.
 *
 * Coverage:
 *   1. create a root page via the editor form and land on its detail URL.
 *   2. edit the title of an existing page and verify the change persists
 *      across a full page reload.
 *   3. delete a page through the danger action, confirm the themed dialog,
 *      and verify the app navigates away from the deleted page before the
 *      mutation fires (regression guard for commit 20c9460).
 */

import { expect, test } from '@playwright/test';

import { cleanupTenant, createTestTenant, injectAuth, type TestTenant } from './fixtures/tenant';

test.describe('pages crud', () => {
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

  test('creates, edits, and deletes a page end-to-end', async ({ page }) => {
    const t = tenant;
    if (!t) throw new Error('tenant not initialised');
    await injectAuth(page.context(), t);

    await page.goto('/pages');

    // Sidebar renders once the workspace resolves.
    await expect(page.getByRole('searchbox')).toBeVisible({ timeout: 10_000 });

    // -------------------------------------------------------------------
    // 1) Create
    // -------------------------------------------------------------------
    // The tree sidebar exposes two affordances labelled with t('create'):
    // an icon-only header button and a labelled button at the bottom.
    // Click the labelled one via its visible text so the selector matches
    // both EN ("New page") and JA ("新しいページ").
    const createTitle = `E2E Page ${Date.now()}`;
    await page
      .getByRole('button', { name: /^(new page|新しいページ)$/i })
      .last()
      .click();

    const titleField = page.getByLabel(/^(title|タイトル)$/i);
    await expect(titleField).toBeVisible({ timeout: 5_000 });
    await titleField.fill(createTitle);

    // The submit button reuses the "create" label inside the editor form.
    // Scope to the form so we do not re-click the sidebar's own trigger.
    const editorForm = page.locator('form').filter({ has: titleField });
    await editorForm.getByRole('button', { name: /^(new page|新しいページ)$/i }).click();

    // Expect navigation to the detail URL for the freshly-minted page.
    await expect(page).toHaveURL(/\/pages\/[0-9a-f-]{36}$/i, { timeout: 10_000 });

    // The heading for the new page should render on the detail view.
    const headingLocator = page.getByRole('heading', { level: 1, name: createTitle });
    await expect(headingLocator).toBeVisible({ timeout: 10_000 });

    // Capture the detail URL so we can return to it after navigating away.
    const detailUrl = page.url();

    // -------------------------------------------------------------------
    // 2) Edit
    // -------------------------------------------------------------------
    // Click the Edit action in the detail header. The button surfaces
    // t('edit') text; regex covers EN and JA.
    await page.getByRole('button', { name: /^(edit page|ページを編集)$/i }).click();

    const updatedTitle = `${createTitle} (edited)`;
    const editTitleField = page.getByLabel(/^(title|タイトル)$/i);
    await expect(editTitleField).toBeVisible({ timeout: 5_000 });
    await editTitleField.fill(updatedTitle);

    // In edit mode the submit button reuses t('edit') (see page-editor.tsx).
    const editForm = page.locator('form').filter({ has: editTitleField });
    await editForm.getByRole('button', { name: /^(edit page|ページを編集)$/i }).click();

    // After save the component switches back to view mode and the heading
    // should reflect the new title.
    await expect(page.getByRole('heading', { level: 1, name: updatedTitle })).toBeVisible({
      timeout: 10_000,
    });

    // Persist across reload — proves the change hit the backend, not just
    // client state.
    await page.reload();
    await expect(page.getByRole('heading', { level: 1, name: updatedTitle })).toBeVisible({
      timeout: 10_000,
    });

    // -------------------------------------------------------------------
    // 3) Delete
    // -------------------------------------------------------------------
    await page.getByRole('button', { name: /^(delete page|ページを削除)$/i }).click();

    // The themed confirm dialog announces as role=dialog. The confirm button
    // uses t('common:common.confirm') — EN "Confirm" / JA "実行".
    const confirmDialog = page.getByRole('dialog');
    await expect(confirmDialog).toBeVisible({ timeout: 5_000 });
    await confirmDialog.getByRole('button', { name: /^(confirm|実行)$/i }).click();

    // Commit 20c9460 adds `navigate({ to: '/pages' })` BEFORE firing the
    // delete mutation so the detail query does not try to refetch a
    // tombstoned row. Assert the URL moves off the page's own slug.
    await expect(page).toHaveURL(/\/pages(\/?|\?.*)?$/, { timeout: 10_000 });
    expect(page.url()).not.toBe(detailUrl);

    // And the page should be gone from the tree. Scope to the sidebar tree
    // so a stale breadcrumb (if any) does not give a false positive.
    await expect(page.getByRole('heading', { level: 1, name: updatedTitle })).toHaveCount(0, {
      timeout: 10_000,
    });
    await expect(page.getByRole('link', { name: updatedTitle })).toHaveCount(0);
  });
});
