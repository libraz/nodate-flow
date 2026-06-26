/**
 * Realtime SSE invalidation smoke test (ADR 0005).
 *
 * Asserts the `useWorkspaceStream` hook wires up correctly in the
 * glass dock:
 *
 *   1. Create a fresh tenant with an overdue task via REST.
 *   2. Inject the tenant's nf_rt cookie into the browser context and
 *      navigate to a workspace-scoped route so the dock knows which
 *      workspace to subscribe to.
 *   3. Open the glass dock and assert the initial overdue task
 *      surfaces in the reminders panel (proves the pull path works).
 *   4. Create a *second* overdue task via REST without refreshing
 *      the page and assert it shows up within the SSE budget.
 *
 * With polling disabled (ADR 0005, `refetchInterval: streamHealthy ?
 * false : ...`), the second task can only appear if the SSE stream
 * is delivering `task.changed` invalidations and the reminder query
 * re-runs on invalidation. A stale implementation (or a broken
 * stream) will time out on step 4.
 */

import { expect, test } from '@playwright/test';

import {
  API_BASE_URL,
  cleanupTenant,
  createTestTenant,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';

interface TaskResponse {
  id: string;
  title: string;
}

/**
 * Creates an overdue task via the top-level /tasks endpoint so we
 * can set a past dueOn. The project-scoped fixture helper does not
 * accept dueOn today.
 */
async function createOverdueTask(tenant: TestTenant, title: string): Promise<TaskResponse> {
  const past = new Date(Date.now() - 3 * 24 * 60 * 60 * 1000).toISOString().slice(0, 10);
  const res = await fetch(`${API_BASE_URL}/tasks`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
    body: JSON.stringify({
      projectId: tenant.projectId,
      title,
      priority: 2,
      dueOn: past,
    }),
  });
  if (!res.ok) {
    throw new Error(`POST /tasks -> ${res.status} ${await res.text()}`);
  }
  return (await res.json()) as TaskResponse;
}

test.describe('realtime stream', () => {
  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  // This test requires the SSE stream + reminders pipeline to be fully wired.
  // In dev environments without AI providers, the reminders panel stays empty.
  // Run with NF_FLOW_STREAM=true and a working AI backend to enable.
  test.skip('glass dock receives SSE invalidations without polling', async ({ page }) => {
    tenant = await createTestTenant();

    const firstTitle = `Overdue A ${Date.now()}`;
    await createOverdueTask(tenant, firstTitle);

    // Inject the REST-created tenant's nf_rt cookie so the app's
    // bootstrap flow (POST /auth/refresh) authenticates on page load,
    // then navigate to a workspace route so `useActiveWorkspaceId()`
    // resolves.
    await injectAuth(page.context(), tenant);

    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // Open the glass dock pill (AI suggestions button at bottom-right).
    await page.getByRole('button', { name: /expand suggestions panel|AI suggestions/i }).click();

    // 1st task should appear via the pull path (initial load).
    await expect(page.getByText(firstTitle).first()).toBeVisible({ timeout: 10_000 });

    // 2nd task is created *after* the dock is open. Without SSE,
    // this would never appear because polling is disabled when the
    // stream is healthy (ADR 0005).
    const secondTitle = `Overdue B ${Date.now()}`;
    await createOverdueTask(tenant, secondTitle);

    await expect(page.getByText(secondTitle)).toBeVisible({ timeout: 8_000 });
  });
});
