/**
 * AI Metrics dashboard E2E (G1 — `/workspaces/{wsId}/settings/ai/metrics`).
 *
 * Smoke-level coverage for the workspace-scoped AI telemetry surface.
 * The page lays out three KPI cards (Proposed / Applied / Dismissed),
 * an acceptance-rate card, the per-provider outbound rate-limit table,
 * and a window selector segmented control. The active window is
 * reflected in the `?windowDays=` URL search param.
 *
 * Cases:
 *   A. golden-path render — page mounts with the three KPI cards,
 *      acceptance-rate card, and the outbound section header.
 *   B. switching the window selector to 7d updates the URL search
 *      param to `?windowDays=7`; switching back to 30 (default) drops
 *      the param.
 *   C. a fresh tenant has no provider rate-limit history yet, so the
 *      outbound table renders the "no provider limits" empty state
 *      copy.
 *   D. when the metrics endpoint 500s, the local ErrorBoundary mounts
 *      <MetricsError> instead of collapsing to the root FatalFallback.
 *   E. with the user locale set to ja, the page title resolves through
 *      the aiMetrics namespace in Japanese (namespace registration +
 *      key resolution smoke).
 *   F. on a mobile viewport (375x812) the page title and all three KPI
 *      labels remain visible (i.e. nothing gets clipped off-screen).
 *
 * Each test creates its own tenant via REST so the suite stays
 * parallel-safe.
 */

import { expect, type Page, test } from '@playwright/test';

