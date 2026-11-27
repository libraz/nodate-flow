/**
 * Board view E2E.
 *
 * Cards are dragged between columns with @dnd-kit/core (pointer sensor with a
 * 5px activation distance, keyboard sensor: Space lifts, Left/Right pick a
 * column, Space drops, Escape cancels). The column under the drop picks the
 * transition; illegal and same-column drops are no-ops.
 *
 * Each test owns a tenant so moving cards never touches shared seeds.
 */

import { expect, type Locator, type Page, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import {
  API_BASE_URL,
  cleanupTenant,
  createTask,
  createTestTenant,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

const TRANSITIONS_URL = /\/tasks\/[^/]+\/transitions$/;

async function transitionTask(tenant: TestTenant, taskId: string, name: string): Promise<void> {
  const res = await fetch(`${API_BASE_URL}/tasks/${taskId}/transitions`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
    body: JSON.stringify({ transition: name }),
  });
  expect(res.ok).toBe(true);
}

function column(page: Page, name: string): Locator {
  return page.getByRole('region', { name, exact: true });
}

function card(page: Page, title: string): Locator {
  return page.getByRole('group', { name: title, exact: true });
}

async function center(locator: Locator): Promise<{ x: number; y: number }> {
  const box = await locator.boundingBox();
  if (!box) throw new Error('element has no bounding box');
  return { x: box.x + box.width / 2, y: box.y + box.height / 2 };
}

/**
 * Presses on `source`, crosses the activation distance, then walks to the
 * centre of `target` in small steps: dnd-kit needs intermediate pointermove
 * events to register the hover.
 */
async function dragTo(page: Page, source: Locator, target: Locator): Promise<void> {
  const from = await center(source);
  await page.mouse.move(from.x, from.y);
  await page.mouse.down();
  await page.mouse.move(from.x + 10, from.y + 10, { steps: 5 });
  await expect(page.getByRole('region', { name: 'Board' })).toHaveAttribute(
    'data-drag-active',
    'true',
  );
  const to = await center(target);
  await page.mouse.move(to.x, to.y, { steps: 25 });
  await expect(target).toHaveAttribute('data-drop', 'over');
  await page.mouse.up();
}

test.describe('board view', () => {
  test('task card appears in open column on board view', async ({ page }) => {
    const { user: tenant, seededTasks } = loadTenants();

    await injectAuth(page.context(), tenant);

    // Navigate to the project tasks view (board is the default sub-view)
    await page.goto(`/projects/${tenant.projectId}/tasks`);

    // Verify the pre-seeded task card is visible on the board
    await expect(page.getByText(seededTasks.board).first()).toBeVisible({ timeout: 10_000 });

    // Accessibility check on the board view
    await checkA11y(page, ['color-contrast', 'region', 'aria-required-children']);
  });
});

test.describe('board drag and drop', () => {
  let tenant: TestTenant;

  test.beforeEach(async ({ page }) => {
    tenant = await createTestTenant();
    await injectAuth(page.context(), tenant);
  });

  test.afterEach(async () => {
    await cleanupTenant(tenant);
  });

  test('pointer drag from Open to Done moves the card and persists after reload', async ({
    page,
  }) => {
    const title = `E2E DnD Done ${Date.now().toString(36)}`;
    await createTask(tenant, title);
    await page.goto(`/projects/${tenant.projectId}/tasks`);
    await expect(column(page, 'Open').getByRole('group', { name: title })).toBeVisible({
      timeout: 10_000,
    });

    const transition = page.waitForResponse(
      (res) => res.request().method() === 'POST' && TRANSITIONS_URL.test(res.url()),
    );
    await dragTo(page, card(page, title), column(page, 'Done'));
    expect((await transition).ok()).toBe(true);

    await expect(column(page, 'Done').getByRole('group', { name: title })).toBeVisible();
    await expect(column(page, 'Open').getByRole('group', { name: title })).toHaveCount(0);

    await page.reload();
    await expect(column(page, 'Done').getByRole('group', { name: title })).toBeVisible({
      timeout: 10_000,
    });
    await expect(column(page, 'Open').getByRole('group', { name: title })).toHaveCount(0);
  });

  test('dropping on an illegal column leaves the card in place and sends no transition', async ({
    page,
  }) => {
    // Done only allows `reopen` (back to Open), so Review is unreachable.
    const title = `E2E DnD Illegal ${Date.now().toString(36)}`;
    const { id } = await createTask(tenant, title);
    await transitionTask(tenant, id, 'complete');

    const transitionRequests: string[] = [];
    page.on('request', (req) => {
      if (req.method() === 'POST' && TRANSITIONS_URL.test(req.url())) {
        transitionRequests.push(req.url());
      }
    });

    await page.goto(`/projects/${tenant.projectId}/tasks`);
    await expect(column(page, 'Done').getByRole('group', { name: title })).toBeVisible({
      timeout: 10_000,
    });

    const from = await center(card(page, title));
    await page.mouse.move(from.x, from.y);
    await page.mouse.down();
    await page.mouse.move(from.x + 10, from.y + 10, { steps: 5 });
    const to = await center(column(page, 'Review'));
    await page.mouse.move(to.x, to.y, { steps: 25 });
    await expect(column(page, 'Review')).toHaveAttribute('data-drop', 'illegal');
    await page.mouse.up();

    // The drag is over once the board stops flagging it.
    await expect(page.getByRole('region', { name: 'Board' })).not.toHaveAttribute(
      'data-drag-active',
      'true',
    );
    await expect(column(page, 'Done').getByRole('group', { name: title })).toBeVisible();
    await expect(column(page, 'Review').getByRole('group', { name: title })).toHaveCount(0);
    expect(transitionRequests).toEqual([]);
  });

  test('keyboard drag moves a card one column to the right', async ({ page }) => {
    const title = `E2E DnD Keyboard ${Date.now().toString(36)}`;
    await createTask(tenant, title);
    await page.goto(`/projects/${tenant.projectId}/tasks`);
    const openCard = column(page, 'Open').getByRole('group', { name: title });
    await expect(openCard).toBeVisible({ timeout: 10_000 });

    await openCard.focus();
    await page.keyboard.press('Space');
    await expect(page.getByRole('region', { name: 'Board' })).toHaveAttribute(
      'data-drag-active',
      'true',
    );
    await page.keyboard.press('ArrowRight');
    await expect(column(page, 'Waiting')).toHaveAttribute('data-drop', 'over');

    const transition = page.waitForResponse(
      (res) => res.request().method() === 'POST' && TRANSITIONS_URL.test(res.url()),
    );
    await page.keyboard.press('Space');
    expect((await transition).ok()).toBe(true);

    await expect(column(page, 'Waiting').getByRole('group', { name: title })).toBeVisible();
    await expect(column(page, 'Open').getByRole('group', { name: title })).toHaveCount(0);
  });

  test('a plain click on the card opens the task detail', async ({ page }) => {
    const title = `E2E DnD Click ${Date.now().toString(36)}`;
    const { id } = await createTask(tenant, title);
    await page.goto(`/projects/${tenant.projectId}/tasks`);
    const openCard = column(page, 'Open').getByRole('group', { name: title });
    await expect(openCard).toBeVisible({ timeout: 10_000 });

    await openCard.getByRole('link', { name: title }).click();

    await expect(page).toHaveURL(new RegExp(`/tasks/${id}$`));
    await expect(page.getByRole('heading', { name: title })).toBeVisible({ timeout: 10_000 });
  });
});
