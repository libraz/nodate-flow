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
 *   F. typing a query + pressing Enter on the keyboard-active row
 *      commits the link via the WAI-ARIA combobox + activedescendant
 *      pattern (no mouse events).
 *   G. pressing Escape inside the open picker closes the dialog and
 *      restores focus to the section header trigger.
 *   H. when the initial linked-events GET fails the section mounts the
 *      local LinkedEventsError fallback (role=alert + the localised
 *      `error.fetchFailed` copy) instead of escalating to the route
 *      FatalFallback.
 *   I. registering the tenant with locale='ja' surfaces the Japanese
 *      `section.title` on the empty-state task detail page.
 *   J. (nested describe) on a 375x812 mobile viewport the section
 *      header title and the "Link event" trigger both stay visible
 *      without overflow clipping them off-screen.
 *
 * Each test creates its own tenant + task via REST so the suite stays
 * parallel-safe.
 */

import { type Page, expect, test } from '@playwright/test';

import enLinkedEvents from '../locales/en/linkedEvents.json' with { type: 'json' };
import jaLinkedEvents from '../locales/ja/linkedEvents.json' with { type: 'json' };
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
  errorFetchFailed: enLinkedEvents.error.fetchFailed,
  jaSectionTitle: jaLinkedEvents.section.title,
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

  test('F: ArrowDown + Enter commits the link via keyboard navigation', async ({ page }) => {
    tenant = await createTestTenant();
    const task = await seedTask(tenant, `Links F ${Date.now().toString(36)}`);
    const calendar = await ensurePersonalCalendar(tenant);
    const eventTitle = `Keynote ${Date.now().toString(36)}`;
    const window = tomorrowEventWindow();
    await createCalendarEvent(tenant, calendar.id, {
      title: eventTitle,
      startAt: window.startAt,
      endAt: window.endAt,
    });

    await injectAuth(page.context(), tenant);
    await openTaskDetail(page, task.id, task.title);

    await page.getByRole('button', { name: copy.trigger }).first().click();
    const picker = page.getByRole('dialog', { name: copy.trigger });
    const combobox = picker.getByRole('combobox');
    await expect(combobox).toBeVisible({ timeout: 5_000 });
    await expect(combobox).toBeFocused();

    // Type ~6 chars so the listbox debounces down to a single match.
    await combobox.fill(eventTitle.slice(0, 6));
    const optionRow = picker.getByRole('option', { name: new RegExp(eventTitle) });
    await expect(optionRow).toBeVisible({ timeout: 5_000 });

    // The combobox has a literal `aria-expanded` so it always serialises
    // to "true" while the popover is mounted.
    await expect(combobox).toHaveAttribute('aria-expanded', 'true');

    // Auto-pick effect sets aria-activedescendant to the first selectable
    // option once results arrive. Poll because it lands a tick after the
    // listbox renders.
    await expect
      .poll(async () => combobox.getAttribute('aria-activedescendant'), { timeout: 5_000 })
      .not.toBeNull();

    // The picker's `moveActive` cycles modulo `selectableIds.length`, so
    // ArrowDown on a single-row listbox lands on the same row that the
    // auto-highlight effect already chose. Either way the active option
    // must be the seeded event. The active row also carries
    // `aria-selected="true"`, which is the cross-check we use here
    // (the option id is a `useId()` token and would need CSS-escaping).
    await page.keyboard.press('ArrowDown');
    const activeId = await combobox.getAttribute('aria-activedescendant');
    expect(activeId).not.toBeNull();
    const activeOption = picker.getByRole('option', { selected: true });
    await expect(activeOption).toHaveAttribute('id', activeId ?? '');
    await expect(activeOption).toHaveText(new RegExp(eventTitle));

    // Enter commits via the activedescendant — no click on the row.
    await page.keyboard.press('Enter');

    await expect(page.getByText(new RegExp(copy.toastLinkedPrefix))).toBeVisible({
      timeout: 5_000,
    });
    await expect(
      page.getByRole('button', { name: new RegExp(`Unlink ${eventTitle.slice(0, 6)}`) }),
    ).toBeVisible({ timeout: 5_000 });
  });

  test('G: Escape closes the picker and restores focus to the trigger', async ({ page }) => {
    tenant = await createTestTenant();
    const task = await seedTask(tenant, `Links G ${Date.now().toString(36)}`);

    await injectAuth(page.context(), tenant);
    await openTaskDetail(page, task.id, task.title);

    const trigger = page.getByRole('button', { name: copy.trigger }).first();
    await trigger.click();

    const picker = page.getByRole('dialog', { name: copy.trigger });
    await expect(picker).toBeVisible({ timeout: 5_000 });
    await expect(picker.getByRole('combobox')).toBeFocused();

    await page.keyboard.press('Escape');

    await expect(picker).toBeHidden({ timeout: 5_000 });
    await expect(trigger).toBeFocused();
  });

  /** H: error state — initial GET fails => local LinkedEventsError mounts. */
  test('H: failed initial GET mounts the LinkedEventsError fallback (role=alert)', async ({
    page,
  }) => {
    tenant = await createTestTenant();
    const task = await seedTask(tenant, `Links H ${Date.now().toString(36)}`);

    // Intercept the linked-events list GET only. The SDK path is
    // `/tasks/{id}/linked-events` (no workspace prefix), so the glob
    // is scoped narrowly enough to avoid catching POST /tasks or any
    // other unrelated endpoint while still matching the failure target.
    await page.route('**/tasks/*/linked-events', async (route) => {
      if (route.request().method() === 'GET') {
        await route.fulfill({
          status: 500,
          contentType: 'application/json',
          body: '{"code":"server_error","message":"forced for E2E"}',
        });
        return;
      }
      await route.continue();
    });

    await injectAuth(page.context(), tenant);
    await page.goto(`/tasks/${task.id}`);

    // The task title still renders (the task GET is untouched), so wait
    // on it as the readiness signal before asserting on the section.
    await expect(page.getByText(task.title).first()).toBeVisible({ timeout: 15_000 });

    // The section's local ErrorBoundary mounts LinkedEventsError, which
    // exposes role="alert" and the localised `error.fetchFailed` copy.
    const alert = page.getByRole('alert').filter({ hasText: copy.errorFetchFailed });
    await expect(alert).toBeVisible({ timeout: 10_000 });
  });

  /** I: locale ja — empty state renders the Japanese section title. */
  test('I: ja locale renders the Japanese section title on empty state', async ({ browser }) => {
    // The web app's auth bootstrap calls `setLanguage(user.locale)` after
    // login, which overrides any localStorage value `addInitScript` could
    // pre-set. Registration-time locale is the proven knob — same pattern
    // used by the calendar-event-dialog ja spec.
    tenant = await createTestTenant({ locale: 'ja' });
    const task = await seedTask(tenant, `Links I ${Date.now().toString(36)}`);

    const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
    try {
      await injectAuth(context, tenant);
      const page = await context.newPage();
      await page.goto(`/tasks/${task.id}`);
      await expect(page.getByText(task.title).first()).toBeVisible({ timeout: 15_000 });

      // The Japanese section.title is "関連イベント".
      await expect(page.getByRole('heading', { name: copy.jaSectionTitle })).toBeVisible({
        timeout: 10_000,
      });
    } finally {
      await context.close();
    }
  });

  /* ─────────────────────────────────────────────────────────── */

  test.describe('mobile viewport', () => {
    test.use({ viewport: { width: 375, height: 812 } });

    /** J: 375x812 mobile — header title + Link event trigger both visible. */
    test('J: mobile renders section header and trigger without overflow', async ({ page }) => {
      tenant = await createTestTenant();
      const task = await seedTask(tenant, `Links J ${Date.now().toString(36)}`);

      await injectAuth(page.context(), tenant);
      await openTaskDetail(page, task.id, task.title);

      // The disclosure heading must stay visible at iPhone-class widths
      // (no clipping into a hidden overflow column).
      await expect(page.getByRole('heading', { name: copy.sectionTitle })).toBeVisible({
        timeout: 5_000,
      });

      // The header "Link event" trigger button must also remain reachable.
      // `.first()` because the empty state CTA shares the same accessible
      // name and matches twice on a freshly-seeded task.
      await expect(page.getByRole('button', { name: copy.trigger }).first()).toBeVisible({
        timeout: 5_000,
      });
    });
  });
});
