/**
 * Task detail edit affordances E2E.
 *
 * Existing task-detail.spec.ts only checks that the detail page renders,
 * and task-complete.spec.ts only covers the Complete transition. This
 * spec closes the gap for the three in-place edits the sidebar and
 * heading expose:
 *
 *   1. Inline title edit (click "Edit title", type, click Save; reload
 *      and assert the new title landed).
 *   2. Due date set via the sidebar DatePicker popover (click trigger,
 *      pick a concrete day, reload, assert the locale-formatted value
 *      re-renders on the trigger).
 *   3. State transition via the sidebar "Transitions" card using a
 *      non-Complete verb ("Start" → In progress) so the coverage is
 *      distinct from task-complete.spec.ts.
 *
 * Each test creates its own fresh tenant via REST because title /
 * state edits would collide with other specs asserting the shared
 * seeded tasks (e.g. auth.spec.ts pins "Seeded smoke task").
 */

import { expect, type Page, test } from '@playwright/test';

import {
  API_BASE_URL,
  cleanupTenant,
  createTask,
  createTestTenant,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';

interface TaskResponse {
  id: string;
  title: string;
}

/**
 * Creates a task with a due date via REST. Mirrors the helper in
 * global-setup.ts which is not exported.
 */
async function createTaskWithDueDate(
  tenant: TestTenant,
  title: string,
  dueOn: string,
): Promise<TaskResponse> {
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
  return (await res.json()) as TaskResponse;
}

/** Navigate to the detail page and wait for the title heading to mount. */
async function openTaskDetail(page: Page, taskId: string, title: string): Promise<void> {
  await page.goto(`/tasks/${taskId}`);
  await expect(page.getByRole('heading', { level: 1, name: title })).toBeVisible({
    timeout: 10_000,
  });
}

test.describe('task detail edit affordances', () => {
  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('edits the title inline and persists the new value across reload', async ({ page }) => {
    tenant = await createTestTenant();
    const originalTitle = `Edit Title Original ${Date.now().toString(36)}`;
    const { id: taskId } = await createTask(tenant, originalTitle);

    await injectAuth(page.context(), tenant);
    await openTaskDetail(page, taskId, originalTitle);

    // Enter edit mode via the ghost "Edit title" button. The button
    // carries aria-label = tasks.detail.title_edit_named which is
    // "Edit title: {title}" (en) / "タイトルを編集: {title}" (ja),
    // so match that shape instead of the bare visible copy.
    const editTitleRe = /^(Edit title|タイトルを編集)(:\s*.+)?$/;
    await page.getByRole('button', { name: editTitleRe }).first().click();

    const newTitle = `Edit Title Updated ${Date.now().toString(36)}`;
    // The input uses aria-label = tasks.detail.title_edit (the bare
    // form, not the named one) per the TitleEditor source.
    const titleInput = page.getByRole('textbox', { name: /^(Edit title|タイトルを編集)$/ });
    await expect(titleInput).toBeVisible({ timeout: 5_000 });
    await titleInput.fill(newTitle);

    // Save via the explicit button (not Enter) so we exercise the
    // mutation path rather than implicit submit.
    await page.getByRole('button', { name: /^(Save|保存)$/ }).click();

    // The heading should flip to the new value once useUpdateTask
    // invalidates; the edit control re-collapses back to the ghost
    // button which is the simplest signal of save completion.
    await expect(page.getByRole('button', { name: editTitleRe }).first()).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByRole('heading', { level: 1, name: newTitle })).toBeVisible({
      timeout: 10_000,
    });

    // Reload and assert the persisted title is what the API replays.
    await page.reload();
    await expect(page.getByRole('heading', { level: 1, name: newTitle })).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByRole('heading', { level: 1, name: originalTitle })).toHaveCount(0);
  });

  test('sets a due date via the sidebar date picker and persists it across reload', async ({
    page,
  }) => {
    tenant = await createTestTenant();
    const title = `Edit Due ${Date.now().toString(36)}`;
    const { id: taskId } = await createTask(tenant, title);

    await injectAuth(page.context(), tenant);
    await openTaskDetail(page, taskId, title);

    // Pick a specific, unambiguous day that will render identically
    // in any locale after formatDueDate(): a day in the middle of the
    // month far from month boundaries so locale month-name variation
    // is the only difference. We assert via the exact digit "15" on
    // the trigger which both en ("Apr 15, 2026") and ja ("2026/04/15")
    // share.
    const targetYear = 2026;
    const targetMonth = 6; // June — also avoids any DST surprises
    const targetDay = 15;
    const targetIso = `${targetYear}-${String(targetMonth).padStart(2, '0')}-${String(
      targetDay,
    ).padStart(2, '0')}`;

    // The sidebar has two DatePickers (start, due). Scope to the
    // sidebar aside and locate by its surrounding "Due date" / "期日"
    // label's sibling trigger. The picker trigger renders the
    // placeholder "No date" / "日付を選択" when unset — use the
    // FormField label + nearest picker approach by filtering buttons
    // inside the <aside> to the one showing the placeholder copy.
    const sidebar = page.locator('aside');

    // Find the due-date picker's trigger. The placeholder used when
    // no date is set comes from common.date.placeholder which is
    // shared between start and due triggers — so we disambiguate by
    // clicking the trigger that follows the "Due date" / "期日" label.
    const dueLabel = sidebar.getByText(/^(Due date|期日)$/);
    await expect(dueLabel).toBeVisible({ timeout: 5_000 });
    // The DatePicker trigger is the button rendered inside the
    // FormField under that label; it's the first button after the
    // label within the same FormField container.
    const dueTrigger = dueLabel.locator('..').getByRole('button').first();
    await dueTrigger.click();

    // The popover opens at the current month. Step the month header
    // forward with the Next button until we land on the target
    // month. The Next button has aria-label = calendar.next
    // ("Next month" / "次の月"). Match the full label so we don't
    // collide with any other "Next" button elsewhere on the page
    // (e.g. the ai.next "次へ" etc.).
    const nextBtn = page.getByRole('button', { name: /^(Next month|次の月)$/ });
    await expect(nextBtn).toBeVisible({ timeout: 5_000 });

    // Target month header copy, per common.date.monthYear:
    //   en: ICU plural -> "June 2026"
    //   ja: literal    -> "2026年6月"
    const monthNamesEn = [
      'January',
      'February',
      'March',
      'April',
      'May',
      'June',
      'July',
      'August',
      'September',
      'October',
      'November',
      'December',
    ];
    const targetMonthHeaderEn = `${monthNamesEn[targetMonth - 1]} ${targetYear}`;
    const targetMonthHeaderJa = `${targetYear}年${targetMonth}月`;
    const targetHeaderRe = new RegExp(`^(${targetMonthHeaderEn}|${targetMonthHeaderJa})$`);

    // Safety cap: walk at most 24 months forward. If the popover's
    // current month is already at/past the target on mount the loop
    // exits immediately.
    for (let i = 0; i < 24; i++) {
      const header = page.getByText(targetHeaderRe).first();
      if (await header.isVisible().catch(() => false)) break;
      await nextBtn.click();
    }
    // Explicitly assert the target month header landed so a
    // loop-exit on iteration 24 fails loudly instead of silently
    // clicking the wrong day.
    await expect(page.getByText(targetHeaderRe).first()).toBeVisible({ timeout: 5_000 });

    // Click the day button (the calendar grid renders each day as a
    // plain <button> with its day-of-month as text). The grid is
    // inside the popover; use an exact regex so "15" does not match
    // "150" etc., and .first() to pin the in-popover match over any
    // theoretical collision with other "15"-labelled buttons.
    await page
      .getByRole('button', { name: new RegExp(`^${targetDay}$`) })
      .first()
      .click();

    // After selection the popover closes and the mutation fires. The
    // trigger label re-renders with the formatted date. Assert a
    // locale-agnostic fragment ("15" + targetYear) so the test runs
    // green in both en and ja.
    await expect(dueTrigger).toContainText(String(targetDay), { timeout: 10_000 });
    await expect(dueTrigger).toContainText(String(targetYear));

    // Reload and assert the API-persisted value is what the trigger
    // shows. The server returns dueOn = targetIso and the picker
    // re-renders that via formatDueDate().
    await page.reload();
    await expect(page.getByRole('heading', { level: 1, name: title })).toBeVisible({
      timeout: 10_000,
    });

    // Cross-check the server state so the test fails loudly if the
    // UI shows the new date but the API never received it (would
    // surface a silent mutation error otherwise).
    const getRes = await fetch(`${API_BASE_URL}/tasks/${taskId}`, {
      headers: {
        accept: 'application/json',
        authorization: `Bearer ${tenant.accessToken}`,
      },
    });
    expect(getRes.ok).toBeTruthy();
    const fetched = (await getRes.json()) as { dueOn?: string | null };
    expect(fetched.dueOn).toBe(targetIso);

    // And the trigger still shows the date after the reload.
    const sidebarAfter = page.locator('aside');
    const dueLabelAfter = sidebarAfter.getByText(/^(Due date|期日)$/);
    const dueTriggerAfter = dueLabelAfter.locator('..').getByRole('button').first();
    await expect(dueTriggerAfter).toContainText(String(targetDay), { timeout: 10_000 });
    await expect(dueTriggerAfter).toContainText(String(targetYear));
  });

  test('transitions state to In progress via the sidebar Start button', async ({ page }) => {
    tenant = await createTestTenant();
    // Seed with a due date so the task matches real-world shape; not
    // required for the state transition itself.
    const title = `Edit State ${Date.now().toString(36)}`;
    const { id: taskId } = await createTaskWithDueDate(tenant, title, '2026-06-15');

    await injectAuth(page.context(), tenant);
    await openTaskDetail(page, taskId, title);

    const sidebar = page.locator('aside');

    // Scope the state-badge assertion to the row that contains the
    // "State" / "状態" label. The wider <aside> also renders a
    // StateGraph which shows every state name as an SVG label, so a
    // global `aside.getByText(/Open/)` search would match the graph
    // node even after the badge has flipped. The state badge row is
    // the direct parent of the "State" label.
    const stateLabel = sidebar.getByText(/^(State|状態)$/);
    await expect(stateLabel).toBeVisible({ timeout: 10_000 });
    const stateRow = stateLabel.locator('..');

    // Initial state badge reads "Open" / "オープン".
    await expect(stateRow.getByText(/^(Open|オープン)$/)).toBeVisible({ timeout: 10_000 });

    // Click the "Start" transition inside the Transitions card. This
    // is a distinct verb from the "Complete" button covered by
    // task-complete.spec.ts and moves open → waiting.
    const startBtn = sidebar.getByRole('button', { name: /^(Start|開始)$/ });
    await expect(startBtn).toBeVisible({ timeout: 10_000 });
    await startBtn.click();

    // After the transition POST succeeds, "Start" is no longer legal
    // (TRANSITIONS_BY_STATE.waiting = [submit, block, cancel]) and
    // the badge should flip to "In progress" / "進行中".
    await expect(startBtn).toHaveCount(0, { timeout: 10_000 });
    await expect(stateRow.getByText(/^(In progress|進行中)$/)).toBeVisible({
      timeout: 10_000,
    });
    await expect(stateRow.getByText(/^(Open|オープン)$/)).toHaveCount(0);

    // The new legal transitions should be visible (Submit / Block /
    // Cancel) — assert one to prove the sidebar rehydrated, rather
    // than relying solely on the badge text.
    await expect(sidebar.getByRole('button', { name: /^(Submit|レビュー依頼)$/ })).toBeVisible({
      timeout: 5_000,
    });
  });
});
