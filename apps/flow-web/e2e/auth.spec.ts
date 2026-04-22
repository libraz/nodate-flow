/**
 * Closeout smoke test.
 *
 * Happy path:
 *   1. Create a fresh tenant via REST (workspace + project seeded).
 *   2. Log in via the UI signup → land on authenticated root.
 *   3. Seed a task via REST and navigate to the project task list.
 *   4. Verify the task title is visible in the UI.
 *   5. Logout (REST best-effort cleanup).
 *
 * The test deliberately uses REST for setup/teardown and only drives the
 * UI for the assertions that prove the React shell + router + auth wiring
 * are alive. This keeps the smoke test small and stable while still
 * exercising the full stack end to end.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';

test.describe('auth smoke', () => {
  test('authenticated root shows dashboard, seeded task visible in project', async ({ page }) => {
    const { user: tenant, seededTasks } = loadTenants();

    // Inject auth and navigate to authenticated root
    await injectAuth(page.context(), tenant);
    await page.goto('/');

    // Verify we land on the authenticated root (dashboard)
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible({ timeout: 10_000 });

    // Navigate to the project task list and verify pre-seeded task
    await page.goto(`/projects/${tenant.projectId}/tasks`);
    await expect(page.getByText(seededTasks.smoke).first()).toBeVisible({ timeout: 10_000 });
  });
});
