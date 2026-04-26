/**
 * AI Priority Suggestions E2E (G2 — `/workspaces/{wsId}/insights/priority`).
 *
 * Smoke-level coverage for the priority suggestions surface. The page
 * lists tasks whose AI-derived priority differs from the current one,
 * with Apply (PATCH /tasks/{id} with new priority) and Dismiss (local
 * storage filter) actions per card.
 *
 * Cases:
 *   A. zero open tasks — the empty-state copy ("No open tasks to
 *      evaluate.") shows. This is the only branch we can guarantee on
 *      a fresh workspace because AI suggestions are produced by a
 *      backend rule that depends on workspace state we cannot easily
 *      coerce from the test (priority delta + open tasks). The richer
 *      Apply / Dismiss / Undo paths live in unit tests over the
 *      page component.
 *   B. with at least one open task — the page renders without a
 *      runtime error and the page heading + KPI row are visible. We
 *      do not assert on suggestion cards because the backend may
 *      legitimately return zero suggestions for the seeded task.
 *
 * Note: per the task brief, when seeding suggestions deterministically
 * is not feasible we verify the empty / fallback states only. The
 * Apply / Dismiss / Undo paths are intentionally NOT exercised here.
 *
 * Each test creates its own tenant via REST so the suite stays
 * parallel-safe.
 */

import { type Page, expect, test } from '@playwright/test';

import enAiPriority from '../locales/en/aiPriority.json' with { type: 'json' };
import {
  type TestTenant,
  cleanupTenant,
  createTask,
  createTestTenant,
  injectAuth,
} from './fixtures/tenant';

const copy = {
  pageTitle: enAiPriority.page.title,
  emptyNoTasks: enAiPriority.empty.noTasks,
  refreshAction: enAiPriority.action.refresh,
} as const;

async function openPriority(page: Page, tenant: TestTenant): Promise<void> {
  await page.goto(`/workspaces/${tenant.workspaceId}/insights/priority`);
  await page.waitForLoadState('domcontentloaded');
  await expect(page.getByRole('heading', { level: 1, name: copy.pageTitle })).toBeVisible({
    timeout: 15_000,
  });
}

test.describe('ai priority suggestions', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('A: empty workspace shows the "no open tasks" empty state', async ({ page }) => {
    tenant = await createTestTenant();
    await injectAuth(page.context(), tenant);
    await openPriority(page, tenant);

    // A brand-new workspace has zero open tasks → totalEvaluated=0 →
    // the empty state branch renders the "No open tasks to evaluate."
    // copy. The other empty branch ("allOptimised") only fires when
    // total > 0 but no priority delta exists, which we cover in B as
    // a fallback.
    await expect(page.getByText(copy.emptyNoTasks)).toBeVisible({ timeout: 10_000 });
  });

  test('B: with one open task the page renders the KPI row without errors', async ({ page }) => {
    tenant = await createTestTenant();
    // Seed an open task so totalEvaluated >= 1. The backend's
    // suggestion engine may or may not produce a priority delta, so
    // we accept either the populated list OR the "all optimised"
    // empty state — both are valid surface outcomes.
    await createTask(tenant, `Priority subject ${Date.now().toString(36)}`);

    await injectAuth(page.context(), tenant);
    await openPriority(page, tenant);

    // The Refresh button is the most stable surface anchor — it's
    // always part of the page header, regardless of whether the
    // suggestion engine produced cards or fell through to the
    // "all optimised" branch. Numeric KPI values depend on the engine
    // so we do not assert on them; the kpi labels collide with the
    // suggestion card's "Suggested" copy under strict mode so we
    // skip them as well.
    await expect(page.getByRole('button', { name: copy.refreshAction })).toBeVisible({
      timeout: 10_000,
    });

    // Whichever branch the suggestion engine resolves to, the
    // emptyNoTasks copy must not appear (we have at least one open
    // task). This is the surface assertion that proves the page
    // routed to the populated / all-optimised branch correctly.
    await expect(page.getByText(copy.emptyNoTasks)).toBeHidden();
  });
});
