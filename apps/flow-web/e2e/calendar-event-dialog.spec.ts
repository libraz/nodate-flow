/**
 * Unified calendar event dialog E2E.
 *
 * Regression coverage for apps/flow-web/src/features/calendar-events/
 * event-dialog.tsx, driven through the `/calendar` route against the
 * real flow-api (tasks) and time-api (calendar events) backends.
 *
 * Each test creates its own tenant via REST + seeds any prerequisites
 * (personal calendar, pre-existing event, etc.) so the suite stays
 * parallel-safe and never relies on shared mutable state. The vitest
 * unit tests in ../src/features/calendar-events/__tests__/event-dialog
 * .test.tsx already cover the pure state-machine edges; here we only
 * exercise what changes once a live backend and a real browser are in
 * the loop.
 *
 * Kind-matrix covered:
 *   - event (create happy path + ja locale)
 *   - block (with preset chip + diagonal-stripe pill style)
 *   - milestone (bottom-border-only pill style)
 *   - task  (POST /tasks + project picker appears / calendar picker hides)
 *   - edit + delete on an existing event
 *   - validation (empty title, end before start)
 *
 * All assertions read locale strings off the loaded JSON so they stay
 * in sync if copy changes.
 */

import { type Locator, type Page, expect, test } from '@playwright/test';

import enCal from '../locales/en/calendar-events.json' with { type: 'json' };
import jaCal from '../locales/ja/calendar-events.json' with { type: 'json' };
import {
  TIME_API_URL,
  type TestTenant,
  cleanupTenant,
  createCalendarEvent,
  createTestTenant,
  ensurePersonalCalendar,
  injectAuth,
} from './fixtures/tenant';

/* ── locale copy ────────────────────────────────────────────────── */

const copy = {
  en: {
    titleField: enCal.field.title,
    locationField: enCal.field.location,
    projectField: enCal.field.project,
    calendarField: enCal.field.calendar,
    createSubmit: enCal.action.submit.create,
    editSubmit: enCal.action.submit.edit,
    deleteAction: enCal.action.delete,
    createEventTitle: enCal.dialog.title.create.event,
    createBlockTitle: enCal.dialog.title.create.block,
    createMilestoneTitle: enCal.dialog.title.create.milestone,
    createTaskTitle: enCal.dialog.title.create.task,
    editEventTitle: enCal.dialog.title.edit.event,
    toastEventCreated: enCal.toast.created.event,
    toastBlockCreated: enCal.toast.created.block,
    toastMilestoneCreated: enCal.toast.created.milestone,
    toastTaskCreated: enCal.toast.created.task,
    toastEventUpdated: enCal.toast.updated.event,
    toastEventDeleted: enCal.toast.deleted.event,
    kindTask: enCal.kind.task,
    kindEvent: enCal.kind.event,
    kindBlock: enCal.kind.block,
    kindMilestone: enCal.kind.milestone,
    presetWorking: enCal.blockLabel.preset.working,
    validationTitleRequired: enCal.validation.titleRequired,
    validationEndBeforeStart: enCal.validation.endBeforeStart,
  },
  ja: {
    titleField: jaCal.field.title,
    createSubmit: jaCal.action.submit.create,
    createEventTitle: jaCal.dialog.title.create.event,
    toastEventCreated: jaCal.toast.created.event,
  },
} as const;

/* ── helpers ────────────────────────────────────────────────────── */

/**
 * Navigates to /calendar and returns when the month grid has mounted.
 * The h1 "Calendar" / "カレンダー" is the most stable readiness signal.
 */
async function openCalendar(page: Page): Promise<void> {
  await page.goto('/calendar');
  await expect(
    page.getByRole('heading', { level: 1, name: /^(Calendar|カレンダー)$/ }),
  ).toBeVisible({ timeout: 15_000 });
}

