/**
 * Project detail tab strip navigation E2E.
 *
 * The project layout (`/workspaces/$id/projects/$projectId`) renders a
 * tab strip with Overview / Tasks / Gantt / Timeline. Existing tests
 * exercise individual destinations (gantt.spec.ts, timeline.spec.ts) by
 * jumping directly to URLs; none drive the tab strip with clicks. A
 * regression where a tab's `to` prop or `params` shape changes ships
 * green under that pattern.
 *
 * This spec lands on the project overview, then walks the four tabs by
 * click and asserts both the URL and the main content area mount.
 */

import { expect, test } from '@playwright/test';

import enCommon from '../locales/en/common.json' with { type: 'json' };
import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';

test.describe('project tab strip', () => {
  test('navigates between overview, tasks, gantt and timeline via clicks', async ({ page }) => {
    const { user2: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    const projectBase = `/workspaces/${tenant.workspaceId}/projects/${tenant.projectId}`;

    await page.goto(projectBase);
    await expect(page.getByRole('main')).toBeVisible({ timeout: 15_000 });

    const tabStrip = page.getByRole('navigation', { name: enCommon.projects.nav.label });
    await expect(tabStrip).toBeVisible();

    // Tasks
    await tabStrip.getByRole('link', { name: enCommon.projects.nav.tasks, exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`${projectBase}/tasks(/.*)?$`), { timeout: 10_000 });
    await expect(page.getByRole('main')).toBeVisible();

    // Gantt
    await tabStrip.getByRole('link', { name: enCommon.projects.nav.gantt, exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`${projectBase}/gantt$`), { timeout: 10_000 });
    await expect(page.getByRole('main')).toBeVisible();

    // Timeline
    await tabStrip.getByRole('link', { name: enCommon.projects.nav.timeline, exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`${projectBase}/timeline$`), { timeout: 10_000 });
    await expect(page.getByRole('main')).toBeVisible();

    // Overview (return)
    await tabStrip.getByRole('link', { name: enCommon.projects.nav.overview, exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`${projectBase}$`), { timeout: 10_000 });
    await expect(page.getByRole('main')).toBeVisible();
  });
});
