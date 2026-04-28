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

    // After a successful POST the React Query cache invalidates and the
    // sidebar state badge re-renders with "Done". This is the primary
    // signal that the transition landed end-to-end. Scope to the
    // sidebar's <aside> and to the row that contains the "State" label
    // so we don't match unrelated "Done" strings (toasts, static copy,
    // or other state-graph labels).
    const sidebar = page.locator('aside');
    const stateLabel = sidebar.getByText(/^State$/);
    await expect(stateLabel).toBeVisible({ timeout: 10_000 });
    const stateRow = stateLabel.locator('..');
    await expect(stateRow.getByText(/^Done$/).first()).toBeVisible({ timeout: 10_000 });

    // Complete is no longer a legal transition from the Done state, so
    // the Transitions card should hide the button as well — assert via
    // toBeHidden so the assertion semantics match the user-visible state.
    await expect(completeButton).toBeHidden();
  });
});
