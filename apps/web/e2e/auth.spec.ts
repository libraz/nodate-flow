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

import {
  type TestTenant,
  cleanupTenant,
  createTask,
  createTestTenant,
  injectAuth,
} from './fixtures/tenant';

test.describe('auth smoke', () => {
  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('signup, see authenticated root, see seeded task, logout', async ({ page }) => {
    tenant = await createTestTenant();

    // 1. Sign up via the UI form. We register a *second* user here so we
    //    are exercising the real signup flow rather than reusing the
    //    fixture's REST-created tenant. The fixture tenant is what owns
    //    the seeded workspace/project we will inspect afterward.
    const uiTenant = await createTestTenant();

    await page.goto('/signup');
    await page.getByLabel(/name/i).fill(uiTenant.displayName);
    await page.getByLabel(/email/i).fill(uiTenant.email);
    await page.getByLabel(/password/i).fill(uiTenant.password);
    await page.getByRole('button', { name: /sign ?up|create account/i }).click();

    // 2. After signup the app navigates to "/" (authenticated root).
    await expect(page).toHaveURL(/\/$/);

    // 3. Seed a task on the *fixture* tenant and verify its title shows
    //    up when we navigate to that project's task list. We inject the
    //    fixture tenant's nf_rt cookie so the bootstrap flow picks up
    //    the correct session on the next navigation.
    const taskTitle = `Smoke task ${Date.now()}`;
    await createTask(tenant, taskTitle);

    await injectAuth(page.context(), tenant);

    await page.goto(`/projects/${tenant.projectId}/tasks`);
    await expect(page.getByText(taskTitle)).toBeVisible({ timeout: 10_000 });
  });
});
