/**
 * Task ↔ Event manual link UI E2E (G5).
 *
 * Covers the LinkedEventsSection that lives on the task detail page
 * (`/tasks/{taskId}`). The section is a collapsible disclosure with
 * an event picker (combobox + listbox + activedescendant), a kind
 * selector (contributes_to / blocks segmented), and per-row unlink
 * buttons. Backed by `useLinkEvent` / `useUnlinkEvent` (optimistic
 * updates) and emits success / failure toasts via the global toaster.
 *
 * Cases:
 *   A. empty state — section renders the "no events linked yet"
 *      placeholder when the task has no links.
 *   B. clicking the "Link event" trigger opens the picker popover and
 *      focuses the search combobox.
 *   C. seeding an event via REST + picking it from the listbox creates
 *      a row in the section and surfaces the "Linked" toast.
 *   D. clicking the row's unlink control removes the row and surfaces
 *      the "Unlinked" toast.
 *   E. switching the kind selector flips `aria-checked` on the segmented
 *      control as a smoke check that the radiogroup is wired correctly.
 *
 * Each test creates its own tenant + task via REST so the suite stays
 * parallel-safe.
 */

import { type Page, expect, test } from '@playwright/test';

import enLinkedEvents from '../locales/en/linkedEvents.json' with { type: 'json' };
import {
  API_BASE_URL,
  type TestTenant,
  cleanupTenant,
  createCalendarEvent,
  createTestTenant,
  ensurePersonalCalendar,
  injectAuth,
} from './fixtures/tenant';

const copy = {
  sectionTitle: enLinkedEvents.section.title,
  trigger: enLinkedEvents.trigger.linkEvent,
  emptyTitle: enLinkedEvents.empty.title,
  contributesTo: enLinkedEvents.kind.contributesTo,
  blocks: enLinkedEvents.kind.blocks,
  searchPlaceholder: enLinkedEvents.picker.searchPlaceholder,
  toastLinkedPrefix: 'Linked',
  toastUnlinkedPrefix: 'Unlinked',
} as const;

/**
 * Seeds a workspace task and returns its public id + title. The task
 * detail page is the host for the LinkedEventsSection so we navigate
 * directly to `/tasks/{id}` once the row exists.
 */
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
 * Navigates to the task detail page and waits for the LinkedEvents
 * section header to mount. The disclosure caret + title pair is the
 * most stable readiness signal that the suspense boundary resolved.
 */
async function openTaskDetail(page: Page, taskId: string, title: string): Promise<void> {
  await page.goto(`/tasks/${taskId}`);
  await expect(page.getByText(title).first()).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole('heading', { name: copy.sectionTitle })).toBeVisible({
    timeout: 10_000,
  });
}

/** Returns a unix-second pair for "tomorrow 09:00 → 10:00 UTC". */
function tomorrowEventWindow(): { startAt: number; endAt: number } {
  const d = new Date();
  d.setUTCDate(d.getUTCDate() + 1);
  d.setUTCHours(9, 0, 0, 0);
  const start = Math.floor(d.getTime() / 1000);
  return { startAt: start, endAt: start + 3600 };
}

