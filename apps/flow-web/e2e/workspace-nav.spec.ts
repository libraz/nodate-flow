/**
 * Sidebar workspace navigation E2E.
 *
 * The sidebar `navigation` landmark is the only navigation surface for a
 * workspace (the old tab strip is gone). This spec drives the Projects,
 * Members & invites, Overdue & due soon and Workspace settings links, checks
 * that `/workspaces/$id` is replaced by `/workspaces/$id/projects`, and that
 * the project list header shows the workspace description.
 *
 * A fresh tenant per test keeps the description edit isolated.
 */

import { expect, test } from '@playwright/test';

import enCommon from '../locales/en/common.json' with { type: 'json' };
import {
  AUTH_API_URL,
  cleanupTenant,
  createTestTenant,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';

async function setDescription(tenant: TestTenant, description: string): Promise<void> {
  const response = await fetch(`${AUTH_API_URL}/workspaces/${tenant.workspaceId}`, {
    method: 'PATCH',
    headers: {
      'Content-Type': 'application/json',
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
    body: JSON.stringify({ description }),
  });
  if (!response.ok) {
    throw new Error(`PATCH /workspaces/{wsId} -> ${response.status} ${await response.text()}`);
  }
}

test.describe('workspace sidebar navigation', () => {
  test('/workspaces/$id redirects to projects and shows the workspace description', async ({
    page,
  }) => {
    const tenant = await createTestTenant({ locale: 'en' });
    try {
      const description = `Quarterly planning ${tenant.workspaceSlug}`;
      await setDescription(tenant, description);
      await injectAuth(page.context(), tenant);

      await page.goto(`/workspaces/${tenant.workspaceId}`);
      await expect(page).toHaveURL(new RegExp(`/workspaces/${tenant.workspaceId}/projects$`), {
        timeout: 15_000,
      });
      await expect(page.getByText(description, { exact: true })).toBeVisible({ timeout: 10_000 });

      // The tab strip is gone: the page content has no tabs or navigation of its own.
      await expect(page.getByRole('main').getByRole('tablist')).toHaveCount(0);
      await expect(page.getByRole('main').getByRole('navigation')).toHaveCount(0);
    } finally {
      await cleanupTenant(tenant);
    }
  });

  test('navigates via sidebar links to members, reminders, settings and projects', async ({
    page,
  }) => {
    const tenant = await createTestTenant({ locale: 'en' });
    try {
      await injectAuth(page.context(), tenant);
      await page.goto(`/workspaces/${tenant.workspaceId}/projects`);
      await expect(page.getByRole('main')).toBeVisible({ timeout: 15_000 });

      const sidebar = page.getByRole('navigation', { name: enCommon.nav.primary });
      await expect(sidebar).toBeVisible();
      const ws = tenant.workspaceId;

      await sidebar.getByRole('link', { name: enCommon.nav.members, exact: true }).click();
      await expect(page).toHaveURL(new RegExp(`/workspaces/${ws}/members$`), { timeout: 10_000 });
      await expect(page.getByRole('main')).toBeVisible();

      await sidebar.getByRole('link', { name: enCommon.nav.reminders, exact: true }).click();
      await expect(page).toHaveURL(new RegExp(`/workspaces/${ws}/reminders$`), {
        timeout: 10_000,
      });
      await expect(page.getByRole('main')).toBeVisible();

      await sidebar
        .getByRole('link', { name: enCommon.nav.workspaceSettings, exact: true })
        .click();
      await expect(page).toHaveURL(new RegExp(`/workspaces/${ws}/settings(/.*)?$`), {
        timeout: 10_000,
      });
      await expect(page.getByRole('main')).toBeVisible();

      await sidebar.getByRole('link', { name: enCommon.nav.projects, exact: true }).click();
      await expect(page).toHaveURL(new RegExp(`/workspaces/${ws}/projects$`), { timeout: 10_000 });
      await expect(page.getByRole('main')).toBeVisible();
    } finally {
      await cleanupTenant(tenant);
    }
  });
});
