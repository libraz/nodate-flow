/**
 * Overlay stacking E2E.
 *
 * Documented behaviour (verified from source):
 *
 *   - `Dialog` (`packages/ui/src/primitives/dialog/dialog.tsx`) portals to
 *     `#nf-portal-root`, sets `aria-modal="true"` + `role="dialog"`, and
 *     traps focus via `useFocusTrap`.
 *   - On open, `useOverlayLock` records each non-portal `<body>` child
 *     and stamps it with `inert` + `aria-hidden="true"` +
 *     `data-nf-bg-inert`, then sets `<body data-nf-overlay-lock>`. A
 *     reference count means the lock is released only after the *last*
 *     overlay closes.
 *   - Esc handler in Dialog calls `event.stopPropagation()` so it
 *     dismisses only the innermost overlay.
 *   - `Popover` uses `FloatingPortal` from floating-ui and z-index 1400
 *     (`--nf-z-popover`) vs Dialog overlay's 1300 (`--nf-z-modal`), so it
 *     renders above the dialog overlay even when both are mounted.
 *   - `Toast` portals into a separate `#nf-toast-root` at z-index 1500.
 */

import { expect, type Locator, type Page, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { injectAuth, type TestTenant } from './fixtures/tenant';

/**
 * Authenticated bootstrap shared by every spec in this file. Lands the
 * page on the project's task board (which mounts the layout that owns
 * the create / quick-capture dialogs and listens for the open events)
 * and waits until the FAB toolbar is visible — by then the auth flow
 * has settled and the window event listeners are wired.
 */
async function bootstrap(page: Page, tenant: TestTenant): Promise<void> {
  await injectAuth(page.context(), tenant);
  await page.goto(`/projects/${tenant.projectId}/tasks`);
  await page.waitForSelector('[role="toolbar"]', { timeout: 15_000 });
}

async function openCreateDialog(page: Page): Promise<Locator> {
  await page.evaluate(() => {
    window.dispatchEvent(new Event('nf:open-create-task'));
  });
  const dialog = page.getByRole('dialog');
  await expect(dialog.first()).toBeVisible({ timeout: 10_000 });
  return dialog.first();
}

/**
 * Resolve the numeric z-index for the supplied locator by reading the
 * element's computed style and walking up to the nearest positioned
 * ancestor with a non-`auto` z-index. Useful because the Popover panel
 * itself carries `z-index: var(--nf-z-popover)` while the Dialog overlay
 * is the `.overlay` div — both are direct children of their respective
 * portals so a single getComputedStyle is enough.
 */
async function readZIndex(locator: Locator): Promise<number> {
  const handle = await locator.elementHandle();
  if (!handle) throw new Error('locator did not resolve to an element');
  const value = await handle.evaluate((el) => {
    const cs = getComputedStyle(el as Element);
    return cs.zIndex;
  });
  await handle.dispose();
  return Number(value);
}

test.describe('overlay stacking', () => {
  test('dialog locks body scroll and inerts background siblings', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await bootstrap(page, tenant);

    // Baseline: nothing locked, no inerted siblings.
    expect(await page.evaluate(() => document.body.hasAttribute('data-nf-overlay-lock'))).toBe(
      false,
    );
    expect(
      await page.evaluate(() => document.querySelectorAll('body > [data-nf-bg-inert]').length),
    ).toBe(0);

    await openCreateDialog(page);

    // Lock marker is stamped synchronously in useLayoutEffect.
    await expect
      .poll(() => page.evaluate(() => document.body.hasAttribute('data-nf-overlay-lock')))
      .toBe(true);

    // Every non-dialog-portal body child must have inert + aria-hidden +
    // the marker attribute. Only `#nf-portal-root` (the dialog portal
    // host) is exempt — see overlay-lock.ts which excludes that single
    // ID. The toast portal (`#nf-toast-root`), the React root div, and
    // anything else under <body> must all be inerted while the dialog
    // is open.
    const layout = await page.evaluate(() => {
      const DIALOG_PORTAL_ID = 'nf-portal-root';
      const children = Array.from(document.body.children);
      return children.map((c) => ({
        id: c.id || null,
        tag: c.tagName.toLowerCase(),
        hasInert: c.hasAttribute('inert'),
        hasAriaHidden: c.getAttribute('aria-hidden') === 'true',
        hasMarker: c.hasAttribute('data-nf-bg-inert'),
        isDialogPortal: c.id === DIALOG_PORTAL_ID,
      }));
    });
    for (const child of layout) {
      if (child.isDialogPortal) {
        expect(child.hasInert, `dialog portal ${child.id} must NOT be inert`).toBe(false);
        expect(child.hasMarker, `dialog portal ${child.id} must NOT have marker`).toBe(false);
      } else {
        expect(
          child.hasInert,
          `non-dialog-portal body child <${child.tag}#${child.id ?? ''}> must be inert while dialog is open`,
        ).toBe(true);
        expect(child.hasMarker, 'non-dialog-portal body child must carry data-nf-bg-inert').toBe(
          true,
        );
      }
    }
    // The product mounts at least the React root div — guard against an
    // empty match silently passing the loop.
    expect(layout.some((c) => !c.isDialogPortal)).toBe(true);

    // Body scroll is locked.
    const overflow = await page.evaluate(() => document.body.style.overflow);
    expect(overflow).toBe('hidden');

    // Close via Esc and verify everything is restored.
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).toBeHidden();

    await expect
      .poll(() => page.evaluate(() => document.body.hasAttribute('data-nf-overlay-lock')))
      .toBe(false);
    expect(
      await page.evaluate(() => document.querySelectorAll('body > [data-nf-bg-inert]').length),
    ).toBe(0);
    const restoredOverflow = await page.evaluate(() => document.body.style.overflow);
    expect(restoredOverflow).toBe('');
  });

  test('stacked dialogs: lock persists until last closes', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await bootstrap(page, tenant);

    // Open dialog A (full create dialog).
    await page.evaluate(() => {
      window.dispatchEvent(new Event('nf:open-create-task'));
    });
    // Open dialog B (quick capture). Both dialogs are mounted in
    // _authenticated.tsx with independent open state and listen for
    // distinct window events.
    await page.evaluate(() => {
      window.dispatchEvent(new Event('nf:open-quick-capture'));
    });

    // Two dialogs visible.
    await expect(page.getByRole('dialog')).toHaveCount(2, { timeout: 10_000 });
    expect(await page.evaluate(() => document.body.hasAttribute('data-nf-overlay-lock'))).toBe(
      true,
    );

    // Close the innermost (Esc closes the most recent — Quick Capture).
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).toHaveCount(1, { timeout: 5_000 });
    // Lock must STILL be held because dialog A is open.
    expect(await page.evaluate(() => document.body.hasAttribute('data-nf-overlay-lock'))).toBe(
      true,
    );

    // Close the remaining one — lock releases.
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).toHaveCount(0, { timeout: 5_000 });
    await expect
      .poll(() => page.evaluate(() => document.body.hasAttribute('data-nf-overlay-lock')))
      .toBe(false);
  });

  test('popover renders above the dialog overlay and inside the viewport', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await bootstrap(page, tenant);

    const dialog = await openCreateDialog(page);

    // Open the date picker popover by clicking its trigger button.
    // The DatePicker primitive renders a single <button> trigger inside
    // the dialog body whose accessible name is the placeholder text
    // ("Select a date" / "日付を選択") because no value is set yet.
    // Note: FormField's <label htmlFor> is not wired to the DatePicker
    // trigger (the date picker doesn't accept the FormField `control`
    // props), so `getByLabel` cannot find this control. We select by the
    // trigger's visible text instead.
    const dueField = dialog.getByRole('button', { name: /select a date|日付を選択/i });
    await dueField.click();

    // The popover renders into a FloatingPortal (separate from
    // #nf-portal-root). Identify it as the dialog with role=dialog NOT
    // marked aria-modal — Dialog sets aria-modal=true, Popover via
    // floating-ui useRole({role:'dialog'}) does not.
    const popover = page.locator('[role="dialog"]:not([aria-modal="true"])').first();
    await expect(popover).toBeVisible({ timeout: 5_000 });

    const overlayZ = await readZIndex(
      page
        .locator('html')
        .locator('div')
        .filter({
          has: page.locator('[role="dialog"][aria-modal="true"]'),
        })
        .first()
        .locator('xpath=ancestor-or-self::*')
        .first(),
    );
    // The dialog's overlay carries z-index var(--nf-z-modal)=1300; the
    // popover panel carries var(--nf-z-popover)=1400. Compare the panel
    // directly against the overlay div (the parent of the modal dialog).
    const dialogOverlayZ = await page.evaluate(() => {
      const modal = document.querySelector('[role="dialog"][aria-modal="true"]');
      if (!modal) return null;
      const overlay = modal.parentElement;
      if (!overlay) return null;
      return Number(getComputedStyle(overlay).zIndex);
    });
    const popoverZ = await readZIndex(popover);

    expect(dialogOverlayZ, 'dialog overlay z-index must resolve to a finite number').not.toBeNull();
    expect(popoverZ).toBeGreaterThan(dialogOverlayZ as number);
    // Sanity: the documented constants.
    expect(dialogOverlayZ).toBe(1300);
    expect(popoverZ).toBe(1400);
    void overlayZ;

    // Geometry sanity: popover is inside the viewport with non-zero size.
    const rect = await popover.evaluate((el) => {
      const r = (el as Element).getBoundingClientRect();
      return {
        w: r.width,
        h: r.height,
        top: r.top,
        left: r.left,
        bottom: r.bottom,
        right: r.right,
      };
    });
    expect(rect.w, 'popover width must be > 0').toBeGreaterThan(0);
    expect(rect.h, 'popover height must be > 0').toBeGreaterThan(0);
    const viewport = page.viewportSize();
    if (viewport) {
      expect(rect.right).toBeLessThanOrEqual(viewport.width);
      expect(rect.bottom).toBeLessThanOrEqual(viewport.height);
      expect(rect.left).toBeGreaterThanOrEqual(0);
      expect(rect.top).toBeGreaterThanOrEqual(0);
    }
  });

  test('Esc closes innermost overlay only (popover before dialog)', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await bootstrap(page, tenant);

    const dialog = await openCreateDialog(page);
    // See sibling test above: target the DatePicker trigger by visible
    // text rather than `getByLabel` because FormField's htmlFor wiring
    // does not reach the DatePicker trigger button.
    const dueField = dialog.getByRole('button', { name: /select a date|日付を選択/i });
    await dueField.click();

    const popover = page.locator('[role="dialog"]:not([aria-modal="true"])').first();
    await expect(popover).toBeVisible({ timeout: 5_000 });

    // Esc dismisses the popover (floating-ui useDismiss handles it and
    // the dialog's keydown handler stops propagation only AFTER the
    // popover already consumed the event because the popover panel is
    // higher up the focus tree).
    await page.keyboard.press('Escape');
    await expect(popover).toBeHidden({ timeout: 5_000 });
    // The modal dialog must still be visible.
    await expect(
      page.getByRole('dialog', { name: /new task|新しいタスク|タスクの追加|タスクを追加/i }),
    ).toBeVisible();

    // A second Esc closes the dialog itself.
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).toBeHidden({ timeout: 5_000 });
  });

  test('focus trap cycles within the dialog', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await bootstrap(page, tenant);

    const dialog = await openCreateDialog(page);

    // The container the trap watches is the role=dialog node. Enumerate
    // its currently-focusable children using the same selector list as
    // useFocusTrap. We Tab once per item and assert focus stays within
    // the dialog the whole way, then wraps to the first element.
    const focusableSelector = [
      'a[href]',
      'area[href]',
      'button:not([disabled])',
      'input:not([disabled]):not([type="hidden"])',
      'select:not([disabled])',
      'textarea:not([disabled])',
      '[tabindex]:not([tabindex="-1"])',
      '[contenteditable="true"]',
    ].join(',');

    const count = await dialog.evaluate(
      (el, sel) => el.querySelectorAll(sel as string).length,
      focusableSelector,
    );
    expect(count, 'dialog must expose at least two focusable children').toBeGreaterThan(1);

    // The trap focuses the first element on open. Walk forward `count`
    // times — the last Tab should wrap us to the first element again.
    const firstTagBefore = await page.evaluate(() => {
      const a = document.activeElement as HTMLElement | null;
      return a ? `${a.tagName}#${a.id}.${a.className}` : null;
    });
    expect(firstTagBefore, 'first focusable must be focused on open').not.toBeNull();

    for (let i = 0; i < count; i++) {
      await page.keyboard.press('Tab');
      const inside = await page.evaluate(() => {
        const a = document.activeElement;
        const dialog = document.querySelector('[role="dialog"][aria-modal="true"]');
        return !!(a && dialog && dialog.contains(a));
      });
      expect(inside, `focus must remain inside dialog after Tab #${i + 1}`).toBe(true);
    }

    // After `count` Tabs we should be on the first element again
    // (cycled). Compare a stable identity (tag + id + className) since
    // multiple buttons share the same shape.
    const firstTagAfter = await page.evaluate(() => {
      const a = document.activeElement as HTMLElement | null;
      return a ? `${a.tagName}#${a.id}.${a.className}` : null;
    });
    expect(firstTagAfter).toBe(firstTagBefore);
  });

  // Pin the corrected focus-restoration behaviour. Previous regression:
  // `useOverlayLock`'s layout effect ran before `useFocusTrap` captured
  // `previouslyFocused`, so the trigger's ancestor was inerted first, the
  // browser redirected focus to `<body>`, and the snapshot remembered
  // `<body>`. The fix in `packages/ui/src/hooks/use-focus-trap.ts`
  // captures the snapshot during render-phase (before any layout effect
  // runs) and defers the restore via `queueMicrotask` (so the lock has
  // released `inert` by the time `focus()` fires).
  test('focus is restored to the trigger when the dialog closes', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await bootstrap(page, tenant);

    // The glass dock toolbar has a button that opens the create dialog.
    // Use its accessible name to stay locale-aware.
    const trigger = page
      .getByRole('toolbar')
      .getByRole('button', { name: /new task|新規タスク|タスク追加|タスクを追加/i })
      .first();
    await expect(trigger).toBeVisible({ timeout: 10_000 });
    await trigger.focus();
    await expect(trigger).toBeFocused();
    // Stamp the element so we can identify it after the dialog closes.
    await trigger.evaluate((el) => {
      el.setAttribute('data-e2e-focus-target', '1');
    });

    await trigger.click();
    await expect(page.getByRole('dialog')).toBeVisible({ timeout: 10_000 });

    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).toBeHidden({ timeout: 5_000 });

    // useFocusTrap restores focus to the previously focused element on
    // teardown. Verify by checking that the active element carries our
    // marker.
    const restored = await page.evaluate(() =>
      document.activeElement?.getAttribute('data-e2e-focus-target'),
    );
    expect(restored, 'focus must return to the trigger on close').toBe('1');
  });
});
