/**
 * Calendar month-grid drag E2E.
 *
 * Source under test: apps/flow-web/src/routes/_authenticated.calendar.tsx
 * and apps/flow-web/src/features/calendar-events/lib/pointer-drag.ts.
 *
 * The grid moves a pill with pointer events rather than HTML5
 * drag-and-drop, so a plain mouse gesture — press, move, release — is
 * both what a person does and what these tests perform. On drop a task
 * fires `PATCH /tasks/{id}` with `{ dueOn }` and an event fires `PATCH`
 * on its calendar row with the shifted range. Nothing on the grid moves
 * until the request succeeds, so a refusal needs no rollback.
 *
 * A repeating event is movable too: dropping one raises the same
 * "this event / this and following / all events" choice the edit dialog
 * asks before it saves, and the request carries the answer.
 *
 * Cells are addressed via the `data-cell-key="YYYY-MM-DD"` attribute
 * added to the cell wrapper for E2E reachability (CSS-module class
 * names are hashed). Source elements:
 *
 *   - Task pill: <a> inside ul[styles.pillList], whose `title` attribute
 *     is "${task.title} · ${task.workspaceName}".
 *   - Event pill: <button class*="eventPill"> with the same parent.
 *
 * The April 2026 month is used as a fixed test window; the calendar
 * uses local-tz date arithmetic but a tenant created with the default
 * UTC timezone treats `2026-04-12` and `2026-04-15` as same-week dates
 * regardless of where the test runs.
 */

