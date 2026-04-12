/**
 * Calendar view E2E.
 *
 * Happy path:
 *   1. Create a fresh tenant via REST.
 *   2. Seed a task with dueOn via REST.
 *   3. Navigate to the calendar view.
 *   4. Verify the task appears on the calendar.
 *   5. Navigate to the next month and back, verifying month navigation works.
 */

import { expect, test } from '@playwright/test';

import {
  API_BASE_URL,
  type TestTenant,
  cleanupTenant,
  createTestTenant,
  injectAuth,
} from './fixtures/tenant';

async function createTaskWithDueDate(
  tenant: TestTenant,
  title: string,
  dueOn: string,
): Promise<{ id: string; title: string }> {
  const res = await fetch(
    `${API_BASE_URL}/workspaces/${tenant.workspaceId}/projects/${tenant.projectId}/tasks`,
    {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        accept: 'application/json',
        authorization: `Bearer ${tenant.accessToken}`,
      },
      body: JSON.stringify({ title, dueOn }),
    },
  );
  if (!res.ok) {
    throw new Error(`POST /tasks -> ${res.status} ${await res.text()}`);
  }
  return (await res.json()) as { id: string; title: string };
}

test.describe('calendar view', () => {
  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('displays task and supports month navigation', async ({ page }) => {
    tenant = await createTestTenant();

    const taskTitle = `Calendar Task ${Date.now()}`;
    // Use a date in the current month so it shows immediately
    await createTaskWithDueDate(tenant, taskTitle, '2026-04-12');

    await injectAuth(page.context(), tenant);
    await page.goto('/calendar');

    // Verify task appears on the calendar
    await expect(page.getByText(taskTitle)).toBeVisible({ timeout: 15_000 });

    // Navigate to next month
    const nextButton = page.getByRole('button', { name: /next|次|forward|>/i });
    await expect(nextButton).toBeVisible({ timeout: 5_000 });
    await nextButton.click();

    // The task should no longer be visible (it was in the previous month)
    await expect(page.getByText(taskTitle)).not.toBeVisible({ timeout: 5_000 });

    // Navigate back to the previous month
    const prevButton = page.getByRole('button', { name: /prev|前|back|</i });
    await prevButton.click();

    // Task should be visible again
    await expect(page.getByText(taskTitle)).toBeVisible({ timeout: 10_000 });
  });
});
