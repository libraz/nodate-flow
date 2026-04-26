/**
 * Archive Room E2E (G4 — `/workspaces/{wsId}/tasks/archived`).
 *
 * Smoke-level coverage for the new archive surface in flow-web. The page
 * is a cursor-paginated list with a debounced search, range pills
 * (7d / 30d / 90d / all), and project / archiver filters. We exercise the
 * golden-path render of each branch (truly empty, populated, search-
 * filtered) plus the range pill `aria-checked` toggle so we know the
 * radiogroup wiring is intact.
 *
 * Cases:
 *   A. truly empty workspace renders the "Nothing stored yet" empty
 *      state.
 *   B. a task archived via REST shows up in the list after navigation.
 *   C. clicking the 7d range pill flips its `aria-checked` to true (the
 *      surface assertion that the radiogroup is wired correctly).
 *   D. typing a non-matching query into the search input flips the
 *      surface to the "couldn't find any matching archive" filtered
 *      empty state after the 200ms debounce.
 *
 * Each test creates its own tenant via REST so the suite stays
 * parallel-safe.
 */

import { type Page, expect, test } from '@playwright/test';

import enArchive from '../locales/en/archive.json' with { type: 'json' };
import {
  API_BASE_URL,
  type TestTenant,
  cleanupTenant,
  createTask,
  createTestTenant,
  injectAuth,
} from './fixtures/tenant';

const copy = {
  emptyTitle: enArchive.empty.noneTitle,
  filteredTitle: enArchive.empty.filteredTitle,
  searchPlaceholder: enArchive.filters.searchPlaceholder,
  range7d: enArchive.filters.range['7d'],
  range30d: enArchive.filters.range['30d'],
  rangeAll: enArchive.filters.range.all,
} as const;

/**
 * Archives a task via REST (POST /tasks/{id}/archive). Used by the
 * happy-path test to seed a row the archive list must surface.
 */
async function archiveTask(tenant: TestTenant, taskId: string): Promise<void> {
  const res = await fetch(`${API_BASE_URL}/tasks/${taskId}/archive`, {
    method: 'POST',
    headers: {
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
  });
  if (!res.ok) {
    throw new Error(`POST /tasks/${taskId}/archive -> ${res.status} ${await res.text()}`);
  }
}

async function openArchive(page: Page, tenant: TestTenant): Promise<void> {
  await page.goto(`/workspaces/${tenant.workspaceId}/tasks/archived`);
  await page.waitForLoadState('domcontentloaded');
  // Both branches mount the search filter bar, so use it as the
  // readiness signal.
  await expect(page.getByRole('search')).toBeVisible({ timeout: 15_000 });
}

test.describe('archive room', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('A: empty workspace renders the "nothing stored yet" empty state', async ({ page }) => {
    tenant = await createTestTenant();
    await injectAuth(page.context(), tenant);
    await openArchive(page, tenant);

    await expect(page.getByRole('heading', { name: copy.emptyTitle })).toBeVisible({
      timeout: 10_000,
    });
  });

  test('B: a task archived via REST shows up in the list', async ({ page }) => {
    tenant = await createTestTenant();
    const title = `Archive subject ${Date.now().toString(36)}`;
    const task = await createTask(tenant, title);
    await archiveTask(tenant, task.id);

    await injectAuth(page.context(), tenant);
    await openArchive(page, tenant);

    // Row title is a `<Link to="/tasks/{taskId}">{title}</Link>`, so the
    // most stable assertion is the link by accessible name.
    await expect(page.getByRole('link', { name: title })).toBeVisible({ timeout: 10_000 });
  });

  test('C: 7d range pill flips aria-checked when clicked', async ({ page }) => {
    tenant = await createTestTenant();
    await injectAuth(page.context(), tenant);
    await openArchive(page, tenant);

    // Default range is `30d` (see use-archive-filters.ts), so the 7d
    // pill starts unchecked. After a click it must be the checked one.
    const pill7d = page.getByRole('radio', { name: copy.range7d });
    await expect(pill7d).toHaveAttribute('aria-checked', 'false');
    await pill7d.click();
    await expect(pill7d).toHaveAttribute('aria-checked', 'true');

    // The previously-active 30d pill must have flipped back to false.
    await expect(page.getByRole('radio', { name: copy.range30d })).toHaveAttribute(
      'aria-checked',
      'false',
    );
  });

  test('D: a non-matching search query reveals the filtered empty state', async ({ page }) => {
    tenant = await createTestTenant();
    const title = `Archive subject ${Date.now().toString(36)}`;
    const task = await createTask(tenant, title);
    await archiveTask(tenant, task.id);

    await injectAuth(page.context(), tenant);
    await openArchive(page, tenant);

    // Confirm the row exists before filtering so the assertion below
    // tests the filter, not a missing fixture.
    await expect(page.getByRole('link', { name: title })).toBeVisible({ timeout: 10_000 });

    // Switch the range to "All" so the search test is decoupled from
    // wall-clock drift around archivedAt vs. the default 30d window.
    await page.getByRole('radio', { name: copy.rangeAll }).click();

    // Type a query that cannot match the seeded title. The filter
    // applies after the 200ms debounce, so the filtered empty state
    // shows up shortly after.
    await page.getByPlaceholder(copy.searchPlaceholder).fill('zzz-no-match-zzz');
    await expect(page.getByRole('heading', { name: copy.filteredTitle })).toBeVisible({
      timeout: 5_000,
    });
  });
});
