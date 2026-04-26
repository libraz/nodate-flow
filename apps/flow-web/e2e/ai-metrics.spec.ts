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
 *
 * Each test creates its own tenant via REST so the suite stays
 * parallel-safe.
 */

import { type Page, expect, test } from '@playwright/test';

import enAiMetrics from '../locales/en/aiMetrics.json' with { type: 'json' };
import { type TestTenant, cleanupTenant, createTestTenant, injectAuth } from './fixtures/tenant';

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
});