/**
 * Locates today's grid cell. Uses the cell's date-number span — the
 * cell itself is a div[role=button] and there's exactly one cell per
 * in-month date. We pin via the `click_to_add` title attribute which
 * is only set on in-month cells, then narrow to the one whose visible
 * digit matches today's day-of-month.
 */
async function findTodayCell(page: Page): Promise<Locator> {
  const today = new Date();
  const day = today.getDate();
  // All in-month cells have role=button + title="Click to add". Filter
  // by the exact day-of-month text inside. Using exact to avoid "1"
  // matching "15".
  const cells = page.locator('[role="button"][title]').filter({
    has: page.getByText(String(day), { exact: true }),
  });
  // There can be up to 6 such cells across adjacent months if the
  // grid's leading/trailing days happen to share the day number. We
  // want the one that is actually clickable (full opacity). All
  // in-month cells have opacity=1, leading/trailing ones 0.4. Playwright
  // sees the first visible one; narrow explicitly to the in-month cell
  // by matching the title attribute "Click to add" / "クリックして追加".
  // (Both the rendered title comes from common.calendar.click_to_add.)
  const cell = cells.first();
  await expect(cell).toBeVisible({ timeout: 10_000 });
  return cell;
}

/**
 * Opens the EventDialog by clicking the blank area of a cell. We click
 * on the day-number span so the click doesn't accidentally land on a
 * nested anchor/button (the route-level click handler bails out via
 * `closest('a, button')` in that case).
 */
async function openCreateDialog(page: Page, opts: { shift?: boolean } = {}): Promise<Locator> {
  const cell = await findTodayCell(page);
  const clickOpts = opts.shift ? { modifiers: ['Shift'] as const } : undefined;
  // Click near the top-left where the day number sits; avoid the pill
  // list area which may intercept.
  await cell.click({ position: { x: 8, y: 8 }, ...(clickOpts ?? {}) });
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible({ timeout: 5_000 });
  return dialog;
}

/**
 * Register a pageerror listener that collects any uncaught exceptions.
 * Returns the backing array directly so callers can `expect(errors).toEqual([])`
 * without an extra .errors dereference.
 */
function attachConsoleErrorGuard(page: Page): string[] {
  const errors: string[] = [];
  page.on('pageerror', (err) => {
    errors.push(err.message);
  });
  return errors;
}

/** Unix seconds for a given local-time HH:MM today. */
function todayAtUnix(hhmm: string): number {
  const d = new Date();
  const [h, m] = hhmm.split(':').map(Number);
  d.setHours(h ?? 0, m ?? 0, 0, 0);
  return Math.floor(d.getTime() / 1000);
}

/**
 * Locator for the event pill rendered for a given event title. The grid
 * cell itself is a `div[role=button]` whose accessible name concatenates
 * every descendant's text (including the pill's `aria-label`), so a
 * `getByRole('button', { name: /Open event: …/ })` query hits both the
 * cell and the pill in strict mode. Pin the real pill via its HTML tag
 * + aria-label attribute substring match.
 */
function eventPill(page: Page, title: string): Locator {
  // CSS attribute substring selector is safe for our test titles because
  // we generate them with Date.now().toString(36) suffixes and only
  // ASCII/spaces in between.
  return page.locator(`button[aria-label^="Open event: "][aria-label*=${JSON.stringify(title)}]`);
}

/* ── lifecycle ──────────────────────────────────────────────────── */

