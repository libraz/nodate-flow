/**
 * Task title validation E2E.
 *
 * Expected behaviour:
 *   1. A task title is a required human-readable name; whitespace-only
 *      input must not be submitted.
 *   2. Accidental leading/trailing whitespace is normalized before save,
 *      so the task appears and persists under the visible title the user
 *      intended.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { API_BASE_URL, injectAuth } from './fixtures/tenant';

test.describe('task title validation', () => {
  test('requires a meaningful title and trims accidental whitespace on create', async ({
    page,
  }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    await page.goto(`/projects/${tenant.projectId}/tasks`);
    // Wait for the auth bootstrap to complete so the layout's
    // OPEN_CREATE_TASK_EVENT listener is mounted before we dispatch.
    await page.waitForSelector('[data-nf-authenticated="true"], [role="toolbar"]', {
      timeout: 10_000,
    });
    await page.evaluate(() => {
      window.dispatchEvent(new Event('nf:open-create-task'));
    });

    const dialog = page.getByRole('dialog');
    // FormField renders the required indicator as a sibling <span>*</span>
    // inside the <label>. Playwright's getByLabel matches against the
    // label element's textContent, which therefore contains "Title*"
    // (the asterisk has aria-hidden=true but is still part of the DOM
    // text). Match the prefix instead of insisting on a strict equality.
    const titleInput = dialog.getByLabel(/^(Title|タイトル)\*?$/i);
    const submitButton = dialog.getByRole('button', {
      name: /^(Create|Save|Add|作成|保存|追加)$/i,
    });

    await titleInput.fill('   ');
    await expect(submitButton).toBeDisabled();

    const expectedTitle = `Trimmed E2E Task ${Date.now()}`;
    await titleInput.fill(`  ${expectedTitle}  `);
    await expect(submitButton).toBeEnabled();
    await submitButton.click();

    await expect(page.getByText(expectedTitle).first()).toBeVisible({ timeout: 10_000 });
    // The DOM-rendered title intrinsically collapses whitespace via the
    // browser's text rendering, so a getByText("  X  ") query would match
    // a rendered "X" anyway. The authoritative round-trip check is the
    // REST assertion below: the persisted task.title must be exactly the
    // trimmed value, never the padded one.

    const res = await fetch(
      `${API_BASE_URL}/tasks?projectId=${tenant.projectId}&q=${encodeURIComponent(expectedTitle)}`,
      {
        headers: {
          accept: 'application/json',
          authorization: `Bearer ${tenant.accessToken}`,
        },
      },
    );
    expect(res.ok).toBeTruthy();
    const body = (await res.json()) as { tasks?: Array<{ title: string }> };
    expect(body.tasks?.some((task) => task.title === expectedTitle)).toBe(true);
    expect(body.tasks?.some((task) => task.title === `  ${expectedTitle}  `)).toBe(false);
  });
});
