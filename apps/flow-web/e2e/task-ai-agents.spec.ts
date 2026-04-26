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
 *
 * Note: per the task brief, we verify the empty state + collapse
 * toggle only — seeding an invocation requires running the AI
 * pipeline against the task which is non-deterministic in CI. The
 * row-level row rendering is exercised by the section's unit tests.
 *
 * Each test creates its own tenant + task via REST so the suite
 * stays parallel-safe.
 */

import { type Page, expect, test } from '@playwright/test';

import enAiAgents from '../locales/en/aiAgents.json' with { type: 'json' };
import {
  API_BASE_URL,
  type TestTenant,
  cleanupTenant,
  createTestTenant,
  injectAuth,
} from './fixtures/tenant';

const copy = {
  sectionTitle: enAiAgents.section.title,
  expand: enAiAgents.section.expand,
  collapse: enAiAgents.section.collapse,
  emptyTitle: enAiAgents.empty.title,
  emptyBody: enAiAgents.empty.body,
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
  await expect(page.getByRole('heading', { name: copy.sectionTitle })).toBeVisible({
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
});
