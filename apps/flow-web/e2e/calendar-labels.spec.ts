/**
 * Calendar labels E2E.
 *
 * Covers the Labels tab and Members tab of the calendar settings drawer
 * (features/calendars/{labels-tab,member-color-popover}.tsx) together with
 * the label picker in the event dialog and the colour an event pill
 * resolves to.
 *
 * Flow: create a label in the drawer -> assign it in the event dialog ->
 * the pill's `--evt` becomes the label colour and its border changes ->
 * change the member colour -> delete the label -> the pill falls back to
 * the new owner colour.
 *
 * The pill colour is asserted through the inline `--evt` custom property
 * (an exact hex) plus a before/after comparison of the computed border
 * colour. The border itself is a `color-mix(in oklch, ...)` result, so its
 * serialised form is browser-specific and is never compared to a literal.
 */

import { expect, type Locator, type Page, test } from '@playwright/test';

import enCal from '../locales/en/calendar-events.json' with { type: 'json' };
import enCommon from '../locales/en/common.json' with { type: 'json' };
import {
  cleanupTenant,
  createCalendarEvent,
  createTestTenant,
  ensurePersonalCalendar,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';

const copy = {
  railTitle: enCommon.calendars_rail.title,
  rowMenu: enCommon.calendars_rail.actions.menu,
  rowSettings: enCommon.calendars_rail.actions.settings,
  drawerTitle: enCommon.calendar.settings.title,
  tabLabels: enCommon.calendar.settings.tab.labels,
  tabMembers: enCommon.calendar.settings.tab.members,
  colorChange: enCommon.calendar.settings.members.color_change,
  addLabel: enCommon.calendar.labels.add,
  nameField: enCommon.calendar.labels.name,
  customColor: enCommon.calendar.labels.custom_color,
  customColorInvalid: enCommon.calendar.labels.custom_color_invalid,
  swatch: enCommon.calendar.labels.swatch,
  save: enCommon.calendar.labels.save,
  deleteLabel: enCommon.calendar.labels.delete,
  deleteTitle: enCommon.calendar.labels.delete_title,
  deleteConfirm: enCommon.calendar.labels.delete_confirm,
  fieldLabel: enCal.field.label,
  fieldLabelNone: enCal.field.label_none,
  editSubmit: enCal.action.submit.edit,
  toastEventUpdated: enCal.toast.updated.event,
} as const;

/** Label colour: a "dark" row swatch, so it differs from every default owner colour. */
const LABEL_COLOR = '#2273C9';
/** Candidate member colours (member palette); one differing from the current is used. */
const MEMBER_COLOR_CANDIDATES = ['#FF7043', '#2ECC87'] as const;

/** Replaces the single `{name}` placeholder of a locale string. */
function fill(template: string, name: string): string {
  return template.replace('{name}', name);
}

/** Accessible name of a colour swatch. */
function swatchName(hex: string): string {
  return copy.swatch.replace('{hex}', hex);
}

function todayAtUnix(hhmm: string): number {
  const d = new Date();
  const [h, m] = hhmm.split(':').map(Number);
  d.setHours(h ?? 0, m ?? 0, 0, 0);
  return Math.floor(d.getTime() / 1000);
}

async function openCalendar(page: Page): Promise<void> {
  await page.goto('/calendar');
  await expect(
    page.getByRole('heading', { level: 1, name: /^(Calendar|カレンダー)$/ }),
  ).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole('complementary', { name: copy.railTitle })).toBeVisible({
    timeout: 10_000,
  });
}

/** Opens the settings drawer for a calendar and switches to the given tab. */
async function openDrawerTab(page: Page, calendarName: string, tabName: string): Promise<Locator> {
  const rail = page.getByRole('complementary', { name: copy.railTitle });
  const row = rail.getByRole('listitem').filter({ hasText: calendarName });
  await expect(row).toBeVisible({ timeout: 10_000 });
  await row.getByRole('button', { name: copy.rowMenu }).click();
  await page.getByRole('menuitem', { name: copy.rowSettings }).click();

  const drawer = page.getByRole('dialog', { name: copy.drawerTitle });
  await expect(drawer).toBeVisible({ timeout: 5_000 });
  await drawer.getByRole('tab', { name: tabName }).click();
  return drawer;
}

/** Closes the drawer; while it is open the rest of the page is inert. */
async function closeDrawer(page: Page, drawer: Locator): Promise<void> {
  await page.keyboard.press('Escape');
  await expect(drawer).toBeHidden({ timeout: 5_000 });
}

/**
 * Pill for an event title. The grid cell's accessible name also contains
 * the pill's aria-label, so pin the real pill by tag + attribute.
 */
function eventPill(page: Page, title: string): Locator {
  return page.locator(`button[aria-label^="Open event: "][aria-label*=${JSON.stringify(title)}]`);
}

/** The pill's inline `--evt` custom property, lower-cased. */
async function pillColor(pill: Locator): Promise<string> {
  return pill.evaluate((el) =>
    (el as HTMLElement).style.getPropertyValue('--evt').trim().toLowerCase(),
  );
}

async function pillBorderColor(pill: Locator): Promise<string> {
  return pill.evaluate((el) => getComputedStyle(el).borderTopColor);
}

