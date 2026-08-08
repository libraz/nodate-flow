/**
 * Calendar event shift propose / apply E2E.
 *
 * Covers apps/flow-web/src/features/events/shift-event-dialog.tsx, opened
 * from the event detail page header. The dialog respects the actor's
 * `me.calendarShiftDefault` preference (`ask` / `sync_always` /
 * `task_only_always`) — see the file-level docstring on shift-event-dialog
 * for the state machine details.
 *
 * Each test seeds an event + a single contributes_to-linked task via REST
 * so the propose-shift response carries a non-empty safe-task list with
 * no conflicts (the same task is not linked to any other event).
 *
 * Cases:
 *   A. ask mode (default for new users) — shift dialog opens in pick
 *      phase, preview advances to confirm with safe task pre-checked,
 *      Apply moves event + checked task.
 *   B. sync_always — set pref via PATCH /me, reopen dialog → jumps
 *      straight to confirm with safe task pre-checked.
 *   C. task_only_always — set pref, open dialog → confirm phase with
 *      no tasks ticked.
 *   D. "Always use this choice" checkbox — tick + Apply with all-checked
 *      → server stores `sync_always` on /me; reload + reopen confirms
 *      the new shortcut path.
 */

import { expect, type Page, test } from '@playwright/test';

import enCalEvents from '../locales/en/calendar-events.json' with { type: 'json' };
import {
  API_BASE_URL,
  AUTH_API_URL,
  cleanupTenant,
  createCalendarEvent,
  createTestTenant,
  ensurePersonalCalendar,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';

const copy = {
  trigger: enCalEvents.event.shift.trigger,
  dialogTitle: enCalEvents.event.shift.dialog.title,
  pickLabel: enCalEvents.event.shift.pick.label,
  preview: enCalEvents.event.shift.pick.preview,
  apply: enCalEvents.event.shift.confirm.apply,
  rememberDefault: enCalEvents.event.shift.default_remember,
  defaultSavedToast: enCalEvents.event.shift.default_saved_toast,
} as const;

interface SeedResult {
  calendarId: string;
  eventId: string;
  taskId: string;
  eventStartAt: number;
}

/**
 * Seeds the minimum graph for a propose-shift call:
 *   - one personal calendar (auto-created on first list).
 *   - one calendar event tomorrow at 10:00 local.
 *   - one task with a due_on of tomorrow.
 *   - one task→event link with relation='contributes_to' so the task
 *     surfaces in the safeTasks list of the proposal.
 */
async function seedShiftGraph(tenant: TestTenant): Promise<SeedResult> {
  const cal = await ensurePersonalCalendar(tenant);

  // Build tomorrow 10:00–11:00 local time.
  const tomorrow = new Date();
  tomorrow.setDate(tomorrow.getDate() + 1);
  tomorrow.setHours(10, 0, 0, 0);
  const startAt = Math.floor(tomorrow.getTime() / 1000);
  const endAt = startAt + 3600;

  const event = await createCalendarEvent(tenant, cal.id, {
    title: `Shift event ${Date.now().toString(36)}`,
    startAt,
    endAt,
    kind: 'event',
  });

  // Create a task with the same due date so the link clearly belongs to
  // this event in time as well as in the join table.
  const dueOn = (() => {
    const y = tomorrow.getFullYear();
    const m = String(tomorrow.getMonth() + 1).padStart(2, '0');
    const d = String(tomorrow.getDate()).padStart(2, '0');
    return `${y}-${m}-${d}`;
  })();
  const taskRes = await fetch(`${API_BASE_URL}/tasks`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
    body: JSON.stringify({
      title: `Shift task ${Date.now().toString(36)}`,
      dueOn,
      projectId: tenant.projectId,
    }),
  });
  if (!taskRes.ok) {
    throw new Error(`POST /tasks -> ${taskRes.status} ${await taskRes.text()}`);
  }
  const task = (await taskRes.json()) as { id: string; title: string };

  const linkRes = await fetch(`${API_BASE_URL}/tasks/${task.id}/links`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
    body: JSON.stringify({
      eventId: event.id,
      relation: 'contributes_to',
      sortWeight: 0,
    }),
  });
  if (!linkRes.ok) {
    throw new Error(`POST /tasks/{id}/links -> ${linkRes.status} ${await linkRes.text()}`);
  }

  return {
    calendarId: cal.id,
    eventId: event.id,
    taskId: task.id,
    eventStartAt: startAt,
  };
}

