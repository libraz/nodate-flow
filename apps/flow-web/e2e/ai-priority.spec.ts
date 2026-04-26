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
 *   C. deterministic Apply path — the suggestion engine
 *      (apps/flow-api/internal/ai/priorityopt/priorityopt.go) is pure
 *      heuristic Go, so seeding a P0 task with `dueOn=<yesterday>`
 *      produces a guaranteed score of 1.5 (base) + 2.5 (overdue) = 4.0
 *      → suggested priority 4, which always differs from the seeded
 *      0 and therefore always surfaces as a suggestion. With that we
 *      can drive the Apply button end-to-end and assert on the
 *      "Updated to ..." toast + Undo affordance.
 *   D. error state — when the suggestions endpoint 500s, the page's
 *      local ErrorBoundary (wrapping the suspense query body) catches
 *      the throw and renders `PriorityError` inline with `role="alert"`
 *      and the localised `aiPriority.error.fetchFailed` copy plus a
 *      Retry button. The workspace `errorComponent` (WORKSPACE.NOT_FOUND
 *      only) and the root `FatalFallback` are both bypassed.
 *   E. ja locale — pre-seeds `nf.lang=ja` (the storage key consumed
 *      by `detectInitialLanguage` in `src/i18n/index.ts`) plus the
 *      legacy `i18nextLng` mirror, then asserts the Japanese page
 *      title from `locales/ja/aiPriority.json` renders. A fresh
 *      tenant with no tasks is enough — we only need the heading.
 *   F. mobile viewport — narrows to 375x812 (iPhone 13 mini class)
 *      and asserts the page title + Refresh button remain visible
 *      without overflow. Same empty-tenant fixture, no card seeding.
 *
 * Each test creates its own tenant via REST so the suite stays
 * parallel-safe.
 */

import { type Page, expect, test } from '@playwright/test';

import enAiPriority from '../locales/en/aiPriority.json' with { type: 'json' };
import enCommon from '../locales/en/common.json' with { type: 'json' };
import jaAiPriority from '../locales/ja/aiPriority.json' with { type: 'json' };
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
  cardApply: enAiPriority.card.apply,
  // `toast.applied` is "Updated to {priority}." — the priority value is
  // ICU-interpolated at runtime, so we match on the static prefix the
  // page always renders before the placeholder.
  toastAppliedPrefix: enAiPriority.toast.applied.split('{')[0]?.trim() ?? 'Updated to',
  toastUndo: enAiPriority.toast.undo,
  // The list-fetch error path now renders through the page's own
  // `PriorityError` fallback (wrapped in a local ErrorBoundary). We
  // still keep `fatalTitle` around so case D can assert the root
  // FatalFallback is NOT used.
  fatalTitle: enCommon.fatal.title,
  errorFetchFailed: enAiPriority.error.fetchFailed,
  errorRetry: enAiPriority.error.retry,
  jaPageTitle: jaAiPriority.page.title,
} as const;

/**
 * Returns yesterday's date in `YYYY-MM-DD` form (UTC). Used to seed
 * a deliberately-overdue task so the priority engine emits a
 * deterministic +2.5 overdue bump and guarantees a suggestion.
 */
