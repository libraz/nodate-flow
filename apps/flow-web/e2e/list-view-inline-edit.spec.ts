/**
 * Task list-view inline edit E2E.
 *
 * Source under test: apps/flow-web/src/features/tasks/task-list-view.tsx.
 *
 * The list view exposes three inline-editable cells per row, each driven
 * by useInlineEdit() + useUpdateTask() and persisted via PATCH /tasks/{id}:
 *
 *   1. InlineTitleCell — single click navigates to /tasks/{publicId}
 *      via TanStack <Link>; double-click within ~250ms enters edit mode
 *      (an <input aria-label="Edit title">). Enter / blur commits with a
 *      trimmed-non-empty / changed guard; Escape cancels.
 *   2. InlinePriorityCell — click on a role="button" span opens an
 *      inline <select aria-label="Change priority">; selecting a new
 *      option commits; Escape closes; blur stops editing.
 *   3. InlineDueCell — click on a role="button" span opens the shared
 *      DatePicker popover (FloatingPortal at document.body); picking a
 *      day commits dueOn.
 *
 * Each test seeds a fresh task via REST with a unique title so the rows
 * can run in parallel inside the same shared tenant without colliding
 * with other specs that pin "Seeded *" titles.
 *
 * The error path test (test 8) is the only one that uses route mocking
 * — it returns 500 from PATCH /tasks/{id} so we can assert the visible
 * title reverts. All other tests hit the real flow-api / auth-api.
 */