/**
 * Updates the caller's `calendarShiftDefault` preference via PATCH /me on
 * the auth-api. Used to set up the shortcut paths in cases B and C.
 *
 * Returns `true` on success, `false` if the running auth-api binary
 * rejects the property as "unexpected" (HTTP 422). The dto.go source
 * declares the field, but builds without it have been observed in the
 * test environment — when that happens we surface a fixme-style skip
 * rather than fail the test, since the front-end dialog also depends on
 * the same field being settable for the shortcut path to exercise.
 */
async function setShiftDefault(
  tenant: TestTenant,
  pref: 'ask' | 'sync_always' | 'task_only_always',
): Promise<void> {
  const res = await fetch(`${AUTH_API_URL}/me`, {
    method: 'PATCH',
    headers: {
      'Content-Type': 'application/json',
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
    body: JSON.stringify({ calendarShiftDefault: pref }),
  });
  // `calendarShiftDefault` is part of the shipped PATCH /me schema, so a
  // 422 means the running auth-api is not the one this suite is written
  // against — an environment fault, not a case to skip past. Three of
  // the four cases in this file depend on the preference, and skipping
  // them left the shortcut paths (sync_always / task_only_always) and
  // the "always use this choice" persistence unexercised behind a green
  // report.
  if (!res.ok) {
    throw new Error(
      `PATCH /me { calendarShiftDefault: ${pref} } -> ${res.status} ${await res.text()}`,
    );
  }
}

/**
 * Reads back the actor's calendarShiftDefault from the server. Used in
 * case D to assert the "Always use this choice" path persisted.
 */
async function readShiftDefault(tenant: TestTenant): Promise<string | undefined> {
  const res = await fetch(`${AUTH_API_URL}/me`, {
    headers: {
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
  });
  if (!res.ok) {
    throw new Error(`GET /me -> ${res.status} ${await res.text()}`);
  }
  const body = (await res.json()) as { calendarShiftDefault?: string };
  return body.calendarShiftDefault;
}

/** Navigates to the event detail page and waits for the shift trigger to mount. */
async function openEventDetail(
  page: Page,
  tenant: TestTenant,
  calendarId: string,
  eventId: string,
): Promise<void> {
  await page.goto(`/workspaces/${tenant.workspaceId}/calendars/${calendarId}/events/${eventId}`);
  await expect(page.getByRole('button', { name: copy.trigger })).toBeVisible({ timeout: 15_000 });
}

test.describe('event detail — shift dialog', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('A: ask mode previews then applies with the safe task pre-checked', async ({ page }) => {
    tenant = await createTestTenant();
    const seed = await seedShiftGraph(tenant);

    await injectAuth(page.context(), tenant);
    await openEventDetail(page, tenant, seed.calendarId, seed.eventId);

    await page.getByRole('button', { name: copy.trigger }).click();

    const dialog = page.getByRole('dialog', { name: copy.dialogTitle });
    await expect(dialog).toBeVisible({ timeout: 5_000 });

    // Pick phase carries the datetime input — bump start to +1 hour.
    const dtInput = dialog.getByLabel(copy.pickLabel);
    const newStart = new Date((seed.eventStartAt + 3600) * 1000);
    const pad = (n: number): string => String(n).padStart(2, '0');
    const formatted = `${newStart.getFullYear()}-${pad(newStart.getMonth() + 1)}-${pad(newStart.getDate())}T${pad(newStart.getHours())}:${pad(newStart.getMinutes())}`;
    await dtInput.fill(formatted);

    await dialog.getByRole('button', { name: copy.preview }).click();

    // Confirm phase: the safe task row mounts with its checkbox pre-checked.
    const taskCheckbox = dialog.getByRole('checkbox').filter({ hasText: '' }).first();
    // Be more specific — pick the checkbox whose aria-label is the task
    // title (CandidateRow sets aria-label={candidate.taskTitle}).
    const safeCheckbox = dialog.getByRole('checkbox', { name: /Shift task /i });
    await expect(safeCheckbox).toBeChecked({ timeout: 10_000 });
    // (Reference variable to silence lint about unused.)
    void taskCheckbox;

    await dialog.getByRole('button', { name: copy.apply }).click();
    await expect(dialog).toBeHidden({ timeout: 10_000 });

    // Cross-check: the event's startAt advanced by ~3600s.
    const detailRes = await fetch(
      `${API_BASE_URL}/workspaces/${tenant.workspaceId}/calendars/${seed.calendarId}/events/${seed.eventId}`,
      {
        headers: {
          accept: 'application/json',
          authorization: `Bearer ${tenant.accessToken}`,
        },
      },
    );
    expect(detailRes.ok).toBeTruthy();
    const evt = (await detailRes.json()) as { startAt: number };
    expect(Math.abs(evt.startAt - (seed.eventStartAt + 3600))).toBeLessThan(120);
  });

  test('B: sync_always pref jumps straight to confirm with task pre-checked', async ({ page }) => {
    tenant = await createTestTenant();
    await setShiftDefault(tenant, 'sync_always');
    const seed = await seedShiftGraph(tenant);

    await injectAuth(page.context(), tenant);
    await openEventDetail(page, tenant, seed.calendarId, seed.eventId);

    await page.getByRole('button', { name: copy.trigger }).click();

    const dialog = page.getByRole('dialog', { name: copy.dialogTitle });
    await expect(dialog).toBeVisible({ timeout: 5_000 });

    // The dialog's auto-propose effect skips the pick phase: the Apply
    // button is the visible primary CTA, and the safe task is pre-ticked.
    await expect(dialog.getByRole('button', { name: copy.apply })).toBeVisible({ timeout: 10_000 });
    await expect(dialog.getByLabel(copy.pickLabel)).toHaveCount(0);

    const safeCheckbox = dialog.getByRole('checkbox', { name: /Shift task /i });
    await expect(safeCheckbox).toBeChecked();
  });

  test('C: task_only_always pref opens confirm with no task ticked', async ({ page }) => {
    tenant = await createTestTenant();
    await setShiftDefault(tenant, 'task_only_always');
    const seed = await seedShiftGraph(tenant);

    await injectAuth(page.context(), tenant);
    await openEventDetail(page, tenant, seed.calendarId, seed.eventId);

    await page.getByRole('button', { name: copy.trigger }).click();

    const dialog = page.getByRole('dialog', { name: copy.dialogTitle });
    await expect(dialog).toBeVisible({ timeout: 5_000 });

    // Same shortcut path as sync_always — confirm phase mounts directly —
    // but the safe task starts unticked.
    await expect(dialog.getByRole('button', { name: copy.apply })).toBeVisible({ timeout: 10_000 });
    const safeCheckbox = dialog.getByRole('checkbox', { name: /Shift task /i });
    await expect(safeCheckbox).not.toBeChecked();
  });

  test('D: "Always" checkbox + apply persists sync_always to /me', async ({ page }) => {
    tenant = await createTestTenant();
    // Establish the starting preference explicitly. The dialog's
    // fire-and-forget PATCH /me at the end of this flow writes the same
    // field, so if the server rejected it this test would otherwise wait
    // out a toast that can never arrive.
    await setShiftDefault(tenant, 'ask');
    const seed = await seedShiftGraph(tenant);

    await injectAuth(page.context(), tenant);
    await openEventDetail(page, tenant, seed.calendarId, seed.eventId);

    await page.getByRole('button', { name: copy.trigger }).click();

    const dialog = page.getByRole('dialog', { name: copy.dialogTitle });
    await expect(dialog).toBeVisible({ timeout: 5_000 });

    // Pick phase: nudge start by +30m and preview.
    const newStart = new Date((seed.eventStartAt + 1800) * 1000);
    const pad = (n: number): string => String(n).padStart(2, '0');
    const formatted = `${newStart.getFullYear()}-${pad(newStart.getMonth() + 1)}-${pad(newStart.getDate())}T${pad(newStart.getHours())}:${pad(newStart.getMinutes())}`;
    await dialog.getByLabel(copy.pickLabel).fill(formatted);
    await dialog.getByRole('button', { name: copy.preview }).click();

    // Confirm phase: safe task is pre-ticked. Tick the "Always use this
    // choice for shift" checkbox so apply derives sync_always.
    await expect(dialog.getByRole('button', { name: copy.apply })).toBeVisible({ timeout: 10_000 });
    await dialog.getByRole('checkbox', { name: copy.rememberDefault }).check();
    await dialog.getByRole('button', { name: copy.apply }).click();

    await expect(dialog).toBeHidden({ timeout: 10_000 });

    // The fire-and-forget PATCH /me posts to /me; an info toast confirms.
    await expect(page.getByText(copy.defaultSavedToast)).toBeVisible({ timeout: 10_000 });

    // Cross-check via REST: the server-side preference is now sync_always.
    const t = tenant;
    await expect.poll(async () => readShiftDefault(t), { timeout: 10_000 }).toBe('sync_always');
  });
});
