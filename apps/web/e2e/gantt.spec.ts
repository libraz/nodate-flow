/**
 * Gantt view E2E.
 *
 * Happy path:
 *   1. Create a fresh tenant via REST.
 *   2. Seed 2 tasks with due dates via REST.
 *   3. Navigate to the project gantt view.
 *   4. Verify both tasks render in the gantt chart.
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

test.describe('gantt view', () => {
  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('renders seeded tasks with due dates', async ({ page }) => {
    tenant = await createTestTenant();

    const task1Title = `Gantt Task A ${Date.now()}`;
    const task2Title = `Gantt Task B ${Date.now()}`;

    // Seed tasks with due dates spanning a week
    await createTaskWithDueDate(tenant, task1Title, '2026-04-15');
    await createTaskWithDueDate(tenant, task2Title, '2026-04-22');

    await injectAuth(page.context(), tenant);
    await page.goto(`/projects/${tenant.projectId}/gantt`);

    // Verify both task titles are visible in the gantt view
    await expect(page.getByText(task1Title)).toBeVisible({ timeout: 15_000 });
    await expect(page.getByText(task2Title)).toBeVisible({ timeout: 10_000 });
  });
});