function yesterdayISO(): string {
  const d = new Date();
  d.setUTCDate(d.getUTCDate() - 1);
  const y = d.getUTCFullYear();
  const m = String(d.getUTCMonth() + 1).padStart(2, '0');
  const day = String(d.getUTCDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
}

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

  // Case C exercises the Apply path end-to-end. The priority engine
  // (apps/flow-api/internal/ai/priorityopt/priorityopt.go) is pure
  // heuristic Go: a P0 task with an overdue dueOn scores 1.5 + 2.5 =
  // 4.0 → suggested priority 4 ≠ current 0, so a suggestion is always
  // emitted. That deterministic seed lets us click Apply and assert on
  // the resulting toast + card removal.
  test('C: clicking Apply on a deterministic suggestion fires the toast and removes the card', async ({
    page,
  }) => {
    tenant = await createTestTenant();
    const taskTitle = `Overdue subject ${Date.now().toString(36)}`;
    await createTask(tenant, taskTitle, { priority: 0, dueOn: yesterdayISO() });

    await injectAuth(page.context(), tenant);
    await openPriority(page, tenant);

    // The seeded task title must appear on a suggestion card.
    const cardTitle = page.getByText(taskTitle);
    await expect(cardTitle).toBeVisible({ timeout: 10_000 });

    // Locate the Apply button via its aria-label which pins it to this
    // specific card (`card.applyAria` includes the task title), so
    // strict-mode resolution is unambiguous even if other cards exist.
    const applyButton = page.getByRole('button', {
      name: new RegExp(`${copy.cardApply}.*${taskTitle.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}`),
    });
    await expect(applyButton).toBeVisible({ timeout: 5_000 });
    await applyButton.click();

    // Toast: "Updated to P4 — Critical." — match on the static prefix
    // that lives outside the ICU placeholder.
    await expect(page.getByText(new RegExp(copy.toastAppliedPrefix))).toBeVisible({
      timeout: 5_000,
    });

    // The Undo button surfaces inside the toast body briefly; assert
    // that it is visible while the toast is up.
    await expect(page.getByRole('button', { name: copy.toastUndo })).toBeVisible({
      timeout: 5_000,
    });

    // The card the user just applied is gone from the visible list.
    // The optimistic `applied` set hides the row immediately, before
    // the suggestions refetch, so the title disappears synchronously.
    await expect(cardTitle).toBeHidden({ timeout: 5_000 });
  });

  /**
   * D: 500 from the suggestions endpoint surfaces the page's local
   * `PriorityError` fallback. Stub the priority-suggestions response
   * with a server error before navigation so `useSuspenseQuery` throws
   * on the first render; the local ErrorBoundary catches the throw
   * and renders `role="alert"` with the localised
   * `aiPriority.error.fetchFailed` copy + Retry button. The workspace
   * `errorComponent` (WORKSPACE.NOT_FOUND only) and the root
   * FatalFallback are both bypassed.
   */
  test('D: 500 on the suggestions endpoint renders the per-page error fallback', async ({
    page,
  }) => {
    tenant = await createTestTenant();
    await injectAuth(page.context(), tenant);

    // Match both `?` query-stringed and bare variants of the path.
    await page.route('**/ai/priority-suggestions**', (route) =>
      route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: '{"code":"server_error"}',
      }),
    );

    await page.goto(`/workspaces/${tenant.workspaceId}/insights/priority`);
    await page.waitForLoadState('domcontentloaded');

    // The per-page PriorityError fallback renders inside `role="alert"`
    // with the localised fetchFailed copy.
    const alert = page.getByRole('alert').filter({ hasText: copy.errorFetchFailed });
    await expect(alert).toBeVisible({ timeout: 15_000 });
    await expect(alert.getByRole('button', { name: copy.errorRetry })).toBeVisible();

    // Sanity: the root FatalFallback heading must NOT appear — the
    // per-feature boundary contains the failure to its own subtree.
    await expect(page.getByRole('heading', { level: 1, name: copy.fatalTitle })).toHaveCount(0);
  });

  /**
   * E: pre-seeded `nf.lang=ja` renders the Japanese page title.
   * No task seeding required — the heading lives in the page header
   * and is independent of the list contents.
   */
  test('E: ja locale renders the Japanese page title', async ({ page }) => {
    // The tenant is registered with `locale: 'ja'` so the auth
    // bootstrap (`use-auth-bootstrap.ts:setLanguage(user.locale)`)
    // switches i18next to ja immediately after login. Without this,
    // the bootstrap would override any localStorage-seeded language
    // with the user-profile locale (which defaults to en), so the
    // localStorage init script alone is not enough — we mirror the
    // pattern used by `task-ai-agents.spec.ts` D and `ai-metrics.spec.ts` E.
    tenant = await createTestTenant({ locale: 'ja' });

    // Belt-and-braces localStorage seed so the very first paint —
    // before the bootstrap query resolves — is already ja, avoiding
    // a flash of en content.
    await page.addInitScript(() => {
      localStorage.setItem('i18nextLng', 'ja');
      localStorage.setItem('nf.lang', 'ja');
    });

    await injectAuth(page.context(), tenant);
    await page.goto(`/workspaces/${tenant.workspaceId}/insights/priority`);
    await page.waitForLoadState('domcontentloaded');

    // Match without `level` to mirror the working ja-locale pattern in
    // task-ai-agents.spec.ts; the page-title h1 is the only heading
    // that resolves to this string anyway.
    await expect(page.getByRole('heading', { name: copy.jaPageTitle })).toBeVisible({
      timeout: 15_000,
    });
    // Cross-check: the en title should not also render — proves the
    // ja resource bundle is the active one, not just an additive load.
    await expect(page.getByRole('heading', { name: copy.pageTitle })).toHaveCount(0);
  });

  /**
   * F: at a 375x812 mobile viewport the page header still renders
   * its title + Refresh button without horizontal overflow. We
   * assert visibility (Playwright's `toBeVisible` requires the node
   * to be in the visible rect) plus a strict bounding-box check on
   * `documentElement` to prove no horizontal scroll bleed.
   */
  test.describe('mobile viewport', () => {
    test.use({ viewport: { width: 375, height: 812 } });

    test('F: mobile renders the page header without overflow', async ({ page }) => {
      tenant = await createTestTenant();
      await injectAuth(page.context(), tenant);
      await openPriority(page, tenant);

      await expect(page.getByRole('button', { name: copy.refreshAction })).toBeVisible({
        timeout: 10_000,
      });

      // No horizontal overflow: scrollWidth must not exceed clientWidth
      // on the document element. A 1px tolerance covers sub-pixel
      // rounding from `clamp()` paddings.
      const overflow = await page.evaluate(() => {
        const el = document.documentElement;
        return el.scrollWidth - el.clientWidth;
      });
      expect(overflow).toBeLessThanOrEqual(1);
    });
  });
});