import enAiMetrics from '../locales/en/aiMetrics.json' with { type: 'json' };
import jaAiMetrics from '../locales/ja/aiMetrics.json' with { type: 'json' };
import {
  API_BASE_URL,
  cleanupTenant,
  createTestTenant,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';

const copy = {
  pageTitle: enAiMetrics.page.title,
  proposed: enAiMetrics.kpi.proposed,
  applied: enAiMetrics.kpi.applied,
  dismissed: enAiMetrics.kpi.dismissed,
  acceptanceRate: enAiMetrics.kpi.acceptanceRate,
  outboundTitle: enAiMetrics.outbound.title,
  outboundEmpty: enAiMetrics.outbound.empty,
  window7d: enAiMetrics.window['7d'],
  window30d: enAiMetrics.window['30d'],
  window90d: enAiMetrics.window['90d'],
  errorFetchFailed: enAiMetrics.error.fetchFailed,
  errorRetry: enAiMetrics.error.retry,
  pageTitleJa: jaAiMetrics.page.title,
} as const;

async function openMetrics(page: Page, tenant: TestTenant): Promise<void> {
  await page.goto(`/workspaces/${tenant.workspaceId}/settings/ai/metrics`);
  await page.waitForLoadState('domcontentloaded');
  await expect(page.getByRole('heading', { level: 1, name: copy.pageTitle })).toBeVisible({
    timeout: 15_000,
  });
}

test.describe('ai metrics dashboard', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('A: renders the three KPI cards, acceptance card, and outbound section', async ({
    page,
  }) => {
    tenant = await createTestTenant();
    await injectAuth(page.context(), tenant);
    await openMetrics(page, tenant);

    // KPI card labels are plain `<span>` text inside KpiCard. Numeric
    // values depend on workspace state, so assert on the label only.
    await expect(page.getByText(copy.proposed)).toBeVisible({ timeout: 10_000 });
    await expect(page.getByText(copy.applied)).toBeVisible();
    await expect(page.getByText(copy.dismissed)).toBeVisible();
    await expect(page.getByText(copy.acceptanceRate)).toBeVisible();
    // Outbound section header — `<h2>` with the title copy.
    await expect(page.getByRole('heading', { name: copy.outboundTitle })).toBeVisible();
  });

  test('B: switching the window selector updates the ?windowDays= URL param', async ({ page }) => {
    tenant = await createTestTenant();
    await injectAuth(page.context(), tenant);
    await openMetrics(page, tenant);

    // 30 is DEFAULT_WINDOW so the route omits the query string at
    // mount. Switch to 7 days — the route should rewrite the URL with
    // `?windowDays=7`.
    await page.getByRole('radio', { name: copy.window7d }).click();
    await expect(page).toHaveURL(/[?&]windowDays=7\b/, { timeout: 5_000 });

    // Switch to 90 days — `?windowDays=90`.
    await page.getByRole('radio', { name: copy.window90d }).click();
    await expect(page).toHaveURL(/[?&]windowDays=90\b/, { timeout: 5_000 });

    // Switch back to the default window — the param should drop out.
    await page.getByRole('radio', { name: copy.window30d }).click();
    await expect(page).not.toHaveURL(/[?&]windowDays=/, { timeout: 5_000 });
  });

  test('C: a fresh workspace shows the "no provider limits" empty state', async ({ page }) => {
    tenant = await createTestTenant();
    await injectAuth(page.context(), tenant);
    await openMetrics(page, tenant);

    // A brand-new tenant has not invoked any AI provider yet, so the
    // outbound table replaces itself with the empty-state panel.
    await expect(page.getByText(copy.outboundEmpty)).toBeVisible({ timeout: 10_000 });
  });

  /**
   * D: when GET /ai/metrics 500s, the local ErrorBoundary mounts
   * <MetricsError> with role="alert" and the i18n fetchFailed copy,
   * instead of bubbling to the root FatalFallback.
   */
  test('D: 500 from /ai/metrics renders the inline error panel', async ({ page }) => {
    tenant = await createTestTenant();
    await injectAuth(page.context(), tenant);

    // Fault-inject BEFORE goto so the very first metrics fetch fails
    // and the ErrorBoundary mounts instead of MetricsDashboard. Scope
    // the matcher to the flow-api base URL so the SPA navigation
    // request (which also contains "/ai/metrics" in its path) is not
    // intercepted.
    await page.route(`${API_BASE_URL}/workspaces/*/ai/metrics**`, (route) =>
      route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: '{"code":"server_error"}',
      }),
    );

    await page.goto(`/workspaces/${tenant.workspaceId}/settings/ai/metrics`);
    await page.waitForLoadState('domcontentloaded');

    // Header (page title) lives outside the error boundary, so it
    // still renders. The error panel itself is role="alert" with
    // the fetchFailed copy and a Retry button.
    await expect(page.getByRole('heading', { level: 1, name: copy.pageTitle })).toBeVisible({
      timeout: 15_000,
    });
    const alert = page.getByRole('alert');
    await expect(alert).toBeVisible({ timeout: 10_000 });
    await expect(alert).toContainText(copy.errorFetchFailed);
    await expect(page.getByRole('button', { name: copy.errorRetry })).toBeVisible();
  });

  /**
   * E: with the user locale = ja the page title resolves through the
   * aiMetrics namespace in Japanese. Verifies namespace registration +
   * key resolution; not a full ja recreation of case A.
   */
  test('E: ja locale resolves the aiMetrics namespace', async ({ page }) => {
    // The auth bootstrap hard-syncs i18next to `profile.locale` on
    // /me, so the tenant has to be created with locale 'ja' to make
    // it stick. The localStorage seed below is belt-and-suspenders so
    // the very first paint (pre-bootstrap) is also ja.
    tenant = await createTestTenant({ locale: 'ja' });
    await injectAuth(page.context(), tenant);

    await page.addInitScript(() => {
      localStorage.setItem('i18nextLng', 'ja');
      localStorage.setItem('nf.lang', 'ja');
    });

    await page.goto(`/workspaces/${tenant.workspaceId}/settings/ai/metrics`);
    await page.waitForLoadState('domcontentloaded');

    await expect(page.getByRole('heading', { level: 1, name: copy.pageTitleJa })).toBeVisible({
      timeout: 15_000,
    });
  });
});

test.describe('ai metrics dashboard — mobile viewport', () => {
  // `test.use` is per-describe in Playwright, so the mobile case lives
  // in its own block instead of overriding `viewport` per-test.
  test.use({ viewport: { width: 375, height: 812 } });

  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  /**
   * F: at 375x812 the page title and all three KPI labels are still
   * visible (none clipped off-screen / collapsed). Visibility-only;
   * pixel-perfect layout is out of scope for this case.
   */
  test('F: mobile renders KPI cards without overflow', async ({ page }) => {
    tenant = await createTestTenant();
    await injectAuth(page.context(), tenant);

    await page.goto(`/workspaces/${tenant.workspaceId}/settings/ai/metrics`);
    await page.waitForLoadState('domcontentloaded');

    await expect(page.getByRole('heading', { level: 1, name: copy.pageTitle })).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.getByText(copy.proposed)).toBeVisible({ timeout: 10_000 });
    await expect(page.getByText(copy.applied)).toBeVisible();
    await expect(page.getByText(copy.dismissed)).toBeVisible();
  });
});
