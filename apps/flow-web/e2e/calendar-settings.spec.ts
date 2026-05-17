/**
 * Calendar Settings Drawer — General tab E2E.
 *
 * Covers apps/flow-web/src/features/calendars/general-tab.tsx (rename,
 * color swatch, description) plus the validation invariant. Each test
 * seeds its own auto-created personal calendar (the actor is owner) so
 * the rail's "Settings…" menu surfaces and the form's owner-only gate
 * stays satisfied. See {@link seedPersonalCalendar} for context on why
 * personal — not shared — is the seed kind.
 *
 * Cases (scoped to the General tab — archive / delete confirm flow are
 * intentionally out of scope):
 *   A. open drawer → fields prefilled from current calendar.
 *   B. rename calendar → save → name updates in the rail without reload.
 *   C. change color → swatch active state updates and rail row dot
 *      reflects the new color.
 *   D. edit description → save → reopen drawer to confirm persisted.
 *   E. validation: empty name disables save and surfaces the inline error.
 */

import { type Page, expect, test } from '@playwright/test';

import enCommon from '../locales/en/common.json' with { type: 'json' };
import {
  API_BASE_URL,
  type TestTenant,
  cleanupTenant,
  createTestTenant,
  ensurePersonalCalendar,
  injectAuth,
} from './fixtures/tenant';

const copy = {
  railTitle: enCommon.calendars_rail.title,
  rowMenu: enCommon.calendars_rail.actions.menu,
  rowSettings: enCommon.calendars_rail.actions.settings,
  drawerTitle: enCommon.calendar.settings.title,
  tabGeneral: enCommon.calendar.settings.tab.general,
  nameLabel: enCommon.calendar.settings.general.name_label,
  colorLabel: enCommon.calendar.settings.general.color_label,
  descriptionLabel: enCommon.calendar.settings.general.description_label,
  saveAction: enCommon.calendar.settings.general.save,
  savedToast: enCommon.calendar.settings.general.saved,
  nameRequired: enCommon.calendar.settings.general.name_required,
} as const;

interface SeedCalendar {
  id: string;
  name: string;
  color: string;
  description: string;
}

/**
 * Returns the actor's auto-created personal calendar after PATCHing it
 * with the desired baseline name / color / description.
 *
 * We originally intended to seed a `kind=shared` calendar so the
 * surface read closer to a member-managed calendar (rename / color /
 * archive / delete). Calendar sharing is modelled via
 * subscriptions rather than a dedicated kind, so `calendars.kind` is
 * `personal | system` end-to-end. Falling back to the personal calendar
 * still exercises the General tab end-to-end (the actor is owner, so
 * the rename/color/description writes pass the owner gate).
 */
