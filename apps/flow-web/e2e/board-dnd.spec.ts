/**
 * Board view E2E.
 *
 * Happy path:
 *   1. Create a fresh tenant via REST and seed a task via REST.
 *   2. Inject auth, navigate to the project board view.
 *   3. Verify the task card appears in the "Open" column.
 */

import { expect, test } from '@playwright/test';

import {
  type TestTenant,
  cleanupTenant,
  createTask,
  createTestTenant,
  injectAuth,
} from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

test.describe('board view', () => {
  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('task card appears in open column on board view', async ({ page }) => {
    tenant = await createTestTenant();

    const taskTitle = `Board Task ${Date.now()}`;
    await createTask(tenant, taskTitle);

    await injectAuth(page.context(), tenant);

    // Navigate to the project board view
    await page.goto(`/projects/${tenant.projectId}/board`);

    // Verify we are on the board view
    await expect(
      page.getByRole('heading', { name: /board/i }).or(page.locator('[data-testid="board-view"]')),
    ).toBeVisible({ timeout: 10_000 });

    // Find the Open column and verify the task card is inside it
    const openColumn = page
      .getByRole('region', { name: /open/i })
      .or(page.locator('[data-column="open"]'));
    await expect(openColumn.getByText(taskTitle)).toBeVisible({ timeout: 10_000 });

    // Accessibility check on the board view
    await checkA11y(page);
  });
});
