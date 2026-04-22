/**
 * Gantt view E2E.
 *
 * Happy path:
 *   1. Use the shared tenant with pre-seeded tasks (due 2026-04-15 and 2026-04-22).
 *   2. Navigate to the project gantt view.
 *   3. Verify both tasks render in the gantt chart.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';

test.describe('gantt view', () => {
  test('renders seeded tasks with due dates', async ({ page }) => {
    const { user: tenant, seededTasks } = loadTenants();

    await injectAuth(page.context(), tenant);
    await page.goto(`/projects/${tenant.projectId}/gantt`);

    // Verify both pre-seeded task titles are visible in the gantt view
    await expect(page.getByText(seededTasks.ganttA).first()).toBeVisible({ timeout: 15_000 });
    await expect(page.getByText(seededTasks.ganttB).first()).toBeVisible({ timeout: 10_000 });
  });
});