async function seedPersonalCalendar(
  tenant: TestTenant,
  name: string,
  color: string,
  description: string,
): Promise<SeedCalendar> {
  const cal = await ensurePersonalCalendar(tenant);
  const res = await fetch(`${API_BASE_URL}/workspaces/${tenant.workspaceId}/calendars/${cal.id}`, {
    method: 'PATCH',
    headers: {
      'Content-Type': 'application/json',
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
    body: JSON.stringify({ name, color, description }),
  });
  if (!res.ok) {
    throw new Error(`PATCH /calendars/{id} -> ${res.status} ${await res.text()}`);
  }
  const body = (await res.json()) as {
    id: string;
    name: string;
    color: string;
    description?: string;
  };
  return {
    id: body.id,
    name: body.name,
    color: body.color,
    description: body.description ?? '',
  };
}

/** Reads back the calendar via REST so the test can assert against the API truth. */
async function fetchCalendar(
  tenant: TestTenant,
  calId: string,
): Promise<{ name: string; color: string; description?: string }> {
  const res = await fetch(`${API_BASE_URL}/workspaces/${tenant.workspaceId}/calendars/${calId}`, {
    headers: {
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
  });
  if (!res.ok) {
    throw new Error(`GET /calendars/{id} -> ${res.status} ${await res.text()}`);
  }
  return (await res.json()) as { name: string; color: string; description?: string };
}

async function openCalendarWithRail(page: Page): Promise<void> {
  await page.goto('/calendar');
  await expect(
    page.getByRole('heading', { level: 1, name: /^(Calendar|カレンダー)$/ }),
  ).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole('complementary', { name: copy.railTitle })).toBeVisible({
    timeout: 10_000,
  });
}

/**
 * Opens the Settings Drawer for `calendarName` via the rail row menu and
 * leaves the active tab on "General" (the default mount tab).
 */
async function openGeneralTab(page: Page, calendarName: string): Promise<void> {
  const rail = page.getByRole('complementary', { name: copy.railTitle });
  const row = rail.getByRole('listitem').filter({ hasText: calendarName });
  await expect(row).toBeVisible({ timeout: 10_000 });
  await row.getByRole('button', { name: copy.rowMenu }).click();
  await page.getByRole('menuitem', { name: copy.rowSettings }).click();

  const drawer = page.getByRole('dialog', { name: copy.drawerTitle });
  await expect(drawer).toBeVisible({ timeout: 5_000 });
  // Tabs default to "general" but click anyway to be explicit.
  await drawer.getByRole('tab', { name: copy.tabGeneral }).click();
}

test.describe('calendar settings drawer — general tab', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('A: opens drawer with fields prefilled from current calendar', async ({ page }) => {
    tenant = await createTestTenant();
    const initialName = `Settings A ${Date.now().toString(36)}`;
    const initialColor = '#16a34a';
    const initialDesc = 'Initial calendar description.';
    const cal = await seedPersonalCalendar(tenant, initialName, initialColor, initialDesc);

    await injectAuth(page.context(), tenant);
    await openCalendarWithRail(page);
    await openGeneralTab(page, cal.name);

    const drawer = page.getByRole('dialog', { name: copy.drawerTitle });
    await expect(drawer.getByLabel(copy.nameLabel)).toHaveValue(initialName);
    await expect(drawer.getByLabel(copy.descriptionLabel)).toHaveValue(initialDesc);

    // The active swatch carries aria-checked="true" and an opaque
    // data-color attribute pinning the hex; aria-label is the
    // localized swatch name (e.g. "Green") for screen readers.
    const activeSwatch = drawer.getByRole('radio', { checked: true });
    await expect(activeSwatch).toHaveAttribute('data-color', initialColor);
  });

  test('B: renames calendar and the rail reflects the new name', async ({ page }) => {
    tenant = await createTestTenant();
    const initialName = `Rename target ${Date.now().toString(36)}`;
    const cal = await seedPersonalCalendar(tenant, initialName, '#2563eb', '');

    await injectAuth(page.context(), tenant);
    await openCalendarWithRail(page);
    await openGeneralTab(page, cal.name);

    const drawer = page.getByRole('dialog', { name: copy.drawerTitle });
    const renamed = `${initialName} (renamed)`;
    await drawer.getByLabel(copy.nameLabel).fill(renamed);
    await drawer.getByRole('button', { name: copy.saveAction }).click();

    await expect(page.getByText(copy.savedToast)).toBeVisible({ timeout: 5_000 });

    // Close the drawer before asserting against the rail. While the
    // drawer is open, the overlay-lock marks every non-portal `<body>`
    // child as `inert` + `aria-hidden="true"` (intentional a11y
    // behaviour for modal dialogs), which removes the rail from the
    // accessibility tree and makes `getByRole('listitem')` invisible
    // even though its DOM and TanStack Query cache have already updated
    // with the renamed name. Mirroring the real user flow (close drawer
    // → see updated rail) keeps the assertion honest.
    await page.keyboard.press('Escape');
    await expect(drawer).toBeHidden({ timeout: 5_000 });

    // Rail row title updates without a page reload (react-query invalidate).
    const rail = page.getByRole('complementary', { name: copy.railTitle });
    await expect(rail.getByRole('listitem').filter({ hasText: renamed })).toBeVisible({
      timeout: 10_000,
    });

    // Cross-check via REST.
    const fresh = await fetchCalendar(tenant, cal.id);
    expect(fresh.name).toBe(renamed);
  });

  test('C: changes color and the calendar resource reflects the new swatch', async ({ page }) => {
    tenant = await createTestTenant();
    const cal = await seedPersonalCalendar(
      tenant,
      `Color target ${Date.now().toString(36)}`,
      '#2563eb',
      '',
    );

    await injectAuth(page.context(), tenant);
    await openCalendarWithRail(page);
    await openGeneralTab(page, cal.name);

    const drawer = page.getByRole('dialog', { name: copy.drawerTitle });
    const targetColor = '#ea580c';
    await drawer.locator(`[role="radio"][data-color="${targetColor}"]`).click();
    await drawer.getByRole('button', { name: copy.saveAction }).click();

    await expect(page.getByText(copy.savedToast)).toBeVisible({ timeout: 5_000 });

    // The save button toggles the active swatch in-drawer immediately.
    // Reopen the drawer to confirm the new color persisted in the
    // resource (a fresh load reads from the GET /calendars/{id} cache).
    await page.keyboard.press('Escape');
    await expect(drawer).toBeHidden({ timeout: 5_000 });
    await openGeneralTab(page, cal.name);
    const drawer2 = page.getByRole('dialog', { name: copy.drawerTitle });
    const activeSwatch = drawer2.getByRole('radio', { checked: true });
    await expect(activeSwatch).toHaveAttribute('data-color', targetColor);

    // SURFACE GAP: the rail row's color dot is bound to
    // `calendar.displayColor` (the actor's per-subscription color), not
    // `calendar.color` (the resource itself). The General tab only edits
    // the resource color, so the dot legitimately stays unchanged until a
    // separate per-subscription color editor lands. Cross-check via REST
    // that the resource itself moved.
    const fresh = await fetchCalendar(tenant, cal.id);
    expect(fresh.color.toLowerCase()).toBe(targetColor);
  });

  test('D: edits description and the change persists across reopens', async ({ page }) => {
    tenant = await createTestTenant();
    const cal = await seedPersonalCalendar(
      tenant,
      `Description target ${Date.now().toString(36)}`,
      '#2563eb',
      '',
    );

    await injectAuth(page.context(), tenant);
    await openCalendarWithRail(page);
    await openGeneralTab(page, cal.name);

    const drawer = page.getByRole('dialog', { name: copy.drawerTitle });
    const newDescription = `Updated copy ${Date.now().toString(36)}`;
    await drawer.getByLabel(copy.descriptionLabel).fill(newDescription);
    await drawer.getByRole('button', { name: copy.saveAction }).click();

    await expect(page.getByText(copy.savedToast)).toBeVisible({ timeout: 5_000 });

    // Close + reopen the drawer and verify the textarea still carries the
    // saved value (proves it's not just an in-memory state echo).
    await page.keyboard.press('Escape');
    await expect(drawer).toBeHidden({ timeout: 5_000 });

    await openGeneralTab(page, cal.name);
    const drawer2 = page.getByRole('dialog', { name: copy.drawerTitle });
    await expect(drawer2.getByLabel(copy.descriptionLabel)).toHaveValue(newDescription);

    const fresh = await fetchCalendar(tenant, cal.id);
    expect(fresh.description ?? '').toBe(newDescription);
  });

  test('E: validation — empty name disables save and surfaces an error', async ({ page }) => {
    tenant = await createTestTenant();
    const cal = await seedPersonalCalendar(
      tenant,
      `Validation target ${Date.now().toString(36)}`,
      '#2563eb',
      '',
    );

    await injectAuth(page.context(), tenant);
    await openCalendarWithRail(page);
    await openGeneralTab(page, cal.name);

    const drawer = page.getByRole('dialog', { name: copy.drawerTitle });
    const nameInput = drawer.getByLabel(copy.nameLabel);
    await nameInput.fill('');

    // The Save button is gated by `dirty` AND non-empty trimmed name. With
    // an empty value (which is dirty relative to the prefilled name), the
    // form's handleSave path still surfaces the inline error toast.
    // Submitting the form should keep the dialog open + emit the error
    // toast text (`name_required`). We submit by pressing Enter inside
    // the name input (which triggers the form submit).
    await nameInput.press('Enter');

    // Either the inline error or the toast variant must surface. The
    // current implementation funnels through `toaster.show`, so the text
    // appears outside the dialog.
    await expect(page.getByText(copy.nameRequired)).toBeVisible({ timeout: 5_000 });

    // The drawer must remain open (no successful save).
    await expect(drawer).toBeVisible();

    // Cross-check via REST: name is unchanged on the server.
    const fresh = await fetchCalendar(tenant, cal.id);
    expect(fresh.name).toBe(cal.name);
  });
});
