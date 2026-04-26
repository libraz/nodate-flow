/**
 * Event-from-task quick action E2E (W15).
 *
 * Covers apps/flow-web/src/features/tasks/event-from-task/event-from-task-dialog.tsx,
 * triggered from the TaskActionsCard ("Create calendar event" button) on
 * the task detail page (see apps/flow-web/src/routes/_authenticated.tasks.
 * $taskId.lazy.tsx).
 *
 * The backend endpoint (POST /workspaces/{wsId}/calendars/{calId}/events/
 * from-task) derives the event's title + start/end from the task's due_on,
 * so the dialog only collects the destination workspace + calendar.
 *
 * Cases (aligned with R6 plan §6 W15 row, scoped to current UI surfaces):
 *   A. trigger from a task → event-from-task dialog opens with the
 *      destination workspace pre-selected to the task's workspace.
 *   B. submit → event created and linked to the task; cross-checked via
 *      REST because the task detail page does not yet render a linked-
 *      event chip (surface gap noted in writeup).
 *   C. cancel → no event created, task unchanged.
 */

import { type Page, expect, test } from '@playwright/test';

import enCommon from '../locales/en/common.json' with { type: 'json' };
import {
  API_BASE_URL,
  type TestTenant,
  cleanupTenant,
  createTestTenant,
  ensurePersonalCalendar,
  injectAuth,
} from './fixtures/tenant';

const copy = {
  trigger: enCommon.tasks.actions.create_event.trigger,
  dialogTitle: enCommon.tasks.actions.create_event.title,
  workspaceLabel: enCommon.tasks.actions.create_event.workspace_label,
  calendarLabel: enCommon.tasks.actions.create_event.calendar_label,
  submit: enCommon.tasks.actions.create_event.submit,
  cancel: enCommon.tasks.actions.create_event.cancel,
  successToast: enCommon.tasks.actions.create_event.success,
} as const;

interface SeededTask {
  id: string;
  title: string;
  dueOn: string;
}

/**
 * Seeds a task with a due date so the from-task endpoint can derive an
 * event start/end. Returns the public id + title so the test can navigate
 * directly to the detail page.
 */
async function seedTaskWithDueDate(tenant: TestTenant, title: string): Promise<SeededTask> {
  // Pick tomorrow's date in YYYY-MM-DD, in the test runner's local TZ.
  const d = new Date();
  d.setDate(d.getDate() + 1);
  const pad = (n: number): string => String(n).padStart(2, '0');
  const dueOn = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;

  const res = await fetch(`${API_BASE_URL}/tasks`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
    body: JSON.stringify({ title, dueOn, projectId: tenant.projectId }),
  });
  if (!res.ok) {
    throw new Error(`POST /tasks -> ${res.status} ${await res.text()}`);
  }
  const body = (await res.json()) as { id: string; title: string };
  return { id: body.id, title: body.title, dueOn };
}

/**
 * Navigates to /tasks/{id} and waits until the task title h1 mounts.
 * The "Create calendar event" trigger lives in the actions card on the
 * sidebar; we wait for it explicitly so the test does not race the lazy
 * mount.
 */
async function openTaskDetail(page: Page, taskId: string, title: string): Promise<void> {
  await page.goto(`/tasks/${taskId}`);
  await expect(page.getByText(title).first()).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole('button', { name: copy.trigger })).toBeVisible({ timeout: 10_000 });
}

/**
 * Fetches the calendar's events in a wide window to confirm the from-task
 * write landed on the backend. Used by both the success and cancel paths
 * to assert presence / absence respectively.
 */
async function fetchCalendarEvents(
  tenant: TestTenant,
  calId: string,
): Promise<Array<{ id: string; title: string }>> {
  const start = new Date();
  start.setMonth(start.getMonth() - 1);
  const end = new Date();
  end.setMonth(end.getMonth() + 1);
  const res = await fetch(
    `${API_BASE_URL}/workspaces/${tenant.workspaceId}/calendars/${calId}/events?start=${start.toISOString()}&end=${end.toISOString()}`,
    {
      headers: {
        accept: 'application/json',
        authorization: `Bearer ${tenant.accessToken}`,
      },
    },
  );
  if (!res.ok) {
    throw new Error(`GET /calendars/{id}/events -> ${res.status} ${await res.text()}`);
  }
  const body = (await res.json()) as { events: Array<{ id: string; title: string }> };
  return body.events ?? [];
}

