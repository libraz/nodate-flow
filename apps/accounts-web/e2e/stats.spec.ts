/**
 * Instance Stats dashboard (G7) e2e tests.
 *
 * Covers /admin/stats — the instance-admin-only stats dashboard:
 *   - apps/accounts-web/src/routes/_authenticated/admin/stats.tsx
 *   - apps/accounts-web/src/features/admin-stats/api.ts
 *   - apps/accounts-web/src/features/admin-stats/kpi-card.tsx
 *   - apps/accounts-web/src/features/admin-stats/placeholder-section.tsx
 *
 * Cases:
 *   A. Renders title, both KPI cards (users + workspaces), Refresh
 *      button, and the placeholder section.
 *   B. KPI values come from `GET /admin/instance-stats` and are
 *      rendered through `Intl.NumberFormat` (locale-grouped digits).
 *   C. Refresh button triggers a refetch — observable via the
 *      "last refreshed" timestamp updating.
 *   D. The admin sub-nav exposes a `Stats` link that navigates to
 *      `/admin/stats`.
 *
 * Authorization (case E from the plan) is already covered by
 * `admin.spec.ts` — non-admins are redirected by the `_authenticated/admin`
 * layout regardless of which child route they hit.
 *
 * Uses the shared admin tenant from `global-setup`. Skips when admin
 * grant failed (a previous run already claimed the bootstrap admin).
 */

import { expect, test } from '@playwright/test';

import enAdmin from '../locales/en/admin.json' with { type: 'json' };
import enInstanceStats from '../locales/en/instanceStats.json' with { type: 'json' };
import { loadTenants } from './fixtures/load-tenants';
import { injectAuth } from './fixtures/tenant';

const copy = {
  pageTitle: enInstanceStats.page.title,
  pageSubtitle: enInstanceStats.page.subtitle,
  usersTitle: enInstanceStats.kpi.users.title,
  usersHelp: enInstanceStats.kpi.users.help,
  workspacesTitle: enInstanceStats.kpi.workspaces.title,
  workspacesHelp: enInstanceStats.kpi.workspaces.help,
  refresh: enInstanceStats.actions.refresh,
  refreshing: enInstanceStats.actions.refreshing,
  placeholderTitle: enInstanceStats.placeholder.title,
  placeholderBody: enInstanceStats.placeholder.body,
  navStats: enAdmin.nav.stats,
} as const;

/**
 * Returns the locale-grouped number that the KPI card will render
 * for the given count, mirroring `KpiCard.formatValue` which uses
 * `navigator.language`. The browser context locale defaults to
 * `en-US` under Playwright, so this matches the rendered text.
 */
function formatValue(n: number): string {
  return new Intl.NumberFormat('en-US').format(n);
}

