/**
 * Public share detail editor E2E (G16).
 *
 * `public-share-create.spec.ts` covers the create + delete loop on the
 * list page. The detail page (`/workspaces/$id/settings/public-shares/$shareId`)
 * — where the workspace admin attaches events to the share, reorders
 * them, and detaches individual rows — was previously uncovered. This
 * spec drives:
 *
 *   1. seed a tenant + 2 calendar events via REST
 *   2. create a public share via REST so the test starts on a known
 *      empty editor (avoids re-running the create-dialog UI which
 *      already has its own spec)
 *   3. land on the detail page → empty-state copy is rendered
 *   4. click "Add events" → picker dialog opens with the seeded events
 *      as candidates → tick both → click Attach
 *   5. dialog closes; both rows appear in the editor table
 *   6. click "Remove" on the first row → themed confirm dialog → confirm
 *   7. row is gone; the second row remains
 *
 * REST is used to seed events because the calendar create UI has its
 * own dedicated spec; here we only need pre-existing rows for the
 * picker to surface, not coverage of the create flow.
 */

import { type BrowserContext, expect, type Page, test } from '@playwright/test';

import {
  API_BASE_URL,
  cleanupTenant,
  createCalendarEvent,
  createTestTenant,
  ensurePersonalCalendar,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';

interface CreatedShare {
  id: string;
  title: string;
}

async function createShareViaRest(tenant: TestTenant, title: string): Promise<CreatedShare> {
  const res = await fetch(`${API_BASE_URL}/workspaces/${tenant.workspaceId}/public-shares`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
    body: JSON.stringify({ title, description: 'E2E share detail editor' }),
  });
  if (!res.ok) {
    throw new Error(`POST /public-shares -> ${res.status} ${await res.text()}`);
  }
  const body = (await res.json()) as { id: string; title: string };
  return { id: body.id, title: body.title };
}

/** Returns the unix-seconds timestamp for `daysFromNow` from midnight UTC. */
function daysFromMidnight(daysFromNow: number, hours = 12): number {
  const d = new Date();
  d.setUTCHours(0, 0, 0, 0);
  d.setUTCDate(d.getUTCDate() + daysFromNow);
  d.setUTCHours(hours);
  return Math.floor(d.getTime() / 1000);
}

async function gotoDetail(page: Page, tenant: TestTenant, shareId: string): Promise<void> {
  await page.goto(`/workspaces/${tenant.workspaceId}/settings/public-shares/${shareId}`);
}

test.describe('public share detail', () => {
  let tenant: TestTenant | null = null;
  let context: BrowserContext | null = null;

  test.beforeEach(async ({ context: ctx }) => {
    tenant = await createTestTenant();
    context = ctx;
  });

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('attach events from picker, then detach one and verify the row is gone', async ({
    page,
  }) => {
    if (!tenant || !context) throw new Error('tenant not initialised');

    // Seed 2 events on the personal calendar — both within the picker's
    // default date range (the picker defaults to "now → +30d").
    const cal = await ensurePersonalCalendar(tenant);
    const evtA = await createCalendarEvent(tenant, cal.id, {
      title: `Detail evt A ${Date.now().toString(36)}`,
      startAt: daysFromMidnight(2, 9),
      endAt: daysFromMidnight(2, 10),
    });
    const evtB = await createCalendarEvent(tenant, cal.id, {
      title: `Detail evt B ${Date.now().toString(36)}`,
      startAt: daysFromMidnight(3, 14),
      endAt: daysFromMidnight(3, 15),
    });

    const share = await createShareViaRest(tenant, `Detail share ${Date.now().toString(36)}`);

    await injectAuth(context, tenant);
    await gotoDetail(page, tenant, share.id);

    // The detail editor renders the share title as the page heading.
    await expect(page.getByRole('heading', { name: share.title, level: 1 })).toBeVisible({
      timeout: 15_000,
    });

    // Empty state copy because no events are attached yet.
    await expect(page.getByText(/no events yet\./i)).toBeVisible();

    // Open the Add Events picker. Two buttons share the "Add events"
    // label inside the dialog (header trigger + dialog submit), so
    // narrow the click to the page-level header trigger.
    await page
      .getByRole('button', { name: /^add events$/i, exact: true })
      .first()
      .click();
    const picker = page.getByRole('dialog');
    await expect(picker).toBeVisible({ timeout: 5_000 });
    await expect(picker.getByRole('heading', { name: /add events to share/i })).toBeVisible();

    // The picker lists the seeded events as candidates. Each row groups
    // a checkbox with the title in a sibling span (no implicit label
    // association), so we scope the checkbox lookup to the row that
    // carries the event title and click the role inside.
    const candidateA = picker.locator('li').filter({ hasText: evtA.title });
    const candidateB = picker.locator('li').filter({ hasText: evtB.title });
    await candidateA.getByRole('checkbox').check();
    await candidateB.getByRole('checkbox').check();

    // The Attach button label flips to "Attach 2 events" once any row
    // is selected, so the regex tolerates the localized count phrase.
    await picker.getByRole('button', { name: /^attach\b/i }).click();
    await expect(picker).toBeHidden({ timeout: 10_000 });

    // Both rows appear in the editor table.
    const rowA = page.getByRole('row').filter({ hasText: evtA.title });
    const rowB = page.getByRole('row').filter({ hasText: evtB.title });
    await expect(rowA).toBeVisible({ timeout: 10_000 });
    await expect(rowB).toBeVisible();

    // Detach event A. Each row exposes a single Remove button; the
    // themed confirm dialog asks for confirmation before the request
    // fires.
    await rowA.getByRole('button', { name: /^remove$/i, exact: true }).click();
    const confirmDialog = page.getByRole('dialog');
    await expect(confirmDialog).toBeVisible({ timeout: 5_000 });
    await confirmDialog.getByRole('button', { name: /^(confirm|実行|remove)$/i }).click();
    await expect(confirmDialog).toBeHidden({ timeout: 5_000 });

    // Row A disappears; row B remains.
    await expect(rowA).toBeHidden({ timeout: 10_000 });
    await expect(rowB).toBeVisible();
  });
});
