/**
 * Project sub-nav E2E.
 *
 * The project layout (`/workspaces/$id/projects/$projectId`) renders one
 * navigation with six links: Tasks / Gantt / Timeline / Overview / Members /
 * Settings. Members and Settings are their own routes, and the pages behind
 * Overview, Members and Settings carry no second tab strip of their own.
 */

import { expect, type Page, test } from '@playwright/test';

import enCommon from '../locales/en/common.json' with { type: 'json' };
import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';

const nav = enCommon.projects.nav;

function subNav(page: Page) {
  return page.getByRole('navigation', { name: nav.label });
}

test.describe('project sub-nav', () => {
  test('lists six links: tasks, gantt, timeline, overview, members, settings', async ({ page }) => {
    const { user2: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    await page.goto(`/workspaces/${tenant.workspaceId}/projects/${tenant.projectId}/tasks`);
    await expect(subNav(page)).toBeVisible({ timeout: 15_000 });

    await expect(subNav(page).getByRole('link')).toHaveText([
      nav.tasks,
      nav.gantt,
      nav.timeline,
      nav.overview,
      nav.members,
      nav.settings,
    ]);
  });

  test('walks every link by click and lands on its own URL', async ({ page }) => {
    const { user2: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    const base = `/workspaces/${tenant.workspaceId}/projects/${tenant.projectId}`;
    await page.goto(base);
    await expect(subNav(page)).toBeVisible({ timeout: 15_000 });

    const steps: Array<{ label: string; url: RegExp }> = [
      { label: nav.tasks, url: new RegExp(`${base}/tasks(/.*)?$`) },
      { label: nav.gantt, url: new RegExp(`${base}/gantt$`) },
      { label: nav.timeline, url: new RegExp(`${base}/timeline$`) },
      { label: nav.members, url: new RegExp(`${base}/members$`) },
      { label: nav.settings, url: new RegExp(`${base}/settings$`) },
      { label: nav.overview, url: new RegExp(`${base}$`) },
    ];
    for (const step of steps) {
      const link = subNav(page).getByRole('link', { name: step.label, exact: true });
      await link.click();
      await expect(page, `URL after clicking ${step.label}`).toHaveURL(step.url, {
        timeout: 10_000,
      });
      await expect(link).toHaveAttribute('aria-current', 'page');
      await expect(page.getByRole('main')).toBeVisible();
    }
  });

  test('overview, members and settings pages have no inner tablist', async ({ page }) => {
    const { user2: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    const base = `/workspaces/${tenant.workspaceId}/projects/${tenant.projectId}`;
    for (const [path, label] of [
      ['', nav.overview],
      ['/members', nav.members],
      ['/settings', nav.settings],
    ] as const) {
      await page.goto(`${base}${path}`);
      await expect(
        subNav(page).getByRole('link', { name: label, exact: true }),
        `${label} link is current`,
      ).toHaveAttribute('aria-current', 'page', { timeout: 15_000 });
      await expect(page.getByRole('main')).toBeVisible();
      await expect(page.getByRole('tablist'), `tablist on ${label}`).toHaveCount(0);
    }
  });

  test('legacy ?tab= query redirects to the matching route', async ({ page }) => {
    const { user2: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    const base = `/workspaces/${tenant.workspaceId}/projects/${tenant.projectId}`;
    const cases: Array<{ tab: string; url: RegExp; current: string }> = [
      { tab: 'settings', url: new RegExp(`${base}/settings$`), current: nav.settings },
      { tab: 'members', url: new RegExp(`${base}/members$`), current: nav.members },
      { tab: 'overview', url: new RegExp(`${base}$`), current: nav.overview },
    ];
    for (const c of cases) {
      await page.goto(`${base}?tab=${c.tab}`);
      await expect(page, `URL for ?tab=${c.tab}`).toHaveURL(c.url, { timeout: 15_000 });
      await expect(
        subNav(page).getByRole('link', { name: c.current, exact: true }),
      ).toHaveAttribute('aria-current', 'page', { timeout: 15_000 });
      await expect(page.getByRole('main')).toBeVisible();
    }
    // Settings page carries the archive action.
    await page.goto(`${base}?tab=settings`);
    await expect(
      page.getByRole('button', { name: enCommon.projects.archive.action, exact: true }),
    ).toBeVisible({ timeout: 15_000 });
  });

  test('opening a project from the workspace project list lands on its tasks', async ({ page }) => {
    const { user2: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    await page.goto(`/workspaces/${tenant.workspaceId}/projects`);
    const link = page
      .getByRole('main')
      .getByRole('link', { name: /E2E Project/ })
      .first();
    await expect(link).toBeVisible({ timeout: 15_000 });
    await link.click();

    await expect(page).toHaveURL(
      new RegExp(`/workspaces/${tenant.workspaceId}/projects/[^/]+/tasks(/.*)?$`),
      { timeout: 10_000 },
    );
    await expect(page.getByRole('main')).toBeVisible();
  });
});
