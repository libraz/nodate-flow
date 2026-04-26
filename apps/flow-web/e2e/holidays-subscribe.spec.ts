/**
 * Calendars rail — holiday subscription mode E2E (W11).
 *
 * The rail's third section mode (alongside `list` and `discover`) lets the
 * actor subscribe their workspace to a national holiday feed by picking
 * an ISO 3166-1 alpha-2 country code. See
 * apps/flow-web/src/features/calendars-rail/calendars-rail.tsx and
 * apps/flow-web/src/features/calendars-rail/holidays-list.tsx.
 *
 * Cases:
 *   A. open rail → "Subscribe holiday calendar..." trigger flips section
 *      to holidays mode; the country combobox renders and starts empty
 *      because the workspace was created without a country preset.
 *   B. pick a country (JP) → submit → POST /workspaces/{wsId}/calendars/
 *      subscribe-system fires → section returns to list mode → the new
 *      "Holidays: Japan" calendar appears in the rail.
 *   C. back arrow returns to list mode without subscribing.
 *
 * Each test creates its own tenant via REST so the suite stays
 * parallel-safe and never leans on the shared pool from global-setup.
 */

import { type Page, expect, test } from '@playwright/test';

import enCommon from '../locales/en/common.json' with { type: 'json' };
import {
  API_BASE_URL,
  type TestTenant,
  cleanupTenant,
  createTestTenant,
  injectAuth,
} from './fixtures/tenant';

const copy = {
  railTitle: enCommon.calendars_rail.title,
  trigger: enCommon.calendars_rail.holidays.trigger,
  holidaysTitle: enCommon.calendars_rail.holidays.title,
  countryLabel: enCommon.calendars_rail.holidays.country_label,
  countryPlaceholder: enCommon.calendars_rail.holidays.country_placeholder,
  subscribe: enCommon.calendars_rail.holidays.subscribe,
} as const;

/**
 * Navigates to /calendar and waits for the rail to mount. The rail's
 * `<aside aria-label="Calendars">` is the most stable readiness signal —
 * it renders independently of the month grid hydration.
 */
async function openCalendarWithRail(page: Page): Promise<void> {
  await page.goto('/calendar');
  await expect(
    page.getByRole('heading', { level: 1, name: /^(Calendar|カレンダー)$/ }),
  ).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole('complementary', { name: copy.railTitle })).toBeVisible({
    timeout: 10_000,
  });
}

test.describe('calendars rail — holidays subscribe', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('A: opens holidays mode with empty country picker', async ({ page }) => {
    tenant = await createTestTenant();
    await injectAuth(page.context(), tenant);
    await openCalendarWithRail(page);

    // Click the "Subscribe holiday calendar..." trigger.
    await page.getByRole('button', { name: copy.trigger }).click();

    // The section morph swaps the header to "Subscribe holiday calendar".
    await expect(page.getByRole('heading', { name: copy.holidaysTitle })).toBeVisible({
      timeout: 5_000,
    });

    // Country combobox is mounted with a placeholder + empty value.
    // createTestTenant does not set workspace.country, so the picker
    // starts unset and the submit button is disabled.
    const countryPicker = page.getByRole('combobox', { name: copy.countryLabel });
    await expect(countryPicker).toBeVisible();
    await expect(countryPicker).toHaveValue('');

    const submit = page.getByRole('button', { name: copy.subscribe });
    await expect(submit).toBeDisabled();
  });

  test('B: subscribes JP and the holiday calendar appears in the rail', async ({ page }) => {
    tenant = await createTestTenant();
    await injectAuth(page.context(), tenant);
    await openCalendarWithRail(page);

    await page.getByRole('button', { name: copy.trigger }).click();
    await expect(page.getByRole('heading', { name: copy.holidaysTitle })).toBeVisible({
      timeout: 5_000,
    });

    // Type into the combobox — the country list filters by name.
    const countryPicker = page.getByRole('combobox', { name: copy.countryLabel });
    await countryPicker.click();
    await countryPicker.fill('Japan');
    // Pick the JP option from the listbox. Rendering format is
    // "<flag> Japan (JP)" so we match by visible text.
    await page.getByRole('option', { name: /Japan \(JP\)/ }).click();

    // Submit fires POST /workspaces/{wsId}/calendars/subscribe-system.
    await page.getByRole('button', { name: copy.subscribe }).click();

    // After success the rail returns to list mode (the trigger button
    // becomes visible again) and the new holiday calendar surfaces. The
    // backend names the calendar "Holidays · <country>" (en) — match by
    // the localised system-calendar prefix from the rail row title.
    await expect(page.getByRole('button', { name: copy.trigger })).toBeVisible({
      timeout: 10_000,
    });

    // Cross-check via REST: the workspace's calendar list now has a
    // system calendar with systemSlug "holidays.jp" (the slug format
    // emitted by holidaySlug() in flow-api: `"holidays." + strings.ToLower(country)`).
    const res = await fetch(`${API_BASE_URL}/workspaces/${tenant.workspaceId}/calendars`, {
      headers: {
        accept: 'application/json',
        authorization: `Bearer ${tenant.accessToken}`,
      },
    });
    expect(res.ok).toBeTruthy();
    const body = (await res.json()) as {
      calendars: Array<{ id: string; name: string; kind: string; systemSlug?: string }>;
    };
    const holidayCal = body.calendars.find((c) => c.systemSlug === 'holidays.jp');
    expect(holidayCal).toBeDefined();
    // The rail must surface that calendar's name in a row.
    if (!holidayCal) throw new Error('expected holiday calendar in workspace list');
    await expect(page.getByText(holidayCal.name).first()).toBeVisible({ timeout: 10_000 });
  });

  test('C: back arrow returns to list mode without subscribing', async ({ page }) => {
    tenant = await createTestTenant();
    await injectAuth(page.context(), tenant);
    await openCalendarWithRail(page);

    await page.getByRole('button', { name: copy.trigger }).click();
    await expect(page.getByRole('heading', { name: copy.holidaysTitle })).toBeVisible({
      timeout: 5_000,
    });

    // The back button carries the rail's title as its aria-label
    // (`calendars_rail.title` = "Calendars").
    const backButtons = page.getByRole('button', { name: copy.railTitle });
    // First match is the back-arrow inside the holidays section header.
    await backButtons.first().click();

    // List mode resumes — both the "Add teammate calendar..." and
    // "Subscribe holiday calendar..." triggers reappear.
    await expect(page.getByRole('button', { name: copy.trigger })).toBeVisible({ timeout: 5_000 });

    // The workspace must NOT have any system calendar — back is non-mutating.
    const res = await fetch(`${API_BASE_URL}/workspaces/${tenant.workspaceId}/calendars`, {
      headers: {
        accept: 'application/json',
        authorization: `Bearer ${tenant.accessToken}`,
      },
    });
    expect(res.ok).toBeTruthy();
    const body = (await res.json()) as {
      calendars: Array<{ id: string; systemSlug?: string }>;
    };
    expect(
      body.calendars.find((c) => (c.systemSlug ?? '').startsWith('holidays.')),
    ).toBeUndefined();
  });
});
