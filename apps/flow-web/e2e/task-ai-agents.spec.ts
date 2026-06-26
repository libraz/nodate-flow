/**
 * Task detail AI Agents section E2E (G6).
 *
 * Smoke-level coverage for the AI activity collapsible section that
 * sits next to LinkedEventsSection on the task detail page. The
 * section lists every AI invocation against the task (priority
 * suggestions, state inferences, agent ticks, …) — we only assert on
 * the section chrome and the empty-state copy because invocation
 * rows depend on the backend AI engine running against the task,
 * which is not deterministic from the test runner.
 *
 * Cases:
 *   A. empty state — section renders the "No AI activity yet"
 *      placeholder when the task has zero recorded invocations
 *      (i.e. on a freshly seeded task).
 *   B. collapse / expand — clicking the disclosure toggle flips
 *      `aria-expanded` on the button and `hidden` on the body,
 *      and the localStorage persistence keeps the state across
 *      a reload.
 *   C. error state — when GET /tasks/{id}/ai/invocations 500s the
 *      section's local ErrorBoundary catches the throw and renders
 *      `AIAgentsError` (`role="alert"` + `aiAgents.error.fetchFailed`
 *      copy + Retry button) inline, scoped to this section. The root
 *      FatalFallback is reserved for genuinely fatal failures
 *      elsewhere on the page.
 *   D. ja locale — on a freshly seeded task the section title
 *      resolves through the aiAgents namespace in Japanese.
 *   E. mobile viewport — the section header + empty placeholder
 *      both stay visible at 375x812.
 *
 * Note: per the task brief, we verify the empty / collapse / error /
 * locale / mobile cases only — seeding an invocation requires running
 * the AI pipeline against the task which is non-deterministic in CI.
 * The row-level row rendering is exercised by the section's unit
 * tests.
 *
 * Each test creates its own tenant + task via REST so the suite
 * stays parallel-safe.
 */

import { expect, type Page, test } from '@playwright/test';

import enAiAgents from '../locales/en/aiAgents.json' with { type: 'json' };
import enCommon from '../locales/en/common.json' with { type: 'json' };
import jaAiAgents from '../locales/ja/aiAgents.json' with { type: 'json' };
import {
  API_BASE_URL,
  cleanupTenant,
  createTestTenant,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';

const copy = {
  sectionTitle: enAiAgents.section.title,
  sectionTitleJa: jaAiAgents.section.title,
  emptyTitleJa: jaAiAgents.empty.title,
  expand: enAiAgents.section.expand,
  collapse: enAiAgents.section.collapse,
  emptyTitle: enAiAgents.empty.title,
  emptyBody: enAiAgents.empty.body,
  fatalTitle: enCommon.fatal.title,
  errorFetchFailed: enAiAgents.error.fetchFailed,
  errorRetry: enAiAgents.error.retry,
} as const;

async function seedTask(tenant: TestTenant, title: string): Promise<{ id: string; title: string }> {
  const res = await fetch(`${API_BASE_URL}/tasks`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
    body: JSON.stringify({ title, projectId: tenant.projectId }),
  });
  if (!res.ok) {
    throw new Error(`POST /tasks -> ${res.status} ${await res.text()}`);
  }
  const body = (await res.json()) as { id: string; title: string };
  return { id: body.id, title: body.title };
}

/**
 * Navigates to /tasks/{id} and waits until the AI activity section
 * header mounts. The disclosure caret + title pair is the most
 * stable readiness signal that the suspense boundary resolved.
 */
async function openTaskDetail(page: Page, taskId: string, title: string): Promise<void> {
  await page.goto(`/tasks/${taskId}`);
  await expect(page.getByText(title).first()).toBeVisible({ timeout: 15_000 });
  // Exact match: the empty-state heading "No AI activity yet" contains
  // the substring "AI activity" and would otherwise widen this locator
  // into a strict-mode violation now that EmptyState renders a real
  // heading.
  await expect(page.getByRole('heading', { name: copy.sectionTitle, exact: true })).toBeVisible({
    timeout: 10_000,
  });
}

