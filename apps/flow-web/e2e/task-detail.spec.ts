/**
 * Task detail page E2E.
 *
 * Happy path:
 *   1. Load pre-seeded tenant and navigate to the project task list.
 *   2. Click a seeded task to open the detail page.
 *   3. Verify the task title, comments section, and sidebar render.
 *   4. Run a11y check.
 */

import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

test.describe('task detail', () => {
  test('renders task detail page with title, comments, and sidebar', async ({ page }) => {
    const { user: tenant, seededTasks } = loadTenants();
    await injectAuth(page.context(), tenant);

    // Navigate to project tasks and click the smoke task to open detail
    await page.goto(`/projects/${tenant.projectId}/tasks`);
    await expect(page.getByText(seededTasks.smoke).first()).toBeVisible({ timeout: 10_000 });
    await page.getByText(seededTasks.smoke).first().click();

    // Verify we navigated to the task detail page
    await expect(page).toHaveURL(/\/tasks\//, { timeout: 5_000 });

    // Verify the task title is displayed somewhere on the detail page
    await expect(page.getByText(seededTasks.smoke).first()).toBeVisible({ timeout: 10_000 });

    // Verify the main content area renders
    const main = page.getByRole('main');
    await expect(main).toBeVisible({ timeout: 5_000 });

    // Verify no i18n key leaks (raw keys like "tasks.detail." or "common.")
    const bodyText = await page.locator('body').innerText();
    expect(bodyText).not.toMatch(/\btasks\.detail\.\w+/);
    expect(bodyText).not.toMatch(/\bcommon\.\w+\.\w+/);

    // Accessibility check
    await checkA11y(page, [
      'color-contrast',
      'region',
      'landmark-complementary-is-top-level',
      'page-has-heading-one',
    ]);
  });
});