test.describe('admin instance stats dashboard', () => {
  test.beforeEach(() => {
    const { adminGranted } = loadTenants();
    test.skip(!adminGranted, 'Admin grant failed — instance already has an admin from a prior run');
  });

  test('A: page renders title, both KPI cards, refresh button, and placeholder', async ({
    page,
  }) => {
    const { admin } = loadTenants();
    await injectAuth(page.context(), admin);
    await page.goto('/admin/stats');
    await page.waitForLoadState('networkidle');

    // Title + subtitle
    await expect(page.getByRole('heading', { name: copy.pageTitle, level: 1 })).toBeVisible();
    await expect(page.getByText(copy.pageSubtitle)).toBeVisible();

    // Both KPI card labels (uppercase-styled but rendered as text node)
    await expect(page.getByText(copy.usersTitle, { exact: true })).toBeVisible();
    await expect(page.getByText(copy.workspacesTitle, { exact: true })).toBeVisible();
    // KPI help texts further confirm the cards are mounted.
    await expect(page.getByText(copy.usersHelp)).toBeVisible();
    await expect(page.getByText(copy.workspacesHelp)).toBeVisible();

    // Refresh button (or its busy variant if data is still loading)
    const refreshButton = page.getByRole('button', {
      name: new RegExp(`^(${copy.refresh}|${copy.refreshing.replace(/…/, '…')})$`),
    });
    await expect(refreshButton).toBeVisible();

    // Placeholder section
    await expect(page.getByText(copy.placeholderTitle)).toBeVisible();
    await expect(page.getByText(copy.placeholderBody)).toBeVisible();
  });

  test('B: KPI values reflect /admin/instance-stats response with locale formatting', async ({
    page,
  }) => {
    const { admin } = loadTenants();
    await injectAuth(page.context(), admin);

    // Capture the actual API response so we can assert against the
    // numbers the server returned (no assumption about the absolute
    // counts in the test database).
    const statsPromise = page.waitForResponse(
      (res) => res.url().includes('/admin/instance-stats') && res.status() === 200,
    );
    await page.goto('/admin/stats');
    const statsRes = await statsPromise;
    const stats = (await statsRes.json()) as { totalUsers: number; totalWorkspaces: number };
    expect(typeof stats.totalUsers).toBe('number');
    expect(typeof stats.totalWorkspaces).toBe('number');

    await page.waitForLoadState('networkidle');

    // Wait for the placeholder dash to be replaced by a real value.
    // Using the help text to scope to each card avoids matching the
    // other tile when the two values happen to be equal.
    const usersCard = page
      .locator('div')
      .filter({ has: page.getByText(copy.usersTitle, { exact: true }) })
      .filter({ hasText: copy.usersHelp })
      .first();
    const workspacesCard = page
      .locator('div')
      .filter({ has: page.getByText(copy.workspacesTitle, { exact: true }) })
      .filter({ hasText: copy.workspacesHelp })
      .first();

    await expect(usersCard).toContainText(formatValue(stats.totalUsers));
    await expect(workspacesCard).toContainText(formatValue(stats.totalWorkspaces));
  });

  test('C: Refresh button triggers a refetch and updates the last-refreshed timestamp', async ({
    page,
  }) => {
    const { admin } = loadTenants();
    await injectAuth(page.context(), admin);
    await page.goto('/admin/stats');
    await page.waitForLoadState('networkidle');

    const refreshButton = page.getByRole('button', { name: copy.refresh });
    await expect(refreshButton).toBeVisible();
    await expect(refreshButton).toBeEnabled();

    // Capture the "last refreshed" output before clicking. The output
    // element is rendered as <output aria-live="polite"> next to the
    // Refresh button.
    const lastUpdatedOutput = page.locator('output[aria-live="polite"]');
    await expect(lastUpdatedOutput).toBeVisible();
    const beforeText = (await lastUpdatedOutput.textContent()) ?? '';

    // Click refresh and wait for the resulting network request.
    const refetch = page.waitForResponse(
      (res) => res.url().includes('/admin/instance-stats') && res.status() === 200,
    );
    await refreshButton.click();
    await refetch;

    // Either the button briefly went into the "Refreshing…" state, or
    // (more reliably across timing) the timestamp text changed once
    // the refetch settled. We assert the latter, which is the
    // user-visible signal the spec requires.
    await expect
      .poll(async () => (await lastUpdatedOutput.textContent()) ?? '', { timeout: 10_000 })
      .not.toBe(beforeText);
  });

  test('D: admin sub-nav Stats link navigates to /admin/stats', async ({ page }) => {
    const { admin } = loadTenants();
    await injectAuth(page.context(), admin);

    // Land on a sibling admin route so the sub-nav is rendered and
    // we are forced to use the link rather than the URL bar.
    await page.goto('/admin/users');
    await page.waitForLoadState('networkidle');

    const statsLink = page.getByRole('link', { name: copy.navStats, exact: true });
    await expect(statsLink).toBeVisible();
    await statsLink.click();

    await expect(page).toHaveURL(/\/admin\/stats$/);
    await expect(page.getByRole('heading', { name: copy.pageTitle, level: 1 })).toBeVisible();
  });
});
