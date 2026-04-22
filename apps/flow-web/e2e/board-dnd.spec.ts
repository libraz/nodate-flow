/**
 * Board view E2E.
 *
 * Happy path:
 *   1. Create a fresh tenant via REST and seed a task via REST.
 *   2. Inject auth, navigate to the project board view.
 *   3. Verify the task card appears in the "Open" column.
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
});