test.describe('calendar event dialog', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  /* ─────────────────────────────────────────────────────────── */

  test('creates an event via the dialog happy path', async ({ page }) => {
    const errors = attachConsoleErrorGuard(page);
    tenant = await createTestTenant();
    await ensurePersonalCalendar(tenant);

    await injectAuth(page.context(), tenant);
    await openCalendar(page);
    const dialog = await openCreateDialog(page);

    // Dialog header locks to the create-event copy (default kind = event).
    await expect(dialog.getByRole('heading', { name: copy.en.createEventTitle })).toBeVisible();

    const title = `Sprint planning ${Date.now().toString(36)}`;
    await dialog.getByLabel(copy.en.titleField).fill(title);

    // Time pickers render as buttons labelled "HH:MM". Change start →
    // 10:00 and end → 11:00 by clicking the triggers and picking slots.
    const timeTriggers = dialog.getByRole('button', { name: /^\d{2}:\d{2}$/ });
    await expect(timeTriggers).toHaveCount(2);
    await timeTriggers.nth(0).click();
    // Time picker popover is a listbox with options named "HH:MM".
    await page.getByRole('option', { name: '10:00' }).click();
    await timeTriggers.nth(1).click();
    await page.getByRole('option', { name: '11:00' }).click();

    await dialog.getByRole('button', { name: copy.en.createSubmit }).click();

    // Dialog closes + success toast fires + pill renders.
    await expect(dialog).toBeHidden({ timeout: 10_000 });
    await expect(page.getByText(copy.en.toastEventCreated)).toBeVisible({ timeout: 5_000 });
    // The new pill carries aria-label = "Open event: <title> in <ws>".
    await expect(eventPill(page, title)).toBeVisible({ timeout: 10_000 });

    expect(errors).toEqual([]);
  });

  /* ─────────────────────────────────────────────────────────── */

  test('shift-click defaults to block and the Working preset applies', async ({ page }) => {
    const errors = attachConsoleErrorGuard(page);
    tenant = await createTestTenant();
    await ensurePersonalCalendar(tenant);

    await injectAuth(page.context(), tenant);
    await openCalendar(page);
    const dialog = await openCreateDialog(page, { shift: true });

    // Kind picker is a radiogroup; the "block" radio should be checked
    // on mount (shift-click initialItemKind = 'block').
    const blockRadio = dialog.getByRole('radio', { name: copy.en.kindBlock });
    await expect(blockRadio).toHaveAttribute('aria-checked', 'true');
    await expect(dialog.getByRole('heading', { name: copy.en.createBlockTitle })).toBeVisible();

    // Confirm the Working preset is default-pressed.
    const working = dialog.getByRole('button', { name: copy.en.presetWorking });
    await expect(working).toHaveAttribute('aria-pressed', 'true');

    const title = `Deep work ${Date.now().toString(36)}`;
    await dialog.getByLabel(copy.en.titleField).fill(title);
    await dialog.getByRole('button', { name: copy.en.createSubmit }).click();

    await expect(dialog).toBeHidden({ timeout: 10_000 });
    await expect(page.getByText(copy.en.toastBlockCreated)).toBeVisible({ timeout: 5_000 });

    // Toggle the Blocks layer on — the route hides blocks by default.
    await page.getByRole('button', { name: /^(Blocks|ブロック)$/ }).click();

    const pill = eventPill(page, title);
    await expect(pill).toBeVisible({ timeout: 10_000 });

    // Block pills carry a diagonal-stripe backgroundImage (via inline
    // style). This is the most reliable kind marker we have — the
    // route's pillStyleForKind('block') only emits the stripe gradient
    // for blocks. Free kind uses a dashed border; event/milestone omit
    // backgroundImage entirely.
    const bgImage = await pill.evaluate((el) => getComputedStyle(el).backgroundImage);
    expect(bgImage).toContain('repeating-linear-gradient');

    expect(errors).toEqual([]);
  });

  /* ─────────────────────────────────────────────────────────── */

  test('switching to milestone creates a milestone pill', async ({ page }) => {
    const errors = attachConsoleErrorGuard(page);
    tenant = await createTestTenant();
    await ensurePersonalCalendar(tenant);

    await injectAuth(page.context(), tenant);
    await openCalendar(page);
    const dialog = await openCreateDialog(page);

    // Switch kind to Milestone via the segmented control.
    await dialog.getByRole('radio', { name: copy.en.kindMilestone }).click();
    await expect(dialog.getByRole('heading', { name: copy.en.createMilestoneTitle })).toBeVisible();

    const title = `Release ${Date.now().toString(36)}`;
    await dialog.getByLabel(copy.en.titleField).fill(title);
    await dialog.getByRole('button', { name: copy.en.createSubmit }).click();

    await expect(dialog).toBeHidden({ timeout: 10_000 });
    await expect(page.getByText(copy.en.toastMilestoneCreated)).toBeVisible({ timeout: 5_000 });

    const pill = eventPill(page, title);
    await expect(pill).toBeVisible({ timeout: 10_000 });

    // Milestone pills are styled with a border-block-end accent and no
    // solid background. Check both properties — the route's
    // pillStyleForKind('milestone') sets background:'transparent'
    // + borderBlockEnd:'2px solid var(--nf-cal-milestone-color)'.
    const pillStyle = await pill.evaluate((el) => {
      const cs = getComputedStyle(el);
      return {
        background: cs.backgroundColor,
        borderBottom: cs.borderBottomStyle,
        borderBottomWidth: cs.borderBottomWidth,
        backgroundImage: cs.backgroundImage,
      };
    });
    // 'transparent' resolves to rgba(0, 0, 0, 0) in computed style.
    expect(pillStyle.background).toBe('rgba(0, 0, 0, 0)');
    expect(pillStyle.borderBottom).toBe('solid');
    // The stripe gradient (block kind) must not appear.
    expect(pillStyle.backgroundImage).not.toContain('repeating-linear-gradient');

    expect(errors).toEqual([]);
  });

  /* ─────────────────────────────────────────────────────────── */

  test('task kind hides the calendar picker and surfaces the project picker', async ({ page }) => {
    const errors = attachConsoleErrorGuard(page);
    tenant = await createTestTenant();
    // Calendar auto-create is a no-op for the task branch but the
    // default-kind (event) still fetches the calendar list on mount,
    // so keep it.
    await ensurePersonalCalendar(tenant);

    await injectAuth(page.context(), tenant);
    await openCalendar(page);
    const dialog = await openCreateDialog(page);

    await dialog.getByRole('radio', { name: copy.en.kindTask }).click();
    await expect(dialog.getByRole('heading', { name: copy.en.createTaskTitle })).toBeVisible();

    // The project select appears (we seeded one project via
    // createTestTenant) and the calendar combobox disappears.
    await expect(dialog.getByLabel(copy.en.projectField)).toBeVisible();
    await expect(dialog.getByLabel(copy.en.calendarField)).toHaveCount(0);

    const title = `Ship docs ${Date.now().toString(36)}`;
    await dialog.getByLabel(copy.en.titleField).fill(title);
    await dialog.getByRole('button', { name: copy.en.createSubmit }).click();

    await expect(dialog).toBeHidden({ timeout: 10_000 });
    await expect(page.getByText(copy.en.toastTaskCreated)).toBeVisible({ timeout: 5_000 });

    // The task pill on the calendar route links to /tasks/{id}; assert
    // it's rendered with the seeded title. Anchor pill accessibility
    // is minimal — pin via visible text.
    await expect(page.getByText(title).first()).toBeVisible({ timeout: 10_000 });

    // Cross-check the /me/tasks-with-dates endpoint — the UI and the
    // API must agree. Without this the test passes on an optimistic
    // render even if the POST silently 500ed.
    const today = new Date();
    const y = today.getFullYear();
    const m = String(today.getMonth() + 1).padStart(2, '0');
    const d = String(today.getDate()).padStart(2, '0');
    const iso = `${y}-${m}-${d}`;
    const res = await fetch(
      `${process.env.NF_API_URL ?? 'http://localhost:8080'}/me/tasks-with-dates?from=${iso}&to=${iso}`,
      {
        headers: {
          accept: 'application/json',
          authorization: `Bearer ${tenant.accessToken}`,
        },
      },
    );
    expect(res.ok).toBeTruthy();
    const body = (await res.json()) as { tasks: Array<{ title: string; dueOn?: string }> };
    const found = body.tasks.find((t) => t.title === title);
    expect(found).toBeDefined();
    expect(found?.dueOn).toBe(iso);

    expect(errors).toEqual([]);
  });

  /* ─────────────────────────────────────────────────────────── */

  test('edits an existing event in edit mode', async ({ page }) => {
    const errors = attachConsoleErrorGuard(page);
    tenant = await createTestTenant();
    const cal = await ensurePersonalCalendar(tenant);

    const originalTitle = `Edit target ${Date.now().toString(36)}`;
    await createCalendarEvent(tenant, cal.id, {
      title: originalTitle,
      startAt: todayAtUnix('14:00'),
      endAt: todayAtUnix('15:00'),
      kind: 'event',
    });

    await injectAuth(page.context(), tenant);
    await openCalendar(page);

    // Click the seeded pill to open edit mode.
    const pill = eventPill(page, originalTitle);
    await expect(pill).toBeVisible({ timeout: 15_000 });
    await pill.click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible({ timeout: 5_000 });

    // Header switches to the edit copy.
    await expect(dialog.getByRole('heading', { name: copy.en.editEventTitle })).toBeVisible();

    // Title input is prefilled with the seeded value.
    const titleInput = dialog.getByLabel(copy.en.titleField);
    await expect(titleInput).toHaveValue(originalTitle);

    // Delete button is visible and the task kind is locked.
    await expect(dialog.getByRole('button', { name: copy.en.deleteAction })).toBeVisible();
    const taskRadio = dialog.getByRole('radio', { name: copy.en.kindTask });
    await expect(taskRadio).toBeDisabled();

    const newTitle = `${originalTitle} (edited)`;
    await titleInput.fill(newTitle);
    await dialog.getByRole('button', { name: copy.en.editSubmit }).click();

    await expect(dialog).toBeHidden({ timeout: 10_000 });
    await expect(page.getByText(copy.en.toastEventUpdated)).toBeVisible({ timeout: 5_000 });
    await expect(eventPill(page, newTitle)).toBeVisible({ timeout: 10_000 });
    // The old title should no longer be rendered as a pill. Use an
    // exact aria-label match so `newTitle` (which extends `originalTitle`)
    // doesn't accidentally satisfy the "original pill still there" check.
    await expect(
      page.locator(`button[aria-label^="Open event: ${originalTitle} in "]`),
    ).toHaveCount(0);

    expect(errors).toEqual([]);
  });

  /* ─────────────────────────────────────────────────────────── */

  test('deletes an existing event from edit mode', async ({ page }) => {
    const errors = attachConsoleErrorGuard(page);
    tenant = await createTestTenant();
    const cal = await ensurePersonalCalendar(tenant);

    const title = `Delete target ${Date.now().toString(36)}`;
    const seeded = await createCalendarEvent(tenant, cal.id, {
      title,
      startAt: todayAtUnix('09:00'),
      endAt: todayAtUnix('10:00'),
      kind: 'event',
    });

    await injectAuth(page.context(), tenant);
    await openCalendar(page);

    const pill = eventPill(page, title);
    await expect(pill).toBeVisible({ timeout: 15_000 });
    await pill.click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible({ timeout: 5_000 });

    // Pre-accept the native confirm() that the dialog fires before delete.
    page.once('dialog', (d) => {
      void d.accept();
    });

    await dialog.getByRole('button', { name: copy.en.deleteAction }).click();

    await expect(dialog).toBeHidden({ timeout: 10_000 });
    await expect(page.getByText(copy.en.toastEventDeleted)).toBeVisible({ timeout: 5_000 });
    await expect(eventPill(page, title)).toHaveCount(0, { timeout: 10_000 });

    // Cross-check the backend: the soft-deleted event must not come
    // back from the per-calendar list either.
    const today = new Date();
    const dayStart = new Date(today);
    dayStart.setHours(0, 0, 0, 0);
    const dayEnd = new Date(today);
    dayEnd.setHours(23, 59, 59, 999);
    const res = await fetch(
      `${TIME_API_URL}/workspaces/${tenant.workspaceId}/calendars/${cal.id}/events?start=${dayStart.toISOString()}&end=${dayEnd.toISOString()}`,
      {
        headers: {
          accept: 'application/json',
          authorization: `Bearer ${tenant.accessToken}`,
        },
      },
    );
    expect(res.ok).toBeTruthy();
    const body = (await res.json()) as { events: Array<{ id: string; title: string }> };
    expect(body.events.find((e) => e.id === seeded.id)).toBeUndefined();

    expect(errors).toEqual([]);
  });

  /* ─────────────────────────────────────────────────────────── */

  test('validation surfaces title-required and end-before-start errors', async ({ page }) => {
    const errors = attachConsoleErrorGuard(page);
    tenant = await createTestTenant();
    await ensurePersonalCalendar(tenant);

    await injectAuth(page.context(), tenant);
    await openCalendar(page);
    const dialog = await openCreateDialog(page);

    // Submit without a title — dialog stays open and surfaces the
    // title-required error.
    await dialog.getByRole('button', { name: copy.en.createSubmit }).click();
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText(copy.en.validationTitleRequired)).toBeVisible();

    // Fill the title, then push end earlier than start (09:00 start,
    // default end 10:00 → flip end to 09:00 so end == start, which
    // triggers the `e <= s` branch).
    await dialog.getByLabel(copy.en.titleField).fill('Will fail validation');

    const timeTriggers = dialog.getByRole('button', { name: /^\d{2}:\d{2}$/ });
    await timeTriggers.nth(1).click();
    // Pick an earlier slot than the start (09:00). '08:00' is clearly
    // < 09:00 default start.
    await page.getByRole('option', { name: '08:00' }).click();

    await dialog.getByRole('button', { name: copy.en.createSubmit }).click();
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText(copy.en.validationEndBeforeStart)).toBeVisible();

    expect(errors).toEqual([]);
  });

  /* ─────────────────────────────────────────────────────────── */

  test('ja locale renders the dialog and toast copy in Japanese', async ({ browser }) => {
    // Register the tenant with locale='ja'. The web app's auth bootstrap
    // (src/features/auth/use-auth-bootstrap.ts) calls
    // `setLanguage(user.locale)` after login, which writes `nf.lang` to
    // localStorage + calls `i18n.changeLanguage`. That overrides any
    // localStorage value Playwright could set via addInitScript, so
    // registration-time locale is the only stable knob.
    tenant = await createTestTenant({ locale: 'ja' });
    await ensurePersonalCalendar(tenant);

    const context = await browser.newContext({
      viewport: { width: 1280, height: 800 },
    });
    try {
      await injectAuth(context, tenant);

      const page = await context.newPage();
      const errors = attachConsoleErrorGuard(page);

      await page.goto('/calendar');
      // The calendar h1 is "カレンダー" in ja.
      await expect(page.getByRole('heading', { level: 1, name: 'カレンダー' })).toBeVisible({
        timeout: 15_000,
      });

      const cell = await findTodayCell(page);
      await cell.click({ position: { x: 8, y: 8 } });

      const dialog = page.getByRole('dialog');
      await expect(dialog).toBeVisible({ timeout: 5_000 });
      await expect(dialog.getByRole('heading', { name: copy.ja.createEventTitle })).toBeVisible();

      const title = `会議 ${Date.now().toString(36)}`;
      await dialog.getByLabel(copy.ja.titleField).fill(title);
      await dialog.getByRole('button', { name: copy.ja.createSubmit }).click();

      await expect(dialog).toBeHidden({ timeout: 10_000 });
      await expect(page.getByText(copy.ja.toastEventCreated)).toBeVisible({ timeout: 5_000 });

      expect(errors).toEqual([]);
    } finally {
      await context.close();
    }
  });
});