import { expect, type Locator, type Page, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { API_BASE_URL, createTask, injectAuth, type TestTenant } from './fixtures/tenant';

interface TaskFetchResponse {
  id: string;
  title: string;
  priority: number;
  dueOn: string | null;
}

/**
 * Force the list view via localStorage before the route mounts. The
 * task view switcher reads `nf:task-view`; default is 'board' which
 * would land on TaskBoardView and miss every inline edit cell.
 */
async function forceListView(page: Page): Promise<void> {
  await page.addInitScript(() => {
    try {
      window.localStorage.setItem('nf:task-view', 'list');
    } catch {
      // ignore — private mode etc.
    }
  });
}

/** GET /tasks/{id} bearer-auth and return the persisted record. */
async function fetchTask(tenant: TestTenant, id: string): Promise<TaskFetchResponse> {
  const res = await fetch(`${API_BASE_URL}/tasks/${id}`, {
    headers: {
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
  });
  if (!res.ok) throw new Error(`GET /tasks/${id} -> ${res.status} ${await res.text()}`);
  return (await res.json()) as TaskFetchResponse;
}

/**
 * Locate a row by the task title link inside it. Used to bootstrap onto
 * a specific row before mutations re-render its cells. Once edit mode
 * is active the title link is replaced with an <input>, so any locator
 * that scopes by link presence stops matching — see {@link rowByIndex}
 * for a stable handle once edit mode is engaged.
 */
function rowByTitleLink(page: Page, title: string): Locator {
  return page
    .getByRole('row')
    .filter({ has: page.getByRole('link', { name: title, exact: true }) });
}

/**
 * Locate a specific data row by its 1-based aria-rowindex. The header
 * row(s) consume the first one or two indices; the first data row's
 * aria-rowindex equals (headerRows + 1). We capture the value after
 * the title-link-based locator finds the row, so this stays valid even
 * when an inline edit removes the link.
 */
function rowByIndex(page: Page, ariaRowIndex: number): Locator {
  return page.locator(`[role="row"][aria-rowindex="${ariaRowIndex}"]`);
}

/** Read aria-rowindex (1-based) off a row locator. */
async function readRowIndex(row: Locator): Promise<number> {
  const raw = await row.getAttribute('aria-rowindex');
  const idx = Number(raw);
  if (!Number.isFinite(idx) || idx <= 0) {
    throw new Error(`row had no usable aria-rowindex (got ${raw ?? 'null'})`);
  }
  return idx;
}

/**
 * Compute YYYY-MM-DD for `daysFromToday` days from now in the browser's
 * local timezone — DatePicker derives day buttons from local Date math
 * so a UTC offset would land on the wrong cell near midnight.
 */
function isoDateOffsetFromToday(daysFromToday: number): {
  iso: string;
  year: number;
  month: number;
  day: number;
} {
  const d = new Date();
  d.setDate(d.getDate() + daysFromToday);
  const year = d.getFullYear();
  const month = d.getMonth() + 1;
  const day = d.getDate();
  const iso = `${year}-${String(month).padStart(2, '0')}-${String(day).padStart(2, '0')}`;
  return { iso, year, month, day };
}

test.describe('task list view inline edit', () => {
  let tenant: TestTenant;
  // Tasks seeded by individual tests, cleaned up in afterEach so failures
  // never leave the shared tenant in a state that breaks the next run.
  let createdTaskIds: string[] = [];

  test.beforeEach(() => {
    tenant = loadTenants().user;
    createdTaskIds = [];
  });

  test.afterEach(async () => {
    await Promise.all(
      createdTaskIds.map((id) =>
        fetch(`${API_BASE_URL}/tasks/${id}`, {
          method: 'DELETE',
          headers: { authorization: `Bearer ${tenant.accessToken}` },
        }).catch(() => undefined),
      ),
    );
  });

  /** Helper: seed one task and remember its id for cleanup. */
  async function seed(
    title: string,
    options: { priority?: number; dueOn?: string } = {},
  ): Promise<{ id: string; title: string }> {
    const created = await createTask(tenant, title, options);
    createdTaskIds.push(created.id);
    return created;
  }

  test('title double-click enters edit, Enter commits, persists across reload', async ({
    page,
  }) => {
    const stamp = Date.now().toString(36);
    const original = `Inline Title Commit ${stamp}`;
    const updated = `Inline Title Updated ${stamp}`;
    const { id: taskId } = await seed(original);

    await forceListView(page);
    await injectAuth(page.context(), tenant);
    await page.goto(`/projects/${tenant.projectId}/tasks`);

    const initialRow = rowByTitleLink(page, original);
    await expect(initialRow).toBeVisible({ timeout: 15_000 });
    const rowIdx = await readRowIndex(initialRow);
    const stableRow = rowByIndex(page, rowIdx);

    // Double-click swaps the <Link> for <input aria-label="Edit title">.
    // We dblclick the title link directly; the InlineTitleCell handler
    // clears the pending navigation timer and switches to edit mode.
    const titleLink = initialRow.getByRole('link', { name: original, exact: true });
    await titleLink.dblclick();
    const input = stableRow.getByRole('textbox', { name: 'Edit title' });
    await expect(input).toBeVisible({ timeout: 5_000 });

    // Wait for the PATCH /tasks/{id} response so we don't reload before
    // the server has actually written the change.
    const patchResponse = page.waitForResponse(
      (res) =>
        res.url().includes(`/tasks/${taskId}`) &&
        res.request().method() === 'PATCH' &&
        res.status() === 200,
    );
    await input.fill(updated);
    await input.press('Enter');
    const patch = await patchResponse;
    const patchBody = patch.request().postDataJSON() as { title?: string };
    expect(patchBody.title).toBe(updated);

    // UI should reflect the new title (link gets re-rendered with updated text).
    await expect(rowByTitleLink(page, updated)).toBeVisible({ timeout: 5_000 });

    await page.reload();
    await expect(rowByTitleLink(page, updated)).toBeVisible({ timeout: 15_000 });

    const fetched = await fetchTask(tenant, taskId);
    expect(fetched.title).toBe(updated);
  });

  test('title Esc cancels without commit', async ({ page }) => {
    const stamp = Date.now().toString(36);
    const original = `Inline Title Cancel ${stamp}`;
    const { id: taskId } = await seed(original);

    await forceListView(page);
    await injectAuth(page.context(), tenant);
    await page.goto(`/projects/${tenant.projectId}/tasks`);

    const initialRow = rowByTitleLink(page, original);
    await expect(initialRow).toBeVisible({ timeout: 15_000 });
    const rowIdx = await readRowIndex(initialRow);
    const stableRow = rowByIndex(page, rowIdx);

    // Listen for any PATCH on this task — there must be none for the
    // duration of this interaction.
    const sawPatch: string[] = [];
    page.on('request', (req) => {
      if (req.method() === 'PATCH' && req.url().includes(`/tasks/${taskId}`)) {
        sawPatch.push(req.url());
      }
    });

    const titleLink = initialRow.getByRole('link', { name: original, exact: true });
    await titleLink.dblclick();
    const input = stableRow.getByRole('textbox', { name: 'Edit title' });
    await expect(input).toBeVisible({ timeout: 5_000 });
    await input.fill('TYPED BUT CANCELLED');
    await input.press('Escape');

    // Edit mode must close back to the link form without firing PATCH.
    await expect(rowByTitleLink(page, original)).toBeVisible({ timeout: 5_000 });
    await expect(input).toHaveCount(0);

    // Give the network a moment to fire any stray PATCH that shouldn't exist.
    await page.waitForTimeout(300);
    expect(sawPatch, 'Esc must not commit').toEqual([]);

    const fetched = await fetchTask(tenant, taskId);
    expect(fetched.title).toBe(original);
  });

  test('title single click does NOT enter edit (it navigates)', async ({ page }) => {
    const stamp = Date.now().toString(36);
    const original = `Inline Title Navigate ${stamp}`;
    const { id: taskId } = await seed(original);

    await forceListView(page);
    await injectAuth(page.context(), tenant);
    await page.goto(`/projects/${tenant.projectId}/tasks`);

    const initialRow = rowByTitleLink(page, original);
    await expect(initialRow).toBeVisible({ timeout: 15_000 });
    const titleLink = initialRow.getByRole('link', { name: original, exact: true });

    // Single click — the InlineTitleCell schedules navigation through a
    // 250 ms timer (handled via useNavigate). We wait for the URL to
    // change to /tasks/{publicId}.
    await titleLink.click();
    await page.waitForURL(`**/tasks/${taskId}`, { timeout: 5_000 });
    expect(page.url()).toContain(`/tasks/${taskId}`);

    // The detail page heading should mount with the same title.
    await expect(page.getByRole('heading', { level: 1, name: original })).toBeVisible({
      timeout: 10_000,
    });
  });

  test('priority click → select non-default → commits', async ({ page }) => {
    const stamp = Date.now().toString(36);
    const taskTitle = `Inline Priority Commit ${stamp}`;
    // Seed at priority 0 (none) so we can move to 2 (medium) and observe a delta.
    const { id: taskId } = await seed(taskTitle, { priority: 0 });

    await forceListView(page);
    await injectAuth(page.context(), tenant);
    await page.goto(`/projects/${tenant.projectId}/tasks`);

    const initialRow = rowByTitleLink(page, taskTitle);
    await expect(initialRow).toBeVisible({ timeout: 15_000 });
    const rowIdx = await readRowIndex(initialRow);
    const stableRow = rowByIndex(page, rowIdx);

    // The trigger is a role="button" span that carries the priority
    // label ("None") in its aria-label suffixed by " — Change priority".
    // Match by the suffix to be locale-agnostic on the prefix.
    const trigger = initialRow.getByRole('button', { name: /Change priority/ });
    await expect(trigger).toBeVisible({ timeout: 5_000 });
    await trigger.click();

    const select = stableRow.getByRole('combobox', { name: 'Change priority' });
    await expect(select).toBeVisible({ timeout: 5_000 });

    const patchResponse = page.waitForResponse(
      (res) =>
        res.url().includes(`/tasks/${taskId}`) &&
        res.request().method() === 'PATCH' &&
        res.status() === 200,
    );
    // 2 = Medium per the source ordering.
    await select.selectOption('2');
    const patch = await patchResponse;
    const body = patch.request().postDataJSON() as { priority?: number };
    expect(body.priority).toBe(2);

    const fetched = await fetchTask(tenant, taskId);
    expect(fetched.priority).toBe(2);
  });

  test('priority Esc closes without commit', async ({ page }) => {
    const stamp = Date.now().toString(36);
    const taskTitle = `Inline Priority Cancel ${stamp}`;
    const { id: taskId } = await seed(taskTitle, { priority: 1 });

    await forceListView(page);
    await injectAuth(page.context(), tenant);
    await page.goto(`/projects/${tenant.projectId}/tasks`);

    const initialRow = rowByTitleLink(page, taskTitle);
    await expect(initialRow).toBeVisible({ timeout: 15_000 });
    const rowIdx = await readRowIndex(initialRow);
    const stableRow = rowByIndex(page, rowIdx);

    const sawPatch: string[] = [];
    page.on('request', (req) => {
      if (req.method() === 'PATCH' && req.url().includes(`/tasks/${taskId}`)) {
        sawPatch.push(req.url());
      }
    });

    const trigger = initialRow.getByRole('button', { name: /Change priority/ });
    await trigger.click();

    const select = stableRow.getByRole('combobox', { name: 'Change priority' });
    await expect(select).toBeVisible({ timeout: 5_000 });
    await select.press('Escape');

    // After Escape the select must be torn down and replaced with the trigger again.
    await expect(select).toHaveCount(0, { timeout: 5_000 });
    await expect(stableRow.getByRole('button', { name: /Change priority/ })).toBeVisible();

    await page.waitForTimeout(300);
    expect(sawPatch, 'Esc must not commit priority').toEqual([]);

    const fetched = await fetchTask(tenant, taskId);
    expect(fetched.priority).toBe(1);
  });

  test('due click → pick date → commits', async ({ page }) => {
    const stamp = Date.now().toString(36);
    const taskTitle = `Inline Due Commit ${stamp}`;
    const { id: taskId } = await seed(taskTitle);

    await forceListView(page);
    await injectAuth(page.context(), tenant);
    await page.goto(`/projects/${tenant.projectId}/tasks`);

    const initialRow = rowByTitleLink(page, taskTitle);
    await expect(initialRow).toBeVisible({ timeout: 15_000 });
    const rowIdx = await readRowIndex(initialRow);
    const stableRow = rowByIndex(page, rowIdx);

    // Initial due aria-label is "Set due date" (from tasks.inline.edit_due_unset).
    const dueTrigger = initialRow.getByRole('button', { name: 'Set due date' });
    await expect(dueTrigger).toBeVisible({ timeout: 5_000 });
    await dueTrigger.click();

    // After click, the cell renders the DatePicker primitive whose
    // trigger is a <button> displaying `triggerLabel`. The handler
    // passes `triggerLabel = dueOn ? formatDate(dueOn, locale) : t('common.date.placeholder')`,
    // which resolves to "Select a date" / "日付を選択" / "请选择日期"
    // (locale-dependent). We grab it by DOM position because exact
    // text would couple the spec to one locale.
    const popoverTrigger = stableRow.locator('button').last();
    await expect(popoverTrigger).toBeVisible({ timeout: 5_000 });
    await popoverTrigger.click();

    // The DatePicker grid lives in a Popover (FloatingPortal at body root).
    // The DataGrid that hosts the task rows ALSO has role="grid"
    // (aria-label="Tasks"), so we scope by negation: pick the role="grid"
    // that is NOT the named one. Pick a day 7 days out and walk forward
    // up to 2 months to handle month-end rollover.
    const target = isoDateOffsetFromToday(7);
    const dayGrid = page.locator('[role="grid"]:not([aria-label])');
    await expect(dayGrid).toBeVisible({ timeout: 5_000 });

    let attempts = 0;
    while (attempts < 3) {
      // The DatePicker header is the span sibling that precedes the
      // day grid in the popover panel — it carries the formatted
      // "<MonthName> <Year>" text. Anchor on the year (locale-agnostic).
      const headerText = await dayGrid
        .locator('xpath=../*[contains(@class, "_header") or contains(@class, "header")]//span')
        .first()
        .innerText()
        .catch(() => '');
      if (headerText.includes(String(target.year))) {
        break;
      }
      await page.getByRole('button', { name: 'Next month' }).click();
      attempts += 1;
    }

    const patchResponse = page.waitForResponse(
      (res) =>
        res.url().includes(`/tasks/${taskId}`) &&
        res.request().method() === 'PATCH' &&
        res.status() === 200,
    );
    // Day buttons have no aria-label, only the day number as text.
    // Filter the grid's enabled buttons by exact text to disambiguate
    // ("7" vs "17", "27").
    await dayGrid.getByRole('button', { name: String(target.day), exact: true }).click();
    const patch = await patchResponse;
    const body = patch.request().postDataJSON() as { dueOn?: string };
    expect(body.dueOn).toBe(target.iso);

    const fetched = await fetchTask(tenant, taskId);
    expect(fetched.dueOn).toBe(target.iso);
  });

  test('due clear sets null', async ({ page }) => {
    const stamp = Date.now().toString(36);
    const taskTitle = `Inline Due Clear ${stamp}`;
    // Seed with a due date so the "clear" affordance has something to remove.
    const target = isoDateOffsetFromToday(7);
    const { id: taskId } = await seed(taskTitle, { dueOn: target.iso });

    await forceListView(page);
    await injectAuth(page.context(), tenant);
    await page.goto(`/projects/${tenant.projectId}/tasks`);

    const initialRow = rowByTitleLink(page, taskTitle);
    await expect(initialRow).toBeVisible({ timeout: 15_000 });
    const rowIdx = await readRowIndex(initialRow);
    const stableRow = rowByIndex(page, rowIdx);

    // The trigger for a task WITH a due date carries the dated
    // aria-label "<formatted>, change due date" (edit_due_set).
    // Click it to enter edit mode.
    const dueTrigger = initialRow.getByRole('button', { name: /change due date/ });
    await dueTrigger.click();

    // Open the popover (the DatePicker trigger is the last button in
    // the row once edit mode is active).
    const popoverTrigger = stableRow.locator('button').last();
    await popoverTrigger.click();

    // The DatePicker now renders a footer "Clear" button when the host
    // passes `onClear`. Match the locale label set in shared common.json
    // (en: "Clear" / ja: "クリア" / zh: "清除").
    const clearBtn = page.getByRole('button', { name: /^(Clear|クリア|清除)$/ });
    await expect(clearBtn).toBeVisible({ timeout: 5_000 });

    const patchResponse = page.waitForResponse(
      (res) =>
        res.url().includes(`/tasks/${taskId}`) &&
        res.request().method() === 'PATCH' &&
        res.status() === 200,
    );
    await clearBtn.click();
    const patch = await patchResponse;
    // The wire format is `dueOn: ""` — the backend's `*string` handler
    // treats an empty string as "clear" (NULL in DB) while a JSON null
    // would be unmarshalled to nil and skipped.
    const body = patch.request().postDataJSON() as { dueOn?: string | null };
    expect(body.dueOn).toBe('');

    // The Task DTO uses `omitempty` so a cleared dueOn is absent from the
    // response body; treat undefined as "cleared" for the assertion.
    const fetched = await fetchTask(tenant, taskId);
    expect(fetched.dueOn ?? null).toBeNull();
  });

  test('error path: PATCH 500 on title commit reverts visible title', async ({ page }) => {
    const stamp = Date.now().toString(36);
    const original = `Inline Title 500 ${stamp}`;
    const { id: taskId } = await seed(original);

    await forceListView(page);
    await injectAuth(page.context(), tenant);

    // Force PATCH /tasks/{id} to 500. The CORS shim from injectAuth
    // uses route.fetch(); register a more specific handler that wins
    // for this exact endpoint + method.
    await page.route(
      (url) => url.href.includes(`/tasks/${taskId}`) && !url.href.includes('/comments'),
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
            detail: 'forced for E2E revert test',
          }),
        });
      },
    );

    await page.goto(`/projects/${tenant.projectId}/tasks`);

    const initialRow = rowByTitleLink(page, original);
    await expect(initialRow).toBeVisible({ timeout: 15_000 });
    const rowIdx = await readRowIndex(initialRow);
    const stableRow = rowByIndex(page, rowIdx);

    const titleLink = initialRow.getByRole('link', { name: original, exact: true });
    await titleLink.dblclick();
    const input = stableRow.getByRole('textbox', { name: 'Edit title' });
    await expect(input).toBeVisible({ timeout: 5_000 });

    const settle = page.waitForResponse(
      (res) =>
        res.url().includes(`/tasks/${taskId}`) &&
        res.request().method() === 'PATCH' &&
        res.status() === 500,
    );
    await input.fill('Should Be Reverted');
    await input.press('Enter');
    const patch = await settle;
    expect(patch.status()).toBe(500);

    // After the failed mutation, the visible row title should remain
    // the original — the list view does not optimistically apply the
    // edit (handleInlineSave fires updateTask.mutateAsync without
    // optimistic onMutate). The expectation here documents the desired
    // behaviour: original title visible, no row carrying "Should Be
    // Reverted".
    await expect(rowByTitleLink(page, original)).toBeVisible({ timeout: 5_000 });
    await expect(page.getByRole('link', { name: 'Should Be Reverted', exact: true })).toHaveCount(
      0,
    );

    // Server state remains the original.
    const fetched = await fetchTask(tenant, taskId);
    expect(fetched.title).toBe(original);
  });
});
