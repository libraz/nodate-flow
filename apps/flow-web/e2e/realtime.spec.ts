/**
 * Realtime SSE invalidation test (ADR 0005).
 *
 * Asserts that `useWorkspaceStream` — mounted once per authenticated
 * tree in `_authenticated.tsx` — actually delivers `task.changed` and
 * that the delivery reaches the query cache:
 *
 *   1. Create a fresh tenant with one task via REST.
 *   2. Inject auth and open the project task list, which reads the
 *      `['tasks']` cache the stream invalidates.
 *   3. Assert the seeded task renders (the pull path works, so a
 *      failure in step 4 is about the stream and not about the list).
 *   4. Create a second task via REST *without touching the page* and
 *      assert it appears within the stream budget.
 *
 * Nothing here polls: the task list has no `refetchInterval`, so the
 * only way the second row can appear is an SSE `task.changed` frame
 * invalidating `['tasks']` (see `event-to-keys.ts`).
 *
 * This deliberately avoids the reminders panel. The earlier version of
 * this test drove the AI suggestions dock, which needs a configured
 * provider to render anything, and was therefore committed as
 * `test.skip` — the only test in the file, so the whole realtime layer
 * went unverified while the report stayed green. The task list needs
 * no provider, so this version runs everywhere the suite runs.
 */

import { expect, test } from '@playwright/test';

import { cleanupTenant, createTask, createTestTenant, injectAuth } from './fixtures/tenant';

test.describe('realtime stream', () => {
  test('task list picks up an out-of-band create via SSE, without polling', async ({ page }) => {
    const tenant = await createTestTenant();
    try {
      const seededTitle = `Realtime seed ${Date.now()}`;
      await createTask(tenant, seededTitle);

      await injectAuth(page.context(), tenant);
      await page.goto(`/projects/${tenant.projectId}/tasks`);
      await page.waitForLoadState('networkidle');

      // The pull path works, so the list is subscribed to ['tasks'].
      await expect(page.getByText(seededTitle).first()).toBeVisible({ timeout: 15_000 });

      // Created after the page settled, from outside the browser. With
      // no polling on this view, only an SSE invalidation can surface it.
      const streamedTitle = `Realtime streamed ${Date.now()}`;
      await createTask(tenant, streamedTitle);

      await expect(page.getByText(streamedTitle).first()).toBeVisible({ timeout: 15_000 });
    } finally {
      await cleanupTenant(tenant);
    }
  });
});