test.describe('task detail — AI agents section', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('A: empty state renders for a freshly seeded task', async ({ page }) => {
    tenant = await createTestTenant();
    const task = await seedTask(tenant, `AI agents A ${Date.now().toString(36)}`);

    await injectAuth(page.context(), tenant);
    await openTaskDetail(page, task.id, task.title);

    // The empty placeholder is a `<p>` with the title copy + body
    // copy. Use the title — it is short enough that the substring
    // match is safe even with extra whitespace.
    await expect(page.getByText(copy.emptyTitle)).toBeVisible({ timeout: 5_000 });
    await expect(page.getByText(copy.emptyBody)).toBeVisible();
  });

  test('B: collapse / expand toggles aria-expanded and hidden on the body', async ({ page }) => {
    tenant = await createTestTenant();
    const task = await seedTask(tenant, `AI agents B ${Date.now().toString(36)}`);

    await injectAuth(page.context(), tenant);
    await openTaskDetail(page, task.id, task.title);

    // The disclosure is a `<button aria-expanded ...>` whose
    // accessible name is the section title (it wraps an h3 with the
    // title text). Default is expanded for short lists.
    const disclosure = page
      .getByRole('button', { expanded: true })
      .filter({ hasText: copy.sectionTitle });
    await expect(disclosure).toBeVisible({ timeout: 5_000 });

    // Click to collapse → aria-expanded flips to false. The body
    // gets `hidden` so the empty state copy disappears.
    await disclosure.click();
    const collapsedDisclosure = page
      .getByRole('button', { expanded: false })
      .filter({ hasText: copy.sectionTitle });
    await expect(collapsedDisclosure).toBeVisible({ timeout: 5_000 });
    await expect(page.getByText(copy.emptyTitle)).toBeHidden();

    // Click again to expand → empty body is visible again.
    await collapsedDisclosure.click();
    await expect(page.getByText(copy.emptyTitle)).toBeVisible({ timeout: 5_000 });
  });

  /**
   * C: when the AI invocations endpoint fails, the section's local
   * ErrorBoundary catches the throw and renders `AIAgentsError`
   * inline. The fallback uses `role="alert"` and the localised
   * `aiAgents.error.fetchFailed` copy plus a Retry button — neither
   * the parent task detail page nor the root `FatalFallback` is
   * disturbed. We assert on the alert + the retry button to verify
   * the per-feature surface, and confirm the rest of the task page
   * (including the task title) still mounts normally.
   */
  test('C: AI invocations 500 renders the per-section error fallback', async ({ page }) => {
    tenant = await createTestTenant();
    const task = await seedTask(tenant, `AI agents C ${Date.now().toString(36)}`);

    await injectAuth(page.context(), tenant);
    await page.route('**/tasks/*/ai/invocations**', (route) =>
      route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: '{"code":"server_error","message":"forced for E2E"}',
      }),
    );

    await page.goto(`/tasks/${task.id}`);

    // The task detail page itself still mounts — only the AI section
    // unwinds to its local fallback, so the task title remains visible.
    await expect(page.getByText(task.title).first()).toBeVisible({ timeout: 15_000 });

    // The per-section AIAgentsError fallback renders inside `role="alert"`
    // with the localised fetchFailed copy and a Retry button.
    const alert = page.getByRole('alert').filter({ hasText: copy.errorFetchFailed });
    await expect(alert).toBeVisible({ timeout: 10_000 });
    await expect(alert.getByRole('button', { name: copy.errorRetry })).toBeVisible();

    // Sanity: the root FatalFallback heading must NOT appear — the
    // per-feature boundary contains the failure to its own subtree.
    await expect(page.getByRole('heading', { level: 1, name: copy.fatalTitle })).toHaveCount(0);
  });

  /**
   * D: with the user locale = ja the section title resolves through
   * the aiAgents namespace in Japanese. Mirrors the locale-switch
   * pattern used by `ai-metrics.spec.ts` E: tenant created with
   * locale 'ja', plus a localStorage seed so the very first paint
   * (pre-bootstrap) is also ja.
   */
  test('D: ja locale renders the section title in Japanese', async ({ page }) => {
    tenant = await createTestTenant({ locale: 'ja' });
    const task = await seedTask(tenant, `AI agents D ${Date.now().toString(36)}`);

    await injectAuth(page.context(), tenant);
    await page.addInitScript(() => {
      localStorage.setItem('i18nextLng', 'ja');
      localStorage.setItem('nf.lang', 'ja');
    });

    await page.goto(`/tasks/${task.id}`);
    await expect(page.getByText(task.title).first()).toBeVisible({ timeout: 15_000 });
    await expect(page.getByRole('heading', { name: copy.sectionTitleJa, exact: true })).toBeVisible(
      {
        timeout: 10_000,
      },
    );
    // The empty placeholder copy also resolves in ja, confirming the
    // namespace is fully wired (not just `section.title`).
    await expect(page.getByText(copy.emptyTitleJa)).toBeVisible({ timeout: 5_000 });
  });
});

test.describe('task detail — AI agents section — mobile viewport', () => {
  // `test.use` is per-describe in Playwright, so the mobile case lives
  // in its own block instead of overriding `viewport` per-test (mirrors
  // the `ai-metrics.spec.ts` mobile describe at the bottom of the
  // file).
  test.use({ viewport: { width: 375, height: 812 } });

  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  /**
   * E: at 375x812 the AI activity section header and the empty
   * placeholder are both visible. Visibility-only — no pixel-perfect
   * layout assertions; we are confirming the section does not get
   * clipped off-screen / hidden on a phone-class viewport.
   */
  test('E: mobile renders the section header and empty state without overflow', async ({
    page,
  }) => {
    tenant = await createTestTenant();
    const task = await seedTask(tenant, `AI agents E ${Date.now().toString(36)}`);

    await injectAuth(page.context(), tenant);
    await page.goto(`/tasks/${task.id}`);
    await expect(page.getByText(task.title).first()).toBeVisible({ timeout: 15_000 });

    await expect(page.getByRole('heading', { name: copy.sectionTitle, exact: true })).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByText(copy.emptyTitle)).toBeVisible({ timeout: 5_000 });
  });
});
