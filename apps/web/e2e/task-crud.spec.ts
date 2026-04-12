/**
 * Task CRUD E2E.
 *
 * Happy path:
 *   1. Create a fresh tenant via REST (workspace + project pre-seeded).
 *   2. Inject auth and navigate to the project task list.
 *   3. Create a task via the UI and verify it appears.
 *   4. Edit the task title inline (double-click) and verify the change.
 *   5. Delete the task and verify it disappears.
 */

import { expect, test } from '@playwright/test';

import { type TestTenant, cleanupTenant, createTestTenant, injectAuth } from './fixtures/tenant';

test.describe('task crud', () => {
  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('create, edit inline, and delete a task', async ({ page }) => {
    tenant = await createTestTenant();
    await injectAuth(page.context(), tenant);

    await page.goto(`/projects/${tenant.projectId}/tasks`);

    // Create a new task
    const taskTitle = `E2E Task ${Date.now()}`;
    await page.getByRole('button', { name: /new task|add task|create task/i }).click();
    await page.getByLabel(/title/i).fill(taskTitle);
    await page.getByRole('button', { name: /create|save|add/i }).click();

    // Verify task appears in the list
    const taskRow = page.getByText(taskTitle);
    await expect(taskRow).toBeVisible({ timeout: 10_000 });

    // Edit task title inline via double-click
    await taskRow.dblclick();
    const editInput = page.getByRole('textbox', { name: /title/i });
    await expect(editInput).toBeVisible({ timeout: 5_000 });

    const updatedTitle = `${taskTitle} (edited)`;
    await editInput.clear();
    await editInput.fill(updatedTitle);
    await editInput.press('Enter');

    // Verify the updated title appears
    await expect(page.getByText(updatedTitle)).toBeVisible({ timeout: 5_000 });
    await expect(page.getByText(taskTitle).first()).not.toBeVisible();

    // Delete the task
    await page.getByText(updatedTitle).click();
    await page.getByRole('button', { name: /delete/i }).click();

    // Confirm deletion if a dialog appears
    const confirmButton = page.getByRole('button', { name: /confirm|delete|yes/i });
    if (await confirmButton.isVisible({ timeout: 2_000 }).catch(() => false)) {
      await confirmButton.click();
    }

    // Verify task is removed
    await expect(page.getByText(updatedTitle)).not.toBeVisible({ timeout: 5_000 });
  });
});