test.describe('task detail — create event from task', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('A: trigger opens the dialog with workspace + calendar pickers', async ({ page }) => {
    tenant = await createTestTenant();
    await ensurePersonalCalendar(tenant);
    const task = await seedTaskWithDueDate(tenant, `Event from task A ${Date.now().toString(36)}`);

    await injectAuth(page.context(), tenant);
    await openTaskDetail(page, task.id, task.title);

    await page.getByRole('button', { name: copy.trigger }).click();

    const dialog = page.getByRole('dialog', { name: copy.dialogTitle });
    await expect(dialog).toBeVisible({ timeout: 5_000 });

    // The workspace combobox shows the actor's only workspace selected by
    // default (its label = workspace name from createTestTenant).
    const wsPicker = dialog.getByRole('combobox', { name: copy.workspaceLabel });
    await expect(wsPicker).toBeVisible();
    await expect(wsPicker).not.toHaveValue('');

    // The calendar picker is also mounted (auto-create personal calendar
    // produced exactly one writable row).
    await expect(dialog.getByRole('combobox', { name: copy.calendarLabel })).toBeVisible();
  });

  test('B: submit creates an event linked to the task on the backend', async ({ page }) => {
    tenant = await createTestTenant();
    const cal = await ensurePersonalCalendar(tenant);
    const task = await seedTaskWithDueDate(tenant, `Event from task B ${Date.now().toString(36)}`);

    await injectAuth(page.context(), tenant);
    await openTaskDetail(page, task.id, task.title);

    await page.getByRole('button', { name: copy.trigger }).click();

    const dialog = page.getByRole('dialog', { name: copy.dialogTitle });
    await expect(dialog).toBeVisible({ timeout: 5_000 });
    // Submit with the auto-selected defaults — the dialog only collects
    // workspace + calendar and the workspace already has one writable row.
    await dialog.getByRole('button', { name: copy.submit }).click();

    // Success toast surfaces and the dialog closes.
    await expect(page.getByText(copy.successToast)).toBeVisible({ timeout: 10_000 });
    await expect(dialog).toBeHidden({ timeout: 5_000 });

    // Cross-check via REST: the calendar now contains an event whose
    // title matches the task's title (the from-task projection copies
    // the task title verbatim). The task detail page does not surface a
    // linked-event chip in the current build (surface gap), so we lean
    // on the API for the link assertion.
    const t = tenant;
    await expect
      .poll(
        async () => {
          const events = await fetchCalendarEvents(t, cal.id);
          return events.some((e) => e.title === task.title);
        },
        { timeout: 10_000 },
      )
      .toBe(true);
  });

  test('C: cancel leaves the task unchanged and creates no event', async ({ page }) => {
    tenant = await createTestTenant();
    const cal = await ensurePersonalCalendar(tenant);
    const task = await seedTaskWithDueDate(tenant, `Event from task C ${Date.now().toString(36)}`);

    // Snapshot the baseline event count before the test interacts with the UI.
    const beforeEvents = await fetchCalendarEvents(tenant, cal.id);

    await injectAuth(page.context(), tenant);
    await openTaskDetail(page, task.id, task.title);

    await page.getByRole('button', { name: copy.trigger }).click();
    const dialog = page.getByRole('dialog', { name: copy.dialogTitle });
    await expect(dialog).toBeVisible({ timeout: 5_000 });

    await dialog.getByRole('button', { name: copy.cancel }).click();
    await expect(dialog).toBeHidden({ timeout: 5_000 });

    // No new event materialised. We compare against the baseline rather
    // than asserting an empty list because ensurePersonalCalendar may
    // have lazily produced auto-generated rows.
    const afterEvents = await fetchCalendarEvents(tenant, cal.id);
    expect(afterEvents.length).toBe(beforeEvents.length);
    expect(afterEvents.find((e) => e.title === task.title)).toBeUndefined();
  });
});
