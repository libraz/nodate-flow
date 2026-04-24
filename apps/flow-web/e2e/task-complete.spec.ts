/**
 * Task Complete transition E2E.
 *
 * Exercises the state-transition path that existing task-crud.spec.ts and
 * task-detail.spec.ts leave uncovered: clicking the "Complete" button on
 * the task detail sidebar's Transitions panel, posting to
 * POST /tasks/{id}/transitions, and observing the state badge flip to
 * "Done".
 *
 * Setup is REST-only (create task in fresh tenant's project). The UI
 * drives both the navigation to detail and the transition click.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { API_BASE_URL, injectAuth } from './fixtures/tenant';

test.describe('task complete transition', () => {
  test('clicking Complete flips the task state badge to Done', async ({ page }) => {
    const { user: tenant } = loadTenants();

    // Seed a dedicated open task via REST so we don't mutate shared seeds.
    const title = `E2E Complete ${Date.now().toString(36)}`;
    const createRes = await fetch(`${API_BASE_URL}/tasks`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        accept: 'application/json',
        authorization: `Bearer ${tenant.accessToken}`,
      },
      body: JSON.stringify({ title, projectId: tenant.projectId }),
    });
    expect(createRes.ok).toBeTruthy();
    const { id: taskId } = (await createRes.json()) as { id: string };

    await injectAuth(page.context(), tenant);
    await page.goto(`/tasks/${taskId}`);

    // Wait for the detail page to render and the transitions panel to arrive.
    await expect(page.getByRole('heading', { name: title })).toBeVisible({ timeout: 10_000 });

    // The Complete button only exists in the Transitions card while the task
    // is in a state that permits it (open / review). Wait for it to land so
    // we know the detail page has fully hydrated.
    const completeButton = page.getByRole('button', { name: /^Complete$/ });
    await expect(completeButton).toBeVisible({ timeout: 10_000 });

    // Click the Complete transition.
    await completeButton.click();

    // After a successful POST the React Query cache invalidates, the badge
    // re-renders with "Done", and Complete is no longer a legal transition
    // from the new state — so the button disappears.
    await expect(completeButton).toHaveCount(0, { timeout: 10_000 });

    // The "Done" status label should now be visible somewhere on the page
    // (state badge in the sidebar). Scope to the sidebar's <aside> to avoid
    // matching unrelated "Done" strings in toasts or static copy.
    await expect(
      page
        .locator('aside')
        .getByText(/^Done$/)
        .first(),
    ).toBeVisible({
      timeout: 10_000,
    });
  });
});
