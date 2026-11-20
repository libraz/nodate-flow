/**
 * Daily-work E2E: completing assigned tasks in place (My tasks and Home),
 * choosing an assignee at creation, comment placement on the task detail,
 * and the Inbox / Notifications cross-references.
 *
 * Rows expose no state badge, so state is read back through GET /tasks/{id}.
 */

import { expect, test } from '@playwright/test';

import enCommon from '../locales/en/common.json' with { type: 'json' };
import enHelp from '../locales/en/help.json' with { type: 'json' };
import enInbox from '../locales/en/inbox.json' with { type: 'json' };
import enNotifications from '../locales/en/notifications.json' with { type: 'json' };
import jaHelp from '../locales/ja/help.json' with { type: 'json' };
import jaInbox from '../locales/ja/inbox.json' with { type: 'json' };
import jaNotifications from '../locales/ja/notifications.json' with { type: 'json' };
import zhHelp from '../locales/zh/help.json' with { type: 'json' };
import zhInbox from '../locales/zh/inbox.json' with { type: 'json' };
import zhNotifications from '../locales/zh/notifications.json' with { type: 'json' };
import {
  API_BASE_URL,
  AUTH_API_URL,
  cleanupTenant,
  createTask,
  createTestTenant,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';

const copy = {
  undo: enCommon.common.undo,
  completeUnavailable: enCommon.today.complete_unavailable,
  newTask: enCommon.tasks.new,
  commentsTitle: enCommon.tasks.comments.title,
  dependenciesTitle: enCommon.tasks.detail.dependencies.title,
  recentTasks: enCommon.home.recent_tasks,
} as const;

/** Accessible name of a row's complete button for a given task title. */
function completeName(title: string): string {
  return enCommon.today.complete_task.replace('{title}', title);
}

function authHeaders(tenant: TestTenant): Record<string, string> {
  return {
    'Content-Type': 'application/json',
    accept: 'application/json',
    authorization: `Bearer ${tenant.accessToken}`,
  };
}

async function transition(tenant: TestTenant, taskId: string, name: string): Promise<void> {
  const res = await fetch(`${API_BASE_URL}/tasks/${taskId}/transitions`, {
    method: 'POST',
    headers: authHeaders(tenant),
    body: JSON.stringify({ transition: name }),
  });
  if (!res.ok) {
    throw new Error(`POST /tasks/{id}/transitions (${name}) -> ${res.status} ${await res.text()}`);
  }
}

async function derivedState(tenant: TestTenant, taskId: string): Promise<string> {
  const res = await fetch(`${API_BASE_URL}/tasks/${taskId}`, { headers: authHeaders(tenant) });
  if (!res.ok) throw new Error(`GET /tasks/{id} -> ${res.status} ${await res.text()}`);
  return ((await res.json()) as { derivedState: string }).derivedState;
}

/** Adds an existing user to the owner's workspace (same call as calendar-members.spec.ts). */
async function addWorkspaceMember(owner: TestTenant, invitee: TestTenant): Promise<void> {
  const res = await fetch(`${AUTH_API_URL}/workspaces/${owner.workspaceId}/members`, {
    method: 'POST',
    headers: authHeaders(owner),
    body: JSON.stringify({ email: invitee.email, role: 'member' }),
  });
  if (!res.ok) {
    throw new Error(`POST /workspaces/{id}/members -> ${res.status} ${await res.text()}`);
  }
}

test.describe('daily work', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  const tenants: TestTenant[] = [];
  test.afterEach(async () => {
    for (const t of tenants.splice(0)) await cleanupTenant(t);
  });

  async function setup(opts: { locale?: 'en' | 'ja' } = {}): Promise<TestTenant> {
    const tenant = await createTestTenant(opts);
    tenants.push(tenant);
    return tenant;
  }

  test('open task: complete from My tasks, undo restores the open state', async ({ page }) => {
    const tenant = await setup();
    const title = `Complete open ${Date.now().toString(36)}`;
    const task = await createTask(tenant, title);
    expect(await derivedState(tenant, task.id)).toBe('open');

    await injectAuth(page.context(), tenant);
    await page.goto('/today');
    const button = page.getByRole('button', { name: completeName(title) });
    await expect(button).toBeVisible({ timeout: 15_000 });
    await expect(button).not.toHaveAttribute('aria-disabled', 'true');

    await button.click();
    await expect(button).toBeHidden({ timeout: 10_000 });
    await expect.poll(() => derivedState(tenant, task.id)).toBe('done');

    await page.getByRole('button', { name: copy.undo, exact: true }).click();
    await expect(page.getByRole('button', { name: completeName(title) })).toBeVisible({
      timeout: 10_000,
    });
    await expect.poll(() => derivedState(tenant, task.id)).toBe('open');
  });

  test('review task: complete from My tasks, undo restores the review state', async ({ page }) => {
    const tenant = await setup();
    const title = `Complete review ${Date.now().toString(36)}`;
    const task = await createTask(tenant, title);
    await transition(tenant, task.id, 'start');
    await transition(tenant, task.id, 'submit');
    expect(await derivedState(tenant, task.id)).toBe('review');

    await injectAuth(page.context(), tenant);
    await page.goto('/today');
    const button = page.getByRole('button', { name: completeName(title) });
    await expect(button).toBeVisible({ timeout: 15_000 });
    await expect(button).not.toHaveAttribute('aria-disabled', 'true');

    await button.click();
    await expect(button).toBeHidden({ timeout: 10_000 });
    await expect.poll(() => derivedState(tenant, task.id)).toBe('done');

    await page.getByRole('button', { name: copy.undo, exact: true }).click();
    await expect(page.getByRole('button', { name: completeName(title) })).toBeVisible({
      timeout: 10_000,
    });
    await expect.poll(() => derivedState(tenant, task.id)).toBe('review');
  });

  test('home: completing one of six open tasks still lists five rows', async ({ page }) => {
    const tenant = await setup();
    const stamp = Date.now().toString(36);
    for (let i = 1; i <= 6; i++) await createTask(tenant, `Home task ${i} ${stamp}`);

    await injectAuth(page.context(), tenant);
    await page.goto('/');
    const section = page.locator('section').filter({
      has: page.getByRole('heading', { level: 2, name: copy.recentTasks, exact: true }),
    });
    const rows = section.getByRole('listitem');
    await expect(rows).toHaveCount(5, { timeout: 15_000 });

    const first = section.getByRole('button', { name: /^Mark .* done$/ }).first();
    const label = (await first.getAttribute('aria-label')) ?? '';
    const completedTitle = /“(.*)”/.exec(label)?.[1] ?? '';
    expect(completedTitle).toContain(`Home task`);

    await first.click();
    await expect(section.getByRole('button', { name: completeName(completedTitle) })).toHaveCount(
      0,
      { timeout: 10_000 },
    );
    await expect(rows).toHaveCount(5);
  });

  test('waiting task: the complete button is aria-disabled and explains why', async ({ page }) => {
    const tenant = await setup();
    const title = `Waiting ${Date.now().toString(36)}`;
    const task = await createTask(tenant, title);
    await transition(tenant, task.id, 'start');
    expect(await derivedState(tenant, task.id)).toBe('waiting');

    await injectAuth(page.context(), tenant);
    await page.goto('/today');
    const button = page.getByRole('button', { name: completeName(title) });
    await expect(button).toBeVisible({ timeout: 15_000 });
    await expect(button).toHaveAttribute('aria-disabled', 'true');

    const describedBy = await button.getAttribute('aria-describedby');
    expect(describedBy).toBeTruthy();
    await expect(page.locator(`[id="${describedBy}"]`)).toHaveText(copy.completeUnavailable);

    // Playwright treats aria-disabled as non-actionable, hence force.
    await button.click({ force: true });
    await expect(button).toBeVisible();
    expect(await derivedState(tenant, task.id)).toBe('waiting');
  });

  test('create dialog: picking another member makes them the assignee', async ({ page }) => {
    const owner = await setup();
    const invitee = await setup();
    await addWorkspaceMember(owner, invitee);

    await injectAuth(page.context(), owner);
    await page.goto(`/projects/${owner.projectId}/tasks`);
    await page.getByRole('button', { name: copy.newTask }).first().click();

    const dialog = page.getByRole('dialog');
    const title = `Assigned ${Date.now().toString(36)}`;
    await dialog.getByLabel(enCommon.tasks.form.title).fill(title);

    const assignee = dialog.getByRole('combobox', { name: enCommon.tasks.form.assignee });
    await assignee.fill(invitee.displayName);
    await page.getByRole('option', { name: invitee.displayName }).click();
    await expect(assignee).toHaveValue(invitee.displayName);
    await dialog.getByRole('button', { name: enCommon.tasks.form.submit, exact: true }).click();

    await page.getByText(title).first().click();
    await expect(page).toHaveURL(/\/tasks\//, { timeout: 10_000 });
    await expect(page.locator('aside').getByText(invitee.displayName).first()).toBeVisible({
      timeout: 10_000,
    });
  });

  test('task detail: comments sit above dependencies', async ({ page }) => {
    const tenant = await setup();
    const title = `Layout ${Date.now().toString(36)}`;
    const task = await createTask(tenant, title);

    await injectAuth(page.context(), tenant);
    await page.goto(`/tasks/${task.id}`);
    const comments = page.getByRole('heading', { name: copy.commentsTitle, exact: true });
    const dependencies = page.getByRole('heading', { name: copy.dependenciesTitle, exact: true });
    await expect(comments).toBeVisible({ timeout: 15_000 });
    await expect(dependencies).toBeVisible();

    const commentsBox = await comments.boundingBox();
    const dependenciesBox = await dependencies.boundingBox();
    expect(commentsBox).not.toBeNull();
    expect(dependenciesBox).not.toBeNull();
    expect(commentsBox?.y ?? Number.POSITIVE_INFINITY).toBeLessThan(
      dependenciesBox?.y ?? Number.NEGATIVE_INFINITY,
    );
  });
});

test.describe('inbox and notifications wording', () => {
  const locales = {
    en: { inbox: enInbox, notifications: enNotifications, help: enHelp },
    ja: { inbox: jaInbox, notifications: jaNotifications, help: jaHelp },
    zh: { inbox: zhInbox, notifications: zhNotifications, help: zhHelp },
  } as const;

  for (const [locale, { inbox, notifications, help }] of Object.entries(locales)) {
    test(`${locale}: the inbox subtitle names Notifications and Intake, the notifications help names the Inbox`, () => {
      expect(inbox.view.subtitle).toContain(notifications.view.title);
      expect(inbox.view.subtitle).toContain(inbox.tab.intake);
      expect(help.notifications.summary).toContain(inbox.view.title);
    });
  }

  test('ja tenant sees the subtitle on /inbox', async ({ page }) => {
    const tenant = await createTestTenant({ locale: 'ja' });
    try {
      await injectAuth(page.context(), tenant);
      await page.goto('/inbox');
      await expect(page.getByText(jaInbox.view.subtitle)).toBeVisible({ timeout: 15_000 });
    } finally {
      await cleanupTenant(tenant);
    }
  });
});
