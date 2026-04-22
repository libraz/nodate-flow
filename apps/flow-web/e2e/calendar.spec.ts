/**
 * Calendar view E2E.
 *
 * Happy path:
 *   1. Use the shared tenant with a pre-seeded task (due 2026-04-12).
 *   2. Navigate to the calendar view.
 *   3. Verify the task appears on the calendar.
 *   4. Navigate to the next month and back, verifying month navigation works.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';

test.describe('calendar view', () => {
  test('displays task and supports month navigation', async ({ page }) => {
    const { user: tenant, seededTasks } = loadTenants();

    await injectAuth(page.context(), tenant);
    await page.goto('/calendar');

    // Verify pre-seeded task appears on the calendar
    await expect(page.getByText(seededTasks.calendarApril)).toBeVisible({ timeout: 15_000 });

    // Navigate to next month
    const nextButton = page.getByRole('button', { name: /next|次|forward|>/i });
    await expect(nextButton).toBeVisible({ timeout: 5_000 });
    await nextButton.click();

    // The task should no longer be visible (it was in the previous month)
    await expect(page.getByText(seededTasks.calendarApril)).not.toBeVisible({ timeout: 5_000 });

    // Navigate back to the previous month
    const prevButton = page.getByRole('button', { name: /prev|前|back|</i });
    await prevButton.click();

    // Task should be visible again
    await expect(page.getByText(seededTasks.calendarApril)).toBeVisible({ timeout: 10_000 });
  });
});
