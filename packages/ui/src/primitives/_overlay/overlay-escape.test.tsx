/**
 * Escape must reach the top-most overlay only.
 *
 * Dialog and Drawer used to register one `document` keydown listener each
 * and call `stopPropagation()`. Sibling listeners on the same node are not
 * affected by that call, so every open overlay closed on a single Escape:
 * confirming something inside a drawer discarded the drawer's half-filled
 * form along with the confirmation.
 */

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { type ReactElement, useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Dialog from '../dialog/dialog';
import Drawer from '../drawer/drawer';
import { getOverlayOpenCountForTests } from './overlay-lock';

afterEach(() => {
  document.getElementById('nf-portal-root')?.remove();
  document.body.style.removeProperty('overflow');
  document.body.style.removeProperty('padding-inline-end');
  document.body.style.removeProperty('scrollbar-gutter');
  document.body.removeAttribute('data-nf-overlay-lock');
});

/** Drawer holding a form, with a confirmation dialog opened from inside it. */
function DrawerWithConfirm({
  onDrawerClose,
  confirmOpenInitially = false,
}: {
  onDrawerClose?: () => void;
  confirmOpenInitially?: boolean;
}): ReactElement {
  const [drawerOpen, setDrawerOpen] = useState(true);
  const [confirmOpen, setConfirmOpen] = useState(confirmOpenInitially);
  return (
    <Drawer
      open={drawerOpen}
      onClose={() => {
        setDrawerOpen(false);
        onDrawerClose?.();
      }}
      title="Edit task"
    >
      <input aria-label="notes" defaultValue="half-written" />
      <button type="button" data-testid="open-confirm" onClick={() => setConfirmOpen(true)}>
        delete
      </button>
      {confirmOpen ? (
        <Dialog open onClose={() => setConfirmOpen(false)} title="Are you sure?">
          <button type="button">confirm</button>
        </Dialog>
      ) : null}
    </Drawer>
  );
}

describe('overlay Escape stacking', () => {
  it('closes only the dialog stacked on a drawer, then the drawer', async () => {
    const user = userEvent.setup();
    const onDrawerClose = vi.fn();
    render(<DrawerWithConfirm onDrawerClose={onDrawerClose} />);

    await user.click(screen.getByTestId('open-confirm'));
    expect(getOverlayOpenCountForTests()).toBe(2);
    expect(screen.getByRole('dialog', { name: 'Are you sure?' })).toBeDefined();

    await user.keyboard('{Escape}');
    // The confirmation is gone; the drawer — and the text typed into it —
    // is still there.
    expect(screen.queryByRole('dialog', { name: 'Are you sure?' })).toBeNull();
    expect(onDrawerClose).not.toHaveBeenCalled();
    expect(screen.getByRole('dialog', { name: 'Edit task' })).toBeDefined();
    expect(screen.getByLabelText<HTMLInputElement>('notes').value).toBe('half-written');
    expect(getOverlayOpenCountForTests()).toBe(1);

    // Only now, with the drawer top-most, does Escape reach it.
    await user.keyboard('{Escape}');
    expect(onDrawerClose).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('dialog', { name: 'Edit task' })).toBeNull();
    expect(getOverlayOpenCountForTests()).toBe(0);
  });

  it('hands Escape to the overlay drawn on top when both mount at once', async () => {
    // Mounting the two together puts the drawer last in the portal host —
    // React runs the nested overlay's layout effect first, but the
    // parent's portal content is appended after it. The overlays share a
    // z-index, so last in the host is the one painted on top, and that is
    // the one Escape belongs to.
    const user = userEvent.setup();
    const onDrawerClose = vi.fn();
    render(<DrawerWithConfirm onDrawerClose={onDrawerClose} confirmOpenInitially />);

    const portalChildren = Array.from(document.getElementById('nf-portal-root')?.children ?? []);
    const last = portalChildren[portalChildren.length - 1];
    expect(last?.querySelector('[role="dialog"]')?.getAttribute('data-side')).toBe('inline-end');

    expect(getOverlayOpenCountForTests()).toBe(2);
    await user.keyboard('{Escape}');

    // Closing the drawer takes the dialog nested inside it along.
    expect(onDrawerClose).toHaveBeenCalledTimes(1);
    expect(getOverlayOpenCountForTests()).toBe(0);
  });

  it('gives Escape to the last-opened of two sibling dialogs', async () => {
    const user = userEvent.setup();
    const onCloseA = vi.fn();
    const onCloseB = vi.fn();

    function Siblings(): ReactElement {
      const [bOpen, setBOpen] = useState(false);
      return (
        <>
          <Dialog open onClose={onCloseA} title="A">
            <button type="button" data-testid="open-b" onClick={() => setBOpen(true)}>
              open B
            </button>
          </Dialog>
          <Dialog
            open={bOpen}
            onClose={() => {
              setBOpen(false);
              onCloseB();
            }}
            title="B"
          >
            <button type="button">B-ok</button>
          </Dialog>
        </>
      );
    }

    render(<Siblings />);
    await user.click(screen.getByTestId('open-b'));
    expect(getOverlayOpenCountForTests()).toBe(2);

    await user.keyboard('{Escape}');
    expect(onCloseB).toHaveBeenCalledTimes(1);
    expect(onCloseA).not.toHaveBeenCalled();

    await user.keyboard('{Escape}');
    expect(onCloseA).toHaveBeenCalledTimes(1);
  });
});
