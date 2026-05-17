/**
 * Task list-view drag-and-drop reorder E2E.
 *
 * Source under test: apps/flow-web/src/features/tasks/task-list-view.tsx.
 * The leftmost grid column renders a `<span draggable>` handle with
 * aria-label "Drag to reorder" (key: tasks.reorder.drag_handle). On drop
 * the view computes a sequential `sortWeight = (i + 1) * 1000` for the
 * full visible list and POSTs `/tasks/reorder` with `{ projectId, items }`.
 *
 * The mutation in `useReorderTasks` (apps/flow-web/src/features/tasks/api.ts)
 * is documented as optimistic-with-rollback, so the spec asserts:
 *
 *   1. drag persists across reload (server side actually reordered),
 *   2. exactly one POST /tasks/reorder fires per drop with N items,
 *   3. on a 500 response the optimistic move is rolled back so the UI
 *      shows the pre-drag order.
 *
 * HTML5 native dnd is dispatched via DragEvent + DataTransfer in the
 * page (Playwright's mouse-based dragTo does not trigger dragstart for
 * native HTML5 dnd reliably).
 */

import type { Page } from '@playwright/test';
import { expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { API_BASE_URL, type TestTenant, createTask, injectAuth } from './fixtures/tenant';

interface SeededTaskRow {
  id: string;
  title: string;
}

/**
 * Force the list view via localStorage before the route mounts.
 * useTaskView reads `nf:task-view`; default is 'board' which would
 * land on TaskBoardView instead of TaskListView.
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

/**
 * Seed N tasks via REST. Each task is created sequentially so the
 * server-assigned `sortWeight` is monotonically increasing, giving us
 * a deterministic baseline order (returned in createdAt order).
 */
async function seedTasks(tenant: TestTenant, titles: string[]): Promise<SeededTaskRow[]> {
  const out: SeededTaskRow[] = [];
  for (const title of titles) {
    const t = await createTask(tenant, title);
    out.push({ id: t.id, title: t.title });
  }
  return out;
}

/**
 * GET /tasks for the project filtered to the seeded titles, returned
 * in the same order the API delivered them. The list-view consumes the
 * same default sort, so this is the source of truth for "current order".
 */
async function fetchProjectOrder(tenant: TestTenant, expectedTitles: string[]): Promise<string[]> {
  const res = await fetch(`${API_BASE_URL}/tasks?projectId=${tenant.projectId}&limit=100`, {
    headers: {
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
  });
  if (!res.ok) throw new Error(`GET /tasks -> ${res.status} ${await res.text()}`);
  const body = (await res.json()) as { tasks?: Array<{ id: string; title: string }> };
  const set = new Set(expectedTitles);
  return (body.tasks ?? []).map((t) => t.title).filter((title) => set.has(title));
}

/**
 * Locate the drag handle for a row by task title. The list view nests
 * the title link inside the same row as the handle; we walk up to the
 * `[role="row"]` ancestor and then back down to the `[draggable="true"]`
 * span, which is the only draggable element in the row.
 */
function dragHandleForTitle(page: Page, title: string) {
  return page
    .getByRole('row')
    .filter({ has: page.getByRole('link', { name: title, exact: true }) })
    .locator('span[draggable="true"]');
}

/**
 * Dispatch a synthetic HTML5 dnd sequence directly on two DOM elements.
 * Playwright's helper drags via real mouse events which do not fire
 * `dragstart` for native draggables in headless Chromium.
 *
 * The handles in TaskListView attach onDragStart to the <span>
 * directly, so the source must be the <span draggable> itself.
 * The drop target's row also wires onDragOver/onDrop on the same
 * leftmost <span draggable> (each row's handle accepts drops onto its
 * own row), so we re-use the same selector for the target.
 */
async function nativeDnd(
  page: Page,
  fromHandle: ReturnType<Page['locator']>,
  toHandle: ReturnType<Page['locator']>,
): Promise<void> {
  await fromHandle.waitFor({ state: 'visible' });
  await toHandle.waitFor({ state: 'visible' });

  // Tag both elements with a temporary id so the page.evaluate body can
  // re-find them. Passing JSHandles to evaluate works in the abstract,
  // but tagging keeps the inner function self-contained and survives
  // any React re-render that swaps the DOM node between the elementHandle
  // grab and the dispatch.
  const fromTag = `e2e-dnd-from-${Math.random().toString(36).slice(2)}`;
  const toTag = `e2e-dnd-to-${Math.random().toString(36).slice(2)}`;
  await fromHandle.evaluate((el, tag) => {
    el.setAttribute('data-e2e-dnd', tag);
  }, fromTag);
  await toHandle.evaluate((el, tag) => {
    el.setAttribute('data-e2e-dnd', tag);
  }, toTag);

  await page.evaluate(
    ({ fromSel, toSel }) => {
      const from = document.querySelector(`[data-e2e-dnd="${fromSel}"]`) as HTMLElement | null;
      const to = document.querySelector(`[data-e2e-dnd="${toSel}"]`) as HTMLElement | null;
      if (!from || !to) {
        throw new Error('synthetic dnd: source or target element not found');
      }
      const dt = new DataTransfer();
      const fire = (target: HTMLElement, type: string) => {
        const evt = new DragEvent(type, {
          bubbles: true,
          cancelable: true,
          composed: true,
          dataTransfer: dt,
        });
        target.dispatchEvent(evt);
      };
      fire(from, 'dragstart');
      fire(to, 'dragenter');
      fire(to, 'dragover');
      fire(to, 'drop');
      fire(from, 'dragend');
    },
    { fromSel: fromTag, toSel: toTag },
  );
}

/** Read the visible row order by scanning each row's title link text. */
async function visibleOrder(page: Page, expectedTitles: string[]): Promise<string[]> {
  const links = page.getByRole('row').locator('a');
  const all = await links.allInnerTexts();
  const set = new Set(expectedTitles);
  return all.map((s) => s.trim()).filter((t) => set.has(t));
}

// Run the three tests in this file serially (still parallel against
// other spec files) because they all reorder tasks in the *same* shared
// project (`loadTenants().user.projectId`). The reorder handler
// renumbers every visible task on every drop, so two concurrent reorders
// against the same project race: test A captures `before`, test B then
// drops + renumbers, then test A's POST sees a different baseline and
// the post-drop expectation no longer matches the server. Each test's
// own data is uniquely-titled, but the projection (`sortWeight` order)
// is global per-project. Serializing this file is the cheapest fix that
// preserves the rest of the suite's parallelism.
test.describe.configure({ mode: 'serial' });

test.describe('task list view reorder (drag and drop)', () => {
  // Each test seeds its own uniquely-titled tasks via beforeEach. The
  // sortWeight order is shared per-project, which is why this file runs
  // in serial mode (see configure() above).

  let tenant: TestTenant;
  let seeded: SeededTaskRow[];
  let titles: string[];

  test.beforeEach(async () => {
    const t = loadTenants().user;
    tenant = t;
    const stamp = Date.now().toString(36);
    titles = [`Reorder A ${stamp}`, `Reorder B ${stamp}`, `Reorder C ${stamp}`];
    seeded = await seedTasks(tenant, titles);
  });

  test.afterEach(async () => {
    // Best-effort cleanup so the shared tenant doesn't accumulate noise
    // between specs. Failures are ignored.
    await Promise.all(
      seeded.map((task) =>
        fetch(`${API_BASE_URL}/tasks/${task.id}`, {
          method: 'DELETE',
          headers: { authorization: `Bearer ${tenant.accessToken}` },
        }).catch(() => undefined),
      ),
    );
  });

  test('reorder persists across reload', async ({ page }) => {
    await forceListView(page);
    await injectAuth(page.context(), tenant);
    await page.goto(`/projects/${tenant.projectId}/tasks`);

    // Wait for all three rows to render before dragging.
    for (const title of titles) {
      await expect(page.getByRole('link', { name: title, exact: true })).toBeVisible({
        timeout: 15_000,
      });
    }

    // The list defaults to (sort_weight ASC, priority DESC, due_on ASC,
    // created_at DESC). Fresh tasks tie on every key except created_at,
    // which renders newest-first. We capture the actual observed order
    // and compute the post-drop expectation from it rather than
    // hard-coding an arbitrary direction.
    const before = await visibleOrder(page, titles);
    expect(before, 'observed initial order should contain exactly the seeded titles').toEqual(
      expect.arrayContaining(titles),
    );
    expect(before).toHaveLength(titles.length);

    // Drag the last visible row above the first.
    const expected = [before[before.length - 1] as string, ...before.slice(0, before.length - 1)];
    const reorderResponse = page.waitForResponse(
      (res) => res.url().endsWith('/tasks/reorder') && res.request().method() === 'POST',
    );
    await nativeDnd(
      page,
      dragHandleForTitle(page, before[before.length - 1] as string),
      dragHandleForTitle(page, before[0] as string),
    );
    const res = await reorderResponse;
    expect(res.status(), 'reorder POST should succeed').toBe(200);

    // Server-side order is the authoritative state.
    await expect
      .poll(() => fetchProjectOrder(tenant, titles), { timeout: 5_000 })
      .toEqual(expected);

    await page.reload();
    for (const title of titles) {
      await expect(page.getByRole('link', { name: title, exact: true })).toBeVisible({
        timeout: 15_000,
      });
    }
    const afterReload = await visibleOrder(page, titles);
    expect(afterReload).toEqual(expected);
  });

  test('reorder fires exactly one POST /tasks/reorder with all items', async ({ page }) => {
    await forceListView(page);
    await injectAuth(page.context(), tenant);
    await page.goto(`/projects/${tenant.projectId}/tasks`);

    for (const title of titles) {
      await expect(page.getByRole('link', { name: title, exact: true })).toBeVisible({
        timeout: 15_000,
      });
    }

    const reorderRequests: Array<{
      projectId: string;
      items: Array<{ id: string; sortWeight: number }>;
    }> = [];
    page.on('request', (req) => {
      if (req.method() === 'POST' && req.url().endsWith('/tasks/reorder')) {
        try {
          const body = req.postDataJSON() as {
            projectId: string;
            items: Array<{ id: string; sortWeight: number }>;
          };
          reorderRequests.push(body);
        } catch {
          // ignore non-JSON bodies — should not happen for this endpoint
        }
      }
    });

    // Drag the first visible row down by one (single neighbour swap).
    const observed = await visibleOrder(page, titles);
    expect(observed).toHaveLength(titles.length);

    const settle = page.waitForResponse(
      (res) => res.url().endsWith('/tasks/reorder') && res.request().method() === 'POST',
    );
    await nativeDnd(
      page,
      dragHandleForTitle(page, observed[0] as string),
      dragHandleForTitle(page, observed[1] as string),
    );
    await settle;

    expect(reorderRequests, 'exactly one /tasks/reorder POST per drop').toHaveLength(1);
    const sent = reorderRequests[0];
    expect(sent?.projectId).toBe(tenant.projectId);
    // The handler re-numbers the whole visible list on each drop, so the
    // payload size equals N visible tasks (which includes any pre-seeded
    // tasks the shared tenant already owns) — must be at least the seeded
    // titles for this spec.
    const sentItems = sent?.items ?? [];
    expect(sentItems.length).toBeGreaterThanOrEqual(titles.length);
    // Every seeded task id must appear in the payload.
    const sentIds = new Set(sentItems.map((i) => i.id));
    for (const row of seeded) {
      expect(sentIds.has(row.id), `payload should include ${row.title}`).toBe(true);
    }
    // Sort weights are sequential multiples of 1000 (per task-list-view).
    const weights = sentItems.map((i) => i.sortWeight).sort((a, b) => a - b);
    const expectedWeights = sentItems.map((_, i) => (i + 1) * 1000);
    expect(weights).toEqual(expectedWeights);
  });

  test('reorder rolls back optimistic UI when server returns 500', async ({ page }) => {
    await forceListView(page);
    await injectAuth(page.context(), tenant);

    // Intercept POST /tasks/reorder and force a server error before the
    // page navigates so the very first attempt fails. The CORS shim
    // installed by injectAuth uses route.fetch(); we register a more
    // specific handler here so this one wins for the reorder endpoint.
    await page.route(
      (url) => url.href.endsWith('/tasks/reorder'),
      async (route) => {
        if (route.request().method() !== 'POST') {
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
            detail: 'forced for E2E rollback test',
          }),
        });
      },
    );

    await page.goto(`/projects/${tenant.projectId}/tasks`);

    for (const title of titles) {
      await expect(page.getByRole('link', { name: title, exact: true })).toBeVisible({
        timeout: 15_000,
      });
    }
    const before = await visibleOrder(page, titles);
    expect(before).toHaveLength(titles.length);

    const settle = page.waitForResponse(
      (res) => res.url().endsWith('/tasks/reorder') && res.request().method() === 'POST',
    );
    // Drag first row to last (largest swap to make any non-rollback
    // bug obvious in the UI diff).
    await nativeDnd(
      page,
      dragHandleForTitle(page, before[0] as string),
      dragHandleForTitle(page, before[before.length - 1] as string),
    );
    const res = await settle;
    expect(res.status(), 'mocked endpoint should report 500').toBe(500);

    // Server state must be unchanged.
    const serverOrder = await fetchProjectOrder(tenant, titles);
    expect(serverOrder).toEqual(before);

    // UI must roll back to the original order. Poll because the
    // mutation's onError runs asynchronously and onSettled invalidates
    // the query, which schedules another render.
    await expect.poll(() => visibleOrder(page, titles), { timeout: 5_000 }).toEqual(before);
  });
});
