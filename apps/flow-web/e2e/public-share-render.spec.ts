/**
 * Public share render-with-events E2E.
 *
 * Sister spec to public-share-calendar.spec.ts. That spec proves the
 * anonymous /share/cal/{token} route mounts and renders the share title.
 * This one extends the round trip to verify that an attached calendar
 * event is actually rendered on the public page — i.e. the
 * calendar_public_share_events join is honoured by the render handler
 * and surfaces the event title to an unauthenticated viewer.
 *
 * Steps:
 *   1. Create a fresh tenant.
 *   2. Resolve the auto-created personal calendar and POST a single
 *      event into it for a stable, future-dated time window.
 *   3. Create a public share via REST and capture its plaintext token.
 *   4. Attach the event to the share via REST.
 *   5. Open /share/cal/{token} in an incognito browser context (no
 *      auth, no cookies) and assert both the share title and the event
 *      title render.
 *
 * The tenant is created per-test (not borrowed from globalSetup) so
 * mutations stay scoped and parallel-safe.
 */

import { expect, test } from '@playwright/test';

import {
  API_BASE_URL,
  type TestTenant,
  cleanupTenant,
  createCalendarEvent,
  createTestTenant,
  ensurePersonalCalendar,
} from './fixtures/tenant';

interface CreateShareResponse {
  id: string;
  title: string;
  token: string;
}

interface AttachShareResponse {
  attached: number;
  skipped: number;
}

async function createPublicShare(tenant: TestTenant, title: string): Promise<CreateShareResponse> {
  const res = await fetch(`${API_BASE_URL}/workspaces/${tenant.workspaceId}/public-shares`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
    body: JSON.stringify({ title }),
  });
  if (!res.ok) {
    throw new Error(`POST /public-shares -> ${res.status} ${await res.text()}`);
  }
  return (await res.json()) as CreateShareResponse;
}

async function attachEventToShare(
  tenant: TestTenant,
  shareId: string,
  eventId: string,
): Promise<AttachShareResponse> {
  const res = await fetch(
    `${API_BASE_URL}/workspaces/${tenant.workspaceId}/public-shares/${shareId}/events`,
    {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        accept: 'application/json',
        authorization: `Bearer ${tenant.accessToken}`,
      },
      body: JSON.stringify({ eventIds: [eventId] }),
    },
  );
  if (!res.ok) {
    throw new Error(`POST /public-shares/{id}/events -> ${res.status} ${await res.text()}`);
  }
  return (await res.json()) as AttachShareResponse;
}

test.describe('public share render with events', () => {
  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('attached calendar event renders for an anonymous viewer', async ({ browser }) => {
    tenant = await createTestTenant();
    const t = tenant;

    // A stable future-dated event so the public renderer always shows it.
    // Using midnight UTC three days out keeps it well clear of the
    // current-day boundary regardless of test runner timezone.
    const startAt = Math.floor(Date.now() / 1000) + 3 * 24 * 60 * 60;
    const endAt = startAt + 60 * 60;
    const eventTitle = `Shared Event ${Date.now().toString(36)}`;

    const calendar = await ensurePersonalCalendar(t);
    const event = await createCalendarEvent(t, calendar.id, {
      title: eventTitle,
      startAt,
      endAt,
      timezone: 'UTC',
    });
    expect(event.id.length).toBeGreaterThan(0);

    const shareTitle = `E2E Share With Event ${Date.now().toString(36)}`;
    const share = await createPublicShare(t, shareTitle);
    expect(share.token.length).toBeGreaterThan(0);

    const attach = await attachEventToShare(t, share.id, event.id);
    expect(attach.attached).toBe(1);
    expect(attach.skipped).toBe(0);

    // Anonymous context: no auth, no cookies — the token alone is the
    // sole proof of access.
    const anon = await browser.newContext();
    try {
      const page = await anon.newPage();
      await page.goto(`/share/cal/${share.token}`);
      await page.waitForLoadState('domcontentloaded');

      // Share title renders as h1.
      await expect(page.getByRole('heading', { level: 1, name: shareTitle })).toBeVisible({
        timeout: 15_000,
      });

      // The attached event title appears somewhere in the body.
      // Scope to the page body so we tolerate whatever element the
      // renderer chooses (list item, card, etc.).
      await expect(page.getByText(eventTitle, { exact: false })).toBeVisible({
        timeout: 15_000,
      });

      // Sanity: no i18n key leakage.
      const bodyText = await page.locator('body').innerText();
      expect(bodyText).not.toMatch(/\bshare\.\w+\.\w+/);
      expect(bodyText).not.toMatch(/\bsharing\.\w+\.\w+/);
    } finally {
      await anon.close();
    }
  });
});
