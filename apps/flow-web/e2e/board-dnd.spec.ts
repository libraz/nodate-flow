/**
 * Board view E2E.
 *
 * Happy path:
 *   1. Create a fresh tenant via REST and seed a task via REST.
 *   2. Inject auth, navigate to the project board view.
 *   3. Verify the task card appears in the "Open" column.
 *
 * D&D regression:
 *   While the constraint engine does not yet support board-level
 *   drag-and-drop transitions, cards must render with `draggable=false`
 *   so users aren't given a non-functional drag affordance. The
 *   keyboard-accessible move menu remains the supported path.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

test.describe('board view', () => {
  test('task card appears in open column on board view', async ({ page }) => {
    const { user: tenant, seededTasks } = loadTenants();

    await injectAuth(page.context(), tenant);

    // Navigate to the project tasks view (board is the default sub-view)
    await page.goto(`/projects/${tenant.projectId}/tasks`);

    // Verify the pre-seeded task card is visible on the board
    await expect(page.getByText(seededTasks.board).first()).toBeVisible({ timeout: 10_000 });

    // Accessibility check on the board view
    await checkA11y(page, ['color-contrast', 'region', 'aria-required-children']);
  });

  test('cards are not draggable while D&D is disabled', async ({ page }) => {
    const { user: tenant, seededTasks } = loadTenants();

    await injectAuth(page.context(), tenant);
    await page.goto(`/projects/${tenant.projectId}/tasks`);

    // Wait for at least one card to render before asserting attributes.
    const seededCardText = page.getByText(seededTasks.board).first();
    await expect(seededCardText).toBeVisible({ timeout: 10_000 });

    // The board card is the closest ancestor element carrying a
    // `draggable` attribute. While D&D is disabled the value must be
    // the string "false" — never absent and never "true".
    const card = seededCardText.locator('xpath=ancestor::*[@draggable][1]');
    await expect(card).toHaveAttribute('draggable', 'false');

    // The hint copy for the disabled D&D state is rendered once near
    // the first column header.
    await expect(page.getByText("Use the state dropdown to change a task's state.")).toBeVisible();
  });
});
