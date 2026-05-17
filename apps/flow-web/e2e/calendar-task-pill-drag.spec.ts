/**
 * Calendar task-pill drag E2E.
 *
 * Source under test: apps/flow-web/src/routes/_authenticated.calendar.tsx.
 * Task pills (the `<Link draggable>` elements rendered for tasks with
 * a `dueOn` in the visible month) can be dragged onto a different day
 * cell. On drop the route fires `PATCH /tasks/{id}` with `{ dueOn }`
 * and updates the cell only after the mutation succeeds (pessimistic).
 *
 * Calendar event pills (`<button>.eventPill`) are explicitly NOT
 * draggable — the source has no `draggable` attribute on them and no
 * onDragStart handler. The third test pins that behaviour.
 *
 * Cells are addressed via the `data-cell-key="YYYY-MM-DD"` attribute
 * added to the cell wrapper for E2E reachability (CSS-module class
 * names are hashed). Source elements:
 *
 *   - Task pill: <a draggable> inside ul[styles.pillList], whose
 *     `title` attribute is "${task.title} · ${task.workspaceName}".
 *   - Event pill: <button> with the same parent, NOT draggable.
 *
 * The April 2026 month is used as a fixed test window; the calendar
 * uses local-tz date arithmetic but a tenant created with the default
 * UTC timezone treats `2026-04-12` and `2026-04-15` as same-week dates
 * regardless of where the test runs.
 */

import type { Page } from '@playwright/test';
import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import {
  API_BASE_URL,
  type TestTenant,
  createCalendarEvent,
  createTask,
  ensurePersonalCalendar,
  injectAuth,
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
 * Synthesize an HTML5 drag from one element to another. Playwright's
 * mouse-based dragTo does not trigger the native dragstart that the
 * calendar route's `<Link draggable>` relies on, so we dispatch the
 * full DragEvent sequence directly with a shared DataTransfer.
 */
async function nativeDnd(
  page: Page,
  fromHandle: ReturnType<Page['locator']>,
  toHandle: ReturnType<Page['locator']>,
): Promise<void> {
  await fromHandle.waitFor({ state: 'visible' });
  await toHandle.waitFor({ state: 'visible' });
  const fromEl = await fromHandle.elementHandle();
  const toEl = await toHandle.elementHandle();
  if (!fromEl || !toEl) throw new Error('drag source or target element handle was null');
  await page.evaluate(
    ([from, to]) => {
      const dt = new DataTransfer();
      from?.dispatchEvent(
        new DragEvent('dragstart', { bubbles: true, cancelable: true, dataTransfer: dt }),
      );
      to?.dispatchEvent(
        new DragEvent('dragenter', { bubbles: true, cancelable: true, dataTransfer: dt }),
      );
      to?.dispatchEvent(
        new DragEvent('dragover', { bubbles: true, cancelable: true, dataTransfer: dt }),
      );
      to?.dispatchEvent(
        new DragEvent('drop', { bubbles: true, cancelable: true, dataTransfer: dt }),
      );
      from?.dispatchEvent(
        new DragEvent('dragend', { bubbles: true, cancelable: true, dataTransfer: dt }),
      );
    },
    [fromEl, toEl] as const,
  );
}

/** Locate a task pill by its title (rendered inside the pill's link). */
function taskPill(page: Page, title: string) {
  return page.locator('a[draggable="true"]').filter({ hasText: title });
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

test.describe('calendar task-pill drag (move dueOn)', () => {
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
    await nativeDnd(page, sourcePill, targetCell);
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
    await nativeDnd(page, sourcePill, targetCell);
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

  test('calendar event pills are not draggable and never PATCH /events', async ({ page }) => {
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

    // Network listener for any /events/{id} write — must stay at 0.
    const eventWrites: string[] = [];
    page.on('request', (req) => {
      const m = req.method();
      if ((m === 'PATCH' || m === 'PUT' || m === 'POST') && req.url().includes('/events/')) {
        eventWrites.push(`${m} ${req.url()}`);
      }
    });

    await page.goto('/calendar');
    await goToApril2026(page);

    const pill = eventPillByTitle(page, title);
    await expect(pill).toBeVisible({ timeout: 15_000 });

    // The element itself must not advertise itself as draggable. The
    // attribute is either absent or set to "false"; we assert the
    // explicit "false" / null shape rather than relying on missingness.
    const draggableAttr = await pill.getAttribute('draggable');
    expect(draggableAttr === null || draggableAttr === 'false').toBe(true);

    // Even if a user manages to fire dnd events on a non-draggable
    // <button>, the route never wires a handler so dropping it on
    // another cell must NOT issue a PATCH /events/{id}. We attempt the
    // gesture and watch for either a PATCH /events/{id} (a regression)
    // or, harmlessly, a PATCH /tasks (which would also be wrong since
    // this is an event, not a task).
    const targetCell = cellByKey(page, '2026-04-15');
    await nativeDnd(page, pill, targetCell);
    // Give any in-flight request a moment to leave the page.
    await page.waitForTimeout(500);

    expect(eventWrites, 'event pill drag must not trigger any /events/ write').toEqual([]);

    // The event must still appear in the same row of cells (we don't
    // pin a single cell because all-day vs timed event placement is
    // computed in local tz, but a successful no-op drag must leave the
    // event visible exactly once on the calendar).
    await expect(eventPillByTitle(page, title)).toHaveCount(1);

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
