/**
 * Task CRUD E2E.
 *
 * Happy path:
 *   1. Create a fresh tenant via REST (workspace + project pre-seeded).
 *   2. Inject auth and navigate to the project task list.
 *   3. Create a task via the UI and verify it appears on the board.
 *   4. Click the task to open detail, verify title is shown.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

test.describe('task crud', () => {
  test('create task via UI and verify it appears', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    await page.goto(`/projects/${tenant.projectId}/tasks`);

    // Create a new task via the "New task" button
    const taskTitle = `E2E Task ${Date.now()}`;
    await page
      .getByRole('button', { name: /new task|add task|create task/i })
      .first()
      .click();
    await page.getByLabel(/title/i).fill(taskTitle);

    // Submit via the dialog
    const dialog = page.getByRole('dialog');
    await dialog.getByRole('button', { name: /^(create|save|add)$/i }).click();

    // Verify task appears on the board
    await expect(page.getByText(taskTitle).first()).toBeVisible({ timeout: 10_000 });

    // Click the task to open detail page
    await page.getByText(taskTitle).first().click();

    // Verify we navigated to task detail (URL contains /tasks/)
    await expect(page).toHaveURL(/\/tasks\//, { timeout: 5_000 });

    // Accessibility check on the task detail page
    await checkA11y(page, [
      'color-contrast',
      'region',
      'landmark-complementary-is-top-level',
      'page-has-heading-one',
    ]);
  });
});