test.describe('task ↔ event manual links', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('A: empty state renders when the task has no links', async ({ page }) => {
    tenant = await createTestTenant();
    const task = await seedTask(tenant, `Links A ${Date.now().toString(36)}`);

    await injectAuth(page.context(), tenant);
    await openTaskDetail(page, task.id, task.title);

    await expect(page.getByRole('heading', { name: copy.emptyTitle })).toBeVisible({
      timeout: 5_000,
    });
  });

  test('B: clicking Link event opens the picker and focuses the combobox', async ({ page }) => {
    tenant = await createTestTenant();
    const task = await seedTask(tenant, `Links B ${Date.now().toString(36)}`);

    await injectAuth(page.context(), tenant);
    await openTaskDetail(page, task.id, task.title);

    // The trigger lives in the section header. The empty-state CTA
    // shares the same text, so scope the click to the header button
    // (it's the first one with the trigger label).
    await page.getByRole('button', { name: copy.trigger }).first().click();

    // Scope the combobox lookup to the picker dialog — the workspace
    // switcher selectors elsewhere on the page also match
    // `role=combobox` and would trip strict mode.
    const picker = page.getByRole('dialog', { name: copy.trigger });
    const combobox = picker.getByRole('combobox');
    await expect(combobox).toBeVisible({ timeout: 5_000 });
    await expect(combobox).toBeFocused();
  });

  test('C: picking a seeded event creates a row and emits the linked toast', async ({ page }) => {
    tenant = await createTestTenant();
    const task = await seedTask(tenant, `Links C ${Date.now().toString(36)}`);
    const calendar = await ensurePersonalCalendar(tenant);
    const eventTitle = `Standup ${Date.now().toString(36)}`;
    const window = tomorrowEventWindow();
    await createCalendarEvent(tenant, calendar.id, {
      title: eventTitle,
      startAt: window.startAt,
      endAt: window.endAt,
    });

    await injectAuth(page.context(), tenant);
    await openTaskDetail(page, task.id, task.title);

    // Open the picker. The empty state CTA also opens it, but we use
    // the section header trigger so the test does not depend on the
    // empty-state surface path.
    await page.getByRole('button', { name: copy.trigger }).first().click();

    const picker = page.getByRole('dialog', { name: copy.trigger });
    const combobox = picker.getByRole('combobox');
    await expect(combobox).toBeVisible({ timeout: 5_000 });

    // Type a substring of the event title — the listbox filters to a
    // single matching row after the 200ms debounce.
    await combobox.fill(eventTitle.slice(0, 8));
    const optionRow = picker.getByRole('option', { name: new RegExp(eventTitle) });
    await expect(optionRow).toBeVisible({ timeout: 5_000 });
    await optionRow.click();

    // Success toast surfaces with the localised "Linked" prefix.
    await expect(page.getByText(new RegExp(copy.toastLinkedPrefix))).toBeVisible({
      timeout: 5_000,
    });

    // The row appears in the section list. Use the visually-hidden
    // unlink aria-label as a stable selector — the title is rendered
    // as a placeholder anchor that is duplicated in the picker.
    await expect(
      page.getByRole('button', { name: new RegExp(`Unlink ${eventTitle.slice(0, 8)}`) }),
    ).toBeVisible({ timeout: 5_000 });
  });

  test('D: clicking unlink removes the row and emits the unlinked toast', async ({ page }) => {
    tenant = await createTestTenant();
    const task = await seedTask(tenant, `Links D ${Date.now().toString(36)}`);
    const calendar = await ensurePersonalCalendar(tenant);
    const eventTitle = `Review ${Date.now().toString(36)}`;
    const window = tomorrowEventWindow();
    await createCalendarEvent(tenant, calendar.id, {
      title: eventTitle,
      startAt: window.startAt,
      endAt: window.endAt,
    });

    await injectAuth(page.context(), tenant);
    await openTaskDetail(page, task.id, task.title);

    // Link the event first so we have something to unlink.
    await page.getByRole('button', { name: copy.trigger }).first().click();
    const picker = page.getByRole('dialog', { name: copy.trigger });
    await picker.getByRole('combobox').fill(eventTitle.slice(0, 6));
    await picker.getByRole('option', { name: new RegExp(eventTitle) }).click();
    const unlinkBtn = page.getByRole('button', {
      name: new RegExp(`Unlink ${eventTitle.slice(0, 6)}`),
    });
    await expect(unlinkBtn).toBeVisible({ timeout: 5_000 });

    // Click unlink. The row plays a 200ms shake-out animation before
    // the optimistic removal completes, so we wait for the row to
    // disappear rather than assert immediately.
    await unlinkBtn.click();
    await expect(page.getByText(new RegExp(copy.toastUnlinkedPrefix))).toBeVisible({
      timeout: 5_000,
    });
    await expect(unlinkBtn).toBeHidden({ timeout: 5_000 });
  });

  test('E: kind selector toggles aria-checked between contributes_to and blocks', async ({
    page,
  }) => {
    tenant = await createTestTenant();
    const task = await seedTask(tenant, `Links E ${Date.now().toString(36)}`);

    await injectAuth(page.context(), tenant);
    await openTaskDetail(page, task.id, task.title);

    await page.getByRole('button', { name: copy.trigger }).first().click();
    const picker = page.getByRole('dialog', { name: copy.trigger });
    await expect(picker.getByRole('combobox')).toBeVisible({ timeout: 5_000 });

    const contributesPill = picker.getByRole('radio', { name: copy.contributesTo });
    const blocksPill = picker.getByRole('radio', { name: copy.blocks });

    // Default is `contributes_to`.
    await expect(contributesPill).toHaveAttribute('aria-checked', 'true');
    await expect(blocksPill).toHaveAttribute('aria-checked', 'false');

    await blocksPill.click();
    await expect(blocksPill).toHaveAttribute('aria-checked', 'true');
    await expect(contributesPill).toHaveAttribute('aria-checked', 'false');
  });
});
