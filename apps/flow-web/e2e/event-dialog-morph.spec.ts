/**
 * Event-dialog morph stability E2E.
 *
 * The unified calendar EventDialog renders a single SegmentedControl
 * across the kinds {task, event, block, free, milestone}, and beneath
 * it morphs in a kind-specific section that has to fade in over a
 * stable footprint — otherwise the dialog and the picker visibly
 * twitch as the user samples each kind.
 *
 * Two regressions this spec defends against:
 *
 * 1. **Picker width drift.** When SegmentedControl is rendered with
 *    `fullWidth`, every segment must take an equal share of the
 *    inline size (`flex: 1 1 0` + `min-inline-size: 0`). Without that,
 *    the longer Japanese label "マイルストーン" (7 chars) would push
 *    its segment wider than the 2-char "タスク" segment and leave the
 *    row visibly uneven. We assert each segment's bounding box has
 *    the same width.
 *
 * 2. **Morph-zone reflow.** The `<EventDialog>` body has a fixed
 *    block-size (`min(36rem, 75vh)`) but the *inner* morph zone has a
 *    `min-block-size` floor matching the tallest variant so the
 *    dialog does not visually reflow as the kind switches. We assert
 *    the dialog's bounding box and the picker's bounding box are
 *    byte-for-byte stable across kind switches.
 */

import { expect, type Page, test } from '@playwright/test';

import enCal from '../locales/en/calendar-events.json' with { type: 'json' };
import {
  cleanupTenant,
  createTestTenant,
  ensurePersonalCalendar,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';

const copy = {
  kindTask: enCal.kind.task,
  kindEvent: enCal.kind.event,
  kindBlock: enCal.kind.block,
  kindFree: enCal.kind.free,
  kindMilestone: enCal.kind.milestone,
} as const;

/**
 * Open the EventDialog by clicking the top-left of today's grid cell.
 * Mirrors the helper in calendar-event-dialog.spec.ts but kept inline
 * here so this spec does not depend on its sibling's private helpers.
 */
async function openCreateDialog(page: Page): Promise<void> {
  await page.goto('/calendar');
  await expect(
    page.getByRole('heading', { level: 1, name: /^(Calendar|カレンダー)$/ }),
  ).toBeVisible({ timeout: 15_000 });
  const today = new Date();
  const day = today.getDate();
  const cells = page.locator('[role="button"][title]').filter({
    has: page.getByText(String(day), { exact: true }),
  });
  const cell = cells.first();
  await expect(cell).toBeVisible({ timeout: 10_000 });
  await cell.click({ position: { x: 8, y: 8 } });
  await expect(page.getByRole('dialog')).toBeVisible({ timeout: 5_000 });
}

interface Box {
  width: number;
  height: number;
}

async function snap(page: Page, role: 'dialog' | 'radiogroup'): Promise<Box> {
  // We snapshot the bounding box of the named landmark so test
  // assertions are robust against zoom / device-pixel-ratio drift.
  const handle = page.getByRole(role).first();
  const box = await handle.boundingBox();
  if (!box) throw new Error(`${role} has no bounding box`);
  return { width: box.width, height: box.height };
}

test.describe('event-dialog morph stability', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('dialog + picker keep the same bounding box across kind switches', async ({ page }) => {
    tenant = await createTestTenant();
    await ensurePersonalCalendar(tenant);

    await injectAuth(page.context(), tenant);
    await openCreateDialog(page);

    const dialog = page.getByRole('dialog');
    const picker = dialog.getByRole('radiogroup', { name: /kind/i }).first();

    // Default kind is event; let the morph animation settle once before
    // we record the baseline so the very first measurement is stable.
    await expect(dialog.getByRole('radio', { name: copy.kindEvent })).toHaveAttribute(
      'aria-checked',
      'true',
    );
    await page.waitForTimeout(250);

    const baselineDialog = await snap(page, 'dialog');
    const baselinePicker = await snap(page, 'radiogroup');

    // Picker width-stability: every segment is equal-width with
    // `fullWidth`. Pull the bounding box for each radio and assert
    // they all match within 1px (sub-pixel rounding tolerance).
    const radios = picker.getByRole('radio');
    const widths: number[] = [];
    const count = await radios.count();
    for (let i = 0; i < count; i += 1) {
      const radio = radios.nth(i);
      const box = await radio.boundingBox();
      if (!box) throw new Error(`segment ${i} has no bounding box`);
      widths.push(box.width);
    }
    const maxW = Math.max(...widths);
    const minW = Math.min(...widths);
    expect(maxW - minW).toBeLessThanOrEqual(1);

    // Walk the kind picker and assert the dialog + picker bounding
    // boxes do not change as variants morph in/out. We allow 1px of
    // slack to absorb sub-pixel rounding; anything larger is a real
    // reflow.
    for (const kindLabel of [
      copy.kindBlock,
      copy.kindFree,
      copy.kindMilestone,
      copy.kindTask,
      copy.kindEvent,
    ]) {
      await dialog.getByRole('radio', { name: kindLabel }).click();
      // Allow the 180 ms fade to settle before measuring.
      await page.waitForTimeout(220);

      const after = await snap(page, 'dialog');
      expect(Math.abs(after.width - baselineDialog.width)).toBeLessThanOrEqual(1);
      expect(Math.abs(after.height - baselineDialog.height)).toBeLessThanOrEqual(1);

      const afterPicker = await snap(page, 'radiogroup');
      expect(Math.abs(afterPicker.width - baselinePicker.width)).toBeLessThanOrEqual(1);
      expect(Math.abs(afterPicker.height - baselinePicker.height)).toBeLessThanOrEqual(1);
    }
  });
});