test.describe('calendar labels', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('label colours the pill, member colour and label deletion fall back to the owner colour', async ({
    page,
  }) => {
    const suffix = Date.now().toString(36);
    const eventTitle = `Labelled ${suffix}`;
    const labelName = `Client ${suffix}`;

    tenant = await createTestTenant();
    const cal = await ensurePersonalCalendar(tenant);
    await createCalendarEvent(tenant, cal.id, {
      title: eventTitle,
      startAt: todayAtUnix('14:00'),
      endAt: todayAtUnix('15:00'),
      kind: 'event',
    });

    await injectAuth(page.context(), tenant);
    await openCalendar(page);

    // Baseline: an unlabelled pill shows the owner colour.
    const pill = eventPill(page, eventTitle);
    await expect(pill).toBeVisible({ timeout: 15_000 });
    const ownerColor = await pillColor(pill);
    expect(ownerColor).toMatch(/^#[0-9a-f]{6}$/);
    expect(ownerColor).not.toBe(LABEL_COLOR.toLowerCase());
    const ownerBorder = await pillBorderColor(pill);

    // 1. Create the label in the drawer.
    let drawer = await openDrawerTab(page, cal.name, copy.tabLabels);
    await drawer.getByRole('button', { name: copy.addLabel }).click();
    await drawer.getByLabel(copy.nameField).fill(labelName);
    await drawer.getByRole('radio', { name: swatchName(LABEL_COLOR) }).click();
    await drawer.getByRole('button', { name: copy.save, exact: true }).click();
    await expect(drawer.getByText(labelName)).toBeVisible({ timeout: 10_000 });
    await closeDrawer(page, drawer);

    // 2. Assign it in the event dialog.
    await pill.click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible({ timeout: 5_000 });
    const picker = dialog.getByRole('radiogroup', { name: copy.fieldLabel });
    await expect(picker.getByRole('radio', { name: copy.fieldLabelNone })).toBeChecked();
    await picker.getByRole('radio', { name: labelName }).click();
    await expect(picker.getByRole('radio', { name: labelName })).toBeChecked();
    await dialog.getByRole('button', { name: copy.editSubmit }).click();
    await expect(dialog).toBeHidden({ timeout: 10_000 });
    await expect(page.getByText(copy.toastEventUpdated)).toBeVisible({ timeout: 5_000 });

    // 3. The pill now carries the label colour and name.
    const labelled = eventPill(page, eventTitle);
    await expect(labelled).toHaveAttribute('aria-label', new RegExp(`\\(${labelName}\\)`));
    expect(await pillColor(labelled)).toBe(LABEL_COLOR.toLowerCase());
    expect(await pillBorderColor(labelled)).not.toBe(ownerBorder);

    // 4. Change the member colour; the labelled pill is unaffected.
    const newOwnerColor =
      MEMBER_COLOR_CANDIDATES.find((c) => c.toLowerCase() !== ownerColor) ??
      MEMBER_COLOR_CANDIDATES[0];
    drawer = await openDrawerTab(page, cal.name, copy.tabMembers);
    const memberRow = drawer.getByRole('listitem').filter({ hasText: tenant.displayName });
    await memberRow
      .getByRole('button', { name: fill(copy.colorChange, tenant.displayName) })
      .click();
    await page.getByRole('button', { name: swatchName(newOwnerColor) }).click();
    // Saved optimistically: the circle button's own colour is not reachable
    // by role, so wait for the popover swatch to leave before moving on.
    await expect(
      page.getByRole('button', {
        name: swatchName(newOwnerColor),
      }),
    ).toBeHidden({ timeout: 5_000 });
    await closeDrawer(page, drawer);
    expect(await pillColor(eventPill(page, eventTitle))).toBe(LABEL_COLOR.toLowerCase());

    // 5. Delete the label; the pill falls back to the new owner colour.
    drawer = await openDrawerTab(page, cal.name, copy.tabLabels);
    await drawer.getByRole('button', { name: fill(copy.deleteLabel, labelName) }).click();
    const confirm = page
      .getByRole('dialog')
      .filter({ hasText: fill(copy.deleteTitle, labelName) })
      .last();
    await confirm.getByRole('button', { name: copy.deleteConfirm }).click();
    await expect(drawer.getByText(labelName)).toHaveCount(0, { timeout: 10_000 });
    await closeDrawer(page, drawer);

    const fallback = eventPill(page, eventTitle);
    await expect
      .poll(() => pillColor(fallback), { timeout: 10_000 })
      .toBe(newOwnerColor.toLowerCase());
    await expect(fallback).not.toHaveAttribute('aria-label', new RegExp(`\\(${labelName}\\)`));
  });

  test('an invalid custom colour disables saving the label', async ({ page }) => {
    tenant = await createTestTenant();
    const cal = await ensurePersonalCalendar(tenant);

    await injectAuth(page.context(), tenant);
    await openCalendar(page);

    const drawer = await openDrawerTab(page, cal.name, copy.tabLabels);
    await drawer.getByRole('button', { name: copy.addLabel }).click();
    await drawer.getByLabel(copy.nameField).fill('Bad colour');
    await drawer.getByLabel(copy.customColor).fill('#12');

    await expect(drawer.getByText(copy.customColorInvalid)).toBeVisible();
    await expect(drawer.getByRole('button', { name: copy.save, exact: true })).toBeDisabled();
  });
});