import type { Locator, Page } from '@playwright/test';
import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import {
  API_BASE_URL,
  createCalendarEvent,
  createTask,
  ensurePersonalCalendar,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';

const FIXED_YEAR = 2026;
const FIXED_MONTH_INDEX = 3; // April

/**
 * Navigate the calendar from the current month to April 2026 by clicking
 * the prev/next button as needed. The header button name comes from
 * t('calendar.prev') / t('calendar.next') which render as 'Previous
 * month' / 'Next month' in en. We accept the localized variants too so
 * a tenant created with locale='ja' would still pass.
 */
async function goToApril2026(page: Page): Promise<void> {
  // The page renders three <h2>s: the month label, the calendars-rail
  // title, and the pending-invites panel header. Disambiguate by the
  // CSS-module class prefix added to the calendar route's monthLabel.
  const heading = page.locator('h2[class*="monthLabel"]').first();
  await heading.waitFor({ state: 'visible', timeout: 15_000 });
  // Cap iterations so a missing button doesn't loop forever.
  for (let i = 0; i < 36; i++) {
    const text = (await heading.innerText()).trim();
    // Accept en "April 2026" and ja "2026年4月".
    if (/April\s+2026/i.test(text) || /2026\s*年\s*4\s*月/.test(text)) return;
    // Compare against today to decide direction.
    const now = new Date();
    const cursorIsBefore =
      now.getFullYear() < FIXED_YEAR ||
      (now.getFullYear() === FIXED_YEAR && now.getMonth() < FIXED_MONTH_INDEX);
    const btn = cursorIsBefore
      ? page.getByRole('button', { name: /next|次/i })
      : page.getByRole('button', { name: /prev|前/i });
    await btn.first().click();
    await page.waitForTimeout(50);
  }
  throw new Error('failed to navigate to April 2026');
}

/**
 * Drag one element onto another with the mouse.
 *
 * The grid listens for pointer events, which Playwright's own mouse
 * produces, so no synthetic event dispatch is needed. The gesture is
 * stepped rather than teleported: a press that lands on the target in
 * one jump is indistinguishable from a click, and the grid deliberately
 * requires a few pixels of travel before a press becomes a drag.
 */
async function pointerDrag(page: Page, from: Locator, to: Locator): Promise<void> {
  await from.waitFor({ state: 'visible' });
  await to.waitFor({ state: 'visible' });
  const source = await from.boundingBox();
  const target = await to.boundingBox();
  if (!source || !target) throw new Error('drag source or target has no box');
  await page.mouse.move(source.x + source.width / 2, source.y + source.height / 2);
  await page.mouse.down();
  await page.mouse.move(target.x + target.width / 2, target.y + target.height / 2, { steps: 12 });
  await page.mouse.up();
}

/** Locate a task pill by its title (rendered inside the pill's link). */
function taskPill(page: Page, title: string) {
  return page.locator('a[class*="taskPill"]').filter({ hasText: title });
}

/** Locate an event pill button by its visible title. */
function eventPillByTitle(page: Page, title: string) {
  return page.locator('button[class*="eventPill"]').filter({ hasText: title });
}

/** Locate a day cell by its YYYY-MM-DD key. */
function cellByKey(page: Page, dateKey: string) {
  return page.locator(`[data-cell-key="${dateKey}"]`);
}

/**
 * Fetch a single task by id and return its dueOn (or null when unset).
 * Used as the server-side source of truth for the move test.
 */
async function fetchDueOn(tenant: TestTenant, taskId: string): Promise<string | null> {
  const res = await fetch(`${API_BASE_URL}/tasks/${taskId}`, {
    headers: {
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
  });
  if (!res.ok) throw new Error(`GET /tasks/{id} -> ${res.status} ${await res.text()}`);
  const body = (await res.json()) as { dueOn?: string | null };
  return body.dueOn ?? null;
}

test.describe('calendar month-grid drag', () => {
  // Each test seeds its own data so they can execute independently.

  let tenant: TestTenant;
  let calendarId: string;

  test.beforeEach(async () => {
    tenant = loadTenants().user;
    const cal = await ensurePersonalCalendar(tenant);
    calendarId = cal.id;
  });

  test('dragging a task pill to another day updates dueOn', async ({ page }) => {
    const stamp = Date.now().toString(36);
    const title = `Cal-drag move ${stamp}`;
    const created = await createTask(tenant, title, { dueOn: '2026-04-12' });

    await injectAuth(page.context(), tenant);
    await page.goto('/calendar');
    await goToApril2026(page);

    const sourcePill = taskPill(page, title);
    await expect(sourcePill).toBeVisible({ timeout: 15_000 });
    // Sanity: the pill must currently live in the 2026-04-12 cell.
    await expect(cellByKey(page, '2026-04-12').locator(sourcePill)).toHaveCount(1);

    const targetCell = cellByKey(page, '2026-04-15');
    await expect(targetCell).toBeVisible();

    const settle = page.waitForResponse(
      (res) => res.url().includes(`/tasks/${created.id}`) && res.request().method() === 'PATCH',
    );
    await pointerDrag(page, sourcePill, targetCell);
    const res = await settle;
    expect(res.status(), 'PATCH /tasks/{id} should succeed').toBe(200);

    // Server-side dueOn must reflect the drop target.
    await expect.poll(() => fetchDueOn(tenant, created.id), { timeout: 5_000 }).toBe('2026-04-15');

    // UI must repaint the pill in the new cell after the mutation
    // settles (the calendar invalidates the me-tasks query onSuccess).
    await expect(cellByKey(page, '2026-04-15').locator(taskPill(page, title))).toHaveCount(1, {
      timeout: 10_000,
    });
    await expect(cellByKey(page, '2026-04-12').locator(taskPill(page, title))).toHaveCount(0);

    // A drag must not also follow the link it started on.
    await expect(page).toHaveURL(/\/calendar/);

    // Cleanup
    await fetch(`${API_BASE_URL}/tasks/${created.id}`, {
      method: 'DELETE',
      headers: { authorization: `Bearer ${tenant.accessToken}` },
    }).catch(() => undefined);
  });

  test('PATCH 500 leaves the pill in its original cell (pessimistic)', async ({ page }) => {
    const stamp = Date.now().toString(36);
    const title = `Cal-drag rollback ${stamp}`;
    const created = await createTask(tenant, title, { dueOn: '2026-04-12' });

    await injectAuth(page.context(), tenant);

    // Force the PATCH to 500 BEFORE the page navigates so the very
    // first attempt fails. The CORS shim from injectAuth uses
    // route.fetch(); the more specific URL match here wins.
    await page.route(
      (url) => url.href.includes(`/tasks/${created.id}`),
      async (route) => {
        if (route.request().method() !== 'PATCH') {
          await route.fallback();
          return;
        }
        const origin = route.request().headers().origin ?? '*';
        await route.fulfill({
          status: 500,
          headers: {
            'content-type': 'application/json',
            'access-control-allow-origin': origin,
            'access-control-allow-credentials': 'true',
          },
          body: JSON.stringify({
            type: 'about:blank',
            title: 'Internal Server Error',
            status: 500,
            detail: 'forced for E2E pessimistic test',
          }),
        });
      },
    );

    await page.goto('/calendar');
    await goToApril2026(page);

    const sourcePill = taskPill(page, title);
    await expect(sourcePill).toBeVisible({ timeout: 15_000 });
    await expect(cellByKey(page, '2026-04-12').locator(sourcePill)).toHaveCount(1);

    const targetCell = cellByKey(page, '2026-04-15');
    const settle = page.waitForResponse(
      (res) => res.url().includes(`/tasks/${created.id}`) && res.request().method() === 'PATCH',
    );
    await pointerDrag(page, sourcePill, targetCell);
    const res = await settle;
    expect(res.status(), 'mocked PATCH should report 500').toBe(500);

    // Server-side dueOn must be unchanged.
    expect(await fetchDueOn(tenant, created.id)).toBe('2026-04-12');

    // The pill must still live in the source cell. Use a short poll
    // because the route's onError invalidates the me-tasks query, which
    // triggers a refetch + re-render.
    await expect
      .poll(
        async () => await cellByKey(page, '2026-04-12').locator(taskPill(page, title)).count(),
        { timeout: 5_000 },
      )
      .toBe(1);
    await expect(cellByKey(page, '2026-04-15').locator(taskPill(page, title))).toHaveCount(0);

    await fetch(`${API_BASE_URL}/tasks/${created.id}`, {
      method: 'DELETE',
      headers: { authorization: `Bearer ${tenant.accessToken}` },
    }).catch(() => undefined);
  });

  test('dragging an event pill to another day moves it', async ({ page }) => {
    const stamp = Date.now().toString(36);
    const title = `Cal-drag event ${stamp}`;
    // Event on 2026-04-12 12:00..13:00 UTC.
    const startAt = Math.floor(Date.UTC(2026, 3, 12, 12, 0, 0) / 1000);
    const endAt = startAt + 3600;
    const created = await createCalendarEvent(tenant, calendarId, {
      title,
      startAt,
      endAt,
      kind: 'event',
    });

    await injectAuth(page.context(), tenant);
    await page.goto('/calendar');
    await goToApril2026(page);

    const pill = eventPillByTitle(page, title);
    await expect(pill).toBeVisible({ timeout: 15_000 });

    const settle = page.waitForResponse(
      (res) => res.url().includes(`/events/${created.id}`) && res.request().method() === 'PATCH',
    );
    await pointerDrag(page, pill, cellByKey(page, '2026-04-15'));
    const res = await settle;
    expect(res.status(), 'PATCH on the event should succeed').toBe(200);

    // A row that does not repeat is moved outright — three days on,
    // duration intact, and no scope question in between.
    const body = res.request().postDataJSON() as Record<string, unknown>;
    expect(body.startAt).toBe(startAt + 3 * 86_400);
    expect(body.endAt).toBe(endAt + 3 * 86_400);
    expect(body.scope).toBeUndefined();

    await expect(cellByKey(page, '2026-04-15').locator(eventPillByTitle(page, title))).toHaveCount(
      1,
      { timeout: 10_000 },
    );

    // Cleanup
    await fetch(
      `${API_BASE_URL}/workspaces/${tenant.workspaceId}/calendars/${calendarId}/events/${created.id}`,
      {
        method: 'DELETE',
        headers: { authorization: `Bearer ${tenant.accessToken}` },
      },
    ).catch(() => undefined);
  });

  test('dropping a repeating event asks which occurrences the move reaches', async ({ page }) => {
    const stamp = Date.now().toString(36);
    const title = `Cal-drag series ${stamp}`;
    // Repeats yearly from 2026-04-12, so exactly one occurrence falls in
    // the visible month and the pill is unambiguous.
    const startAt = Math.floor(Date.UTC(2026, 3, 12, 12, 0, 0) / 1000);
    const created = await createCalendarEvent(tenant, calendarId, {
      title,
      startAt,
      endAt: startAt + 3600,
      kind: 'event',
      recurrenceRule: { freq: 'yearly', interval: 1 },
    });

    await injectAuth(page.context(), tenant);
    await page.goto('/calendar');
    await goToApril2026(page);

    const pill = eventPillByTitle(page, title);
    await expect(pill).toBeVisible({ timeout: 15_000 });

    // Nothing may be written before the question is answered.
    const writes: string[] = [];
    page.on('request', (req) => {
      if (req.method() === 'PATCH' && req.url().includes('/events/')) writes.push(req.url());
    });

    await pointerDrag(page, pill, cellByKey(page, '2026-04-15'));

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible({ timeout: 10_000 });
    expect(writes, 'the drop must not write before the scope is chosen').toEqual([]);

    const settle = page.waitForResponse(
      (res) => res.url().includes(`/events/${created.id}`) && res.request().method() === 'PATCH',
    );
    await dialog.getByRole('button', { name: /^save$|保存|保存$/i }).click();
    const res = await settle;
    expect(res.status(), 'the scoped PATCH should succeed').toBe(200);

    // The default is the least destructive option, and the occurrence it
    // names is the one that was drawn — not where the drag ended.
    const body = res.request().postDataJSON() as Record<string, unknown>;
    expect(body.scope).toBe('occurrence');
    expect(body.occurrenceStart).toBe(startAt);
    expect(body.startAt).toBe(startAt + 3 * 86_400);

    // Cleanup
    await fetch(
      `${API_BASE_URL}/workspaces/${tenant.workspaceId}/calendars/${calendarId}/events/${created.id}`,
      {
        method: 'DELETE',
        headers: { authorization: `Bearer ${tenant.accessToken}` },
      },
    ).catch(() => undefined);
  });

  test('cancelling the scope choice leaves the event where it was', async ({ page }) => {
    const stamp = Date.now().toString(36);
    const title = `Cal-drag series cancel ${stamp}`;
    const startAt = Math.floor(Date.UTC(2026, 3, 12, 12, 0, 0) / 1000);
    const created = await createCalendarEvent(tenant, calendarId, {
      title,
      startAt,
      endAt: startAt + 3600,
      kind: 'event',
      recurrenceRule: { freq: 'yearly', interval: 1 },
    });

    await injectAuth(page.context(), tenant);

    const writes: string[] = [];
    page.on('request', (req) => {
      if (req.method() === 'PATCH' && req.url().includes('/events/')) writes.push(req.url());
    });

    await page.goto('/calendar');
    await goToApril2026(page);

    const pill = eventPillByTitle(page, title);
    await expect(pill).toBeVisible({ timeout: 15_000 });

    await pointerDrag(page, pill, cellByKey(page, '2026-04-15'));

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible({ timeout: 10_000 });
    await dialog.getByRole('button', { name: /^cancel$|キャンセル|取消/i }).click();
    await expect(dialog).toBeHidden();

    // Give any stray request a moment to leave the page.
    await page.waitForTimeout(500);
    expect(writes, 'a dismissed question must write nothing').toEqual([]);
    await expect(cellByKey(page, '2026-04-12').locator(eventPillByTitle(page, title))).toHaveCount(
      1,
    );

    // Cleanup
    await fetch(
      `${API_BASE_URL}/workspaces/${tenant.workspaceId}/calendars/${calendarId}/events/${created.id}`,
      {
        method: 'DELETE',
        headers: { authorization: `Bearer ${tenant.accessToken}` },
      },
    ).catch(() => undefined);
  });
});
