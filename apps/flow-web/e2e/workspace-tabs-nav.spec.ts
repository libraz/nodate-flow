/**
 * Workspace detail tab strip navigation E2E.
 *
 * The workspace detail layout (`/workspaces/$id`) renders a horizontal
 * sub-nav with Overview / Projects / Timeline / Settings. Each link
 * routes to a different child segment. A regression where one of those
 * `to` props points at a stale path or where the link disappears under
 * a wrong condition currently ships green because no spec drives the
 * tab strip with clicks.
 *
 * This test covers each tab's click behaviour and asserts both the URL
 * change and that the route's main content actually mounts.
 */

import { expect, test } from '@playwright/test';

import enCommon from '../locales/en/common.json' with { type: 'json' };
import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';

test.describe('workspace tab strip', () => {
  test('navigates to projects, timeline, and settings via tab clicks', async ({ page }) => {
    const { user2: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    await page.goto(`/workspaces/${tenant.workspaceId}`);
    await expect(page.getByRole('main')).toBeVisible({ timeout: 15_000 });

    const tabStrip = page.getByRole('navigation', { name: enCommon.workspaces.nav.label });
    await expect(tabStrip).toBeVisible();

    // Projects tab → /workspaces/$id/projects
    await tabStrip
      .getByRole('link', { name: enCommon.workspaces.nav.projects, exact: true })
      .click();
    await expect(page).toHaveURL(new RegExp(`/workspaces/${tenant.workspaceId}/projects$`), {
      timeout: 10_000,
    });
    await expect(page.getByRole('main')).toBeVisible();

    // Timeline tab → /workspaces/$id/timeline
    await tabStrip
      .getByRole('link', { name: enCommon.workspaces.nav.timeline, exact: true })
      .click();
    await expect(page).toHaveURL(new RegExp(`/workspaces/${tenant.workspaceId}/timeline$`), {
      timeout: 10_000,
    });
    await expect(page.getByRole('main')).toBeVisible();

    // Settings tab → /workspaces/$id/settings (any sub-page is fine; the
    // index either renders a hub or the settings layout redirects to a
    // default child).
    await tabStrip
      .getByRole('link', { name: enCommon.workspaces.nav.settings, exact: true })
      .click();
    await expect(page).toHaveURL(new RegExp(`/workspaces/${tenant.workspaceId}/settings(/.*)?$`), {
      timeout: 10_000,
    });
    await expect(page.getByRole('main')).toBeVisible();

    // Back to Overview → /workspaces/$id
    await tabStrip
      .getByRole('link', { name: enCommon.workspaces.nav.overview, exact: true })
      .click();
    await expect(page).toHaveURL(new RegExp(`/workspaces/${tenant.workspaceId}(\\?.*)?$`), {
      timeout: 10_000,
    });
    await expect(page.getByRole('main')).toBeVisible();
  });
});
