/**
 * Notification dropdown — keyset Load more flow.
 *
 * Verifies that the notification bell's infinite-paginated dropdown
 * advances cleanly through three cursor pages: an initial 10, a "Load
 * more" click that yields the next 10, and a third click that drains
 * the remaining 5 (final page → no more pages → button disappears).
 *
 * Why route interception
 * ----------------------
 * Notifications are server-generated side effects of other actions
 * (assignments, mentions, deadline reminders). There is no public REST
 * endpoint to seed a fixed set, so a real-API spec would either need to
 * orchestrate dozens of cross-tenant operations (slow, brittle) or wait
 * for the background scheduler (non-deterministic). Both run counter to
 * what this spec asserts: the UI's pagination wiring against the
 * pre-v1 cursor contract (`empty cursor → first page`,
 * `nextCursor: string | null`). We therefore intercept just the single
 * `GET /me/notifications` call and let every other API request flow to
 * the real backend, which keeps auth bootstrap and unread-count polling
 * working unchanged.
 *
 * The keyset semantics themselves are covered by the Go e2e tests in
 * apps/flow-api/tests/e2e/notification_test.go; here we only verify that
 * the React layer threads cursors correctly and renders the right number
 * of rows after each page advance.
 */

import { type Route, expect, test } from '@playwright/test';

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

function row(workspaceId: string, idx: number): NotifRow {
  return {
    id: `seed-notif-${idx}`,
    workspaceId,
    actorId: null,
    actorDisplayName: null,
    eventType: 'task.created',
    resourceType: 'task',
    resourceId: null,
    title: `Seed notif #${idx}`,
    body: null,
    severity: 'normal',
    channel: 'in_app',
    readAt: 0,
    deliveredAt: 0,
    createdAt: 1_700_000_000 - idx,
    total: 25,
  };
}

test.describe('notifications dropdown — Load more', () => {
  test('walks 25 notifications across 3 cursor pages', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    const allRows = Array.from({ length: 25 }, (_, i) => row(tenant.workspaceId, i + 1));
    const PAGE_SIZE = 20;

    // Intercept only `/me/notifications` (NOT `/me/notifications/unread-count`,
    // which the bell polls every 30s — let that pass through to real API).
    await page.route(`${API_BASE_URL}/me/notifications**`, async (route: Route) => {
      const url = new URL(route.request().url());
      // Skip the unread-count subpath.
      if (url.pathname.endsWith('/unread-count')) {
        return route.fallback();
      }
      const cursor = url.searchParams.get('cursor') ?? '';
      let start = 0;
      if (cursor === 'cursor-2') start = PAGE_SIZE;
      const slice = allRows.slice(start, start + PAGE_SIZE);
      const nextCursor = start + PAGE_SIZE < allRows.length ? 'cursor-2' : null;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          notifications: slice,
          nextCursor,
          total: allRows.length,
        }),
      });
    });

    await page.goto('/');

    // Open the notification bell. Aria label flips between "{count} unread"
    // and "Notifications" depending on the unread-count fetch result; we
    // match either by the haspopup affordance.
    const bell = page.getByRole('button', { name: /notifications|unread/i }).first();
    await expect(bell).toBeVisible({ timeout: 10_000 });
    await bell.click();

    // First page: 20 rows visible.
    const dropdown = page.getByRole('dialog', { name: /notifications/i });
    await expect(dropdown).toBeVisible({ timeout: 5_000 });
    await expect(dropdown.getByText(/Seed notif #/)).toHaveCount(20, { timeout: 5_000 });

    // Load more is shown (more rows remain).
    const loadMore = dropdown.getByRole('button', { name: /Load more/i });
    await expect(loadMore).toBeVisible();
    await loadMore.click();

    // Second page: original 20 + 5 more = 25 rows.
    await expect(dropdown.getByText(/Seed notif #/)).toHaveCount(25, { timeout: 5_000 });

    // No more pages → Load more button is gone.
    await expect(loadMore).toHaveCount(0, { timeout: 5_000 });

    // Spot-check first and last rows are both rendered. Use exact match
    // because "Seed notif #1" is otherwise a substring of #10..#19.
    await expect(dropdown.getByText('Seed notif #1', { exact: true })).toBeVisible();
    await expect(dropdown.getByText('Seed notif #25', { exact: true })).toBeVisible();
  });
});
