/**
 * Calendar view E2E.
 *
 * Happy path:
 *   1. Use the shared tenant and seed a task due in the current month.
 *   2. Navigate to the calendar view.
 *   3. Verify the task appears on the calendar.
 *   4. Navigate to the next month and back, verifying month navigation works.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { createTask, injectAuth } from './fixtures/tenant';

function currentMonthDate(day: number): string {
  const now = new Date();
  const year = now.getFullYear();
  const month = String(now.getMonth() + 1).padStart(2, '0');
  return `${String(year)}-${month}-${String(day).padStart(2, '0')}`;
}

test.describe('calendar view', () => {
  test('displays task and supports month navigation', async ({ page }) => {
    const { user: tenant } = loadTenants();
    const taskTitle = `Calendar smoke ${Date.now()}`;
    await createTask(tenant, taskTitle, { dueOn: currentMonthDate(12) });

    await injectAuth(page.context(), tenant);
    await page.goto('/calendar');

    // Verify the current-month task appears on the calendar.
    await expect(page.getByText(taskTitle)).toBeVisible({ timeout: 15_000 });

    // Navigate to next month
    const nextButton = page.getByRole('button', { name: /next|次|forward|>/i });
    await expect(nextButton).toBeVisible({ timeout: 5_000 });
    await nextButton.click();

    // The task should no longer be visible (it was in the previous month).
    await expect(page.getByText(taskTitle)).not.toBeVisible({ timeout: 5_000 });

    // Navigate back to the previous month
    const prevButton = page.getByRole('button', { name: /prev|前|back|</i });
    await prevButton.click();

    // Task should be visible again.
    await expect(page.getByText(taskTitle)).toBeVisible({ timeout: 10_000 });
  });
});
