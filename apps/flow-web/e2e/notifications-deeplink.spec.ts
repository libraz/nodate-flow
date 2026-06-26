/**
 * Notification deep-link e2e.
 *
 * Clicking a row in the notification dropdown should mark the row as
 * read AND navigate to the underlying resource. We exercise the most
 * common pointer (`resourceType="task"`) by stubbing
 * `GET /me/notifications` so the dropdown opens with a single seeded
 * row pointing at a real task created via REST. Mark-read goes through
 * to the real backend; we only intercept the list call.
 *
 * Mirrors the route-interception pattern used by
 * `notifications-load-more.spec.ts`. Background unread-count polling is
 * left untouched so the bell button still mounts normally.
 */

import { expect, type Route, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { API_BASE_URL, injectAuth } from './fixtures/tenant';

interface NotifRow {
  id: string;
  workspaceId: string;
  actorId: string | null;
  actorDisplayName: string | null;
  eventType: string;
  resourceType: string;
  resourceId: string | null;
  title: string;
  body: string | null;
  severity: 'low' | 'normal' | 'high' | 'urgent';
  channel: string;
  readAt: number | null;
  deliveredAt: number | null;
  createdAt: number;
  total: number;
}

async function seedTask(
  tenant: { accessToken: string; projectId: string },
  title: string,
): Promise<{ id: string; title: string }> {
  const res = await fetch(`${API_BASE_URL}/tasks`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
    body: JSON.stringify({ title, projectId: tenant.projectId }),
  });
  if (!res.ok) throw new Error(`POST /tasks -> ${res.status} ${await res.text()}`);
  const body = (await res.json()) as { id: string; title: string };
  return { id: body.id, title: body.title };
}

test.describe('notifications dropdown — deep-link', () => {
  test('clicking a task notification navigates to the task detail page', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    const task = await seedTask(tenant, `Notif target ${Date.now().toString(36)}`);

    const seededRow: NotifRow = {
      id: 'seed-deeplink-1',
      workspaceId: tenant.workspaceId,
      actorId: null,
      actorDisplayName: null,
      eventType: 'task.created',
      resourceType: 'task',
      resourceId: task.id,
      title: `New task assigned: ${task.title}`,
      body: null,
      severity: 'normal',
      channel: 'in_app',
      readAt: null,
      deliveredAt: 0,
      createdAt: Math.floor(Date.now() / 1000),
      total: 1,
    };

    await page.route(`${API_BASE_URL}/me/notifications**`, async (route: Route) => {
      const url = new URL(route.request().url());
      if (url.pathname.endsWith('/unread-count')) {
        return route.fallback();
      }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          notifications: [seededRow],
          nextCursor: null,
          total: 1,
        }),
      });
    });

    await page.goto('/');

    const bell = page.getByRole('button', { name: /notifications|unread/i }).first();
    await expect(bell).toBeVisible({ timeout: 10_000 });
    await bell.click();

    const dropdown = page.getByRole('dialog', { name: /notifications/i });
    await expect(dropdown).toBeVisible({ timeout: 5_000 });

    const row = dropdown.getByText(seededRow.title);
    await expect(row).toBeVisible({ timeout: 5_000 });
    await row.click();

    // The dropdown closes and the route changes to the task detail.
    await expect(page).toHaveURL(new RegExp(`/tasks/${task.id}$`), { timeout: 10_000 });
    await expect(dropdown).toBeHidden({ timeout: 5_000 });

    // Sanity: the task detail page renders the task title.
    await expect(page.getByText(task.title).first()).toBeVisible({ timeout: 10_000 });
  });
});
