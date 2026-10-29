/**
 * A closing overlay stays mounted while its exit animation runs, and in
 * that window it must already behave as closed: no Escape, no focus trap,
 * no pointer or AT reach. Test DOMs compute no animation, so the browser's
 * computed style is stubbed for every element carrying `data-state`.
 */

import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { type ReactElement, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import Dialog from '../dialog/dialog';
import Drawer from '../drawer/drawer';
import Popover from '../popover/popover';
import { getOverlayOpenCountForTests } from './overlay-lock';

beforeEach(() => {
  const real = window.getComputedStyle.bind(window);
  vi.spyOn(window, 'getComputedStyle').mockImplementation((el, pseudo) => {
    const style = real(el, pseudo);
    if (!(el instanceof HTMLElement) || el.dataset.state !== 'closed') return style;
    return {
      ...style,
      animationName: 'nf-fade-out',
      animationDuration: '120ms',
      animationDelay: '0s',
    } as CSSStyleDeclaration;
  });
});

afterEach(() => {
  vi.restoreAllMocks();
  document.getElementById('nf-portal-root')?.remove();
  document.body.style.removeProperty('overflow');
  document.body.style.removeProperty('padding-inline-end');
  document.body.style.removeProperty('scrollbar-gutter');
  document.body.removeAttribute('data-nf-overlay-lock');
});

function DrawerWithConfirm({ onDrawerClose }: { onDrawerClose: () => void }): ReactElement {
  const [drawerOpen, setDrawerOpen] = useState(true);
  const [confirmOpen, setConfirmOpen] = useState(false);
  return (
    <Drawer
      open={drawerOpen}
      onClose={() => {
        setDrawerOpen(false);
        onDrawerClose();
      }}
      title="Edit task"
    >
      <button type="button" data-testid="open-confirm" onClick={() => setConfirmOpen(true)}>
        delete
      </button>
      <Dialog open={confirmOpen} onClose={() => setConfirmOpen(false)} title="Are you sure?">
        <button type="button">confirm</button>
      </Dialog>
    </Drawer>
  );
}

/** The overlay element that carries the dialog's presence state. */
function overlayOf(dialogName: string): HTMLElement {
  const overlay = screen.getByRole('dialog', { name: dialogName }).parentElement;
  if (!overlay) throw new Error(`no overlay for ${dialogName}`);
  return overlay;
}

describe('overlay exit phase', () => {
  it('a closing dialog is inert, out of the stack and not the Escape target', async () => {
    const user = userEvent.setup();
    const onDrawerClose = vi.fn();
    render(<DrawerWithConfirm onDrawerClose={onDrawerClose} />);

    const opener = screen.getByTestId('open-confirm');
    await user.click(opener);
    const closing = overlayOf('Are you sure?');
    await user.keyboard('{Escape}');

    // Still on screen for its exit animation, but already closed.
    expect(closing.isConnected).toBe(true);
    expect(closing.dataset.state).toBe('closed');
    expect(closing.hasAttribute('inert')).toBe(true);
    expect(closing.getAttribute('aria-hidden')).toBe('true');
    expect(screen.queryAllByRole('dialog')).toHaveLength(1);
    expect(getOverlayOpenCountForTests()).toBe(1);
    expect(document.activeElement).toBe(opener);

    // The next Escape belongs to the drawer, not the dialog on its way out.
    await user.keyboard('{Escape}');
    expect(onDrawerClose).toHaveBeenCalledTimes(1);
    expect(getOverlayOpenCountForTests()).toBe(0);
  });

  it('unmounts the closing dialog on its own animationend', async () => {
    const user = userEvent.setup();
    render(<DrawerWithConfirm onDrawerClose={vi.fn()} />);

    await user.click(screen.getByTestId('open-confirm'));
    const closing = overlayOf('Are you sure?');
    await user.keyboard('{Escape}');
    expect(closing.isConnected).toBe(true);

    fireEvent.animationEnd(closing);
    expect(closing.isConnected).toBe(false);
    expect(screen.getByRole('dialog', { name: 'Edit task' })).toBeDefined();
  });

  it('a closing popover returns focus to its trigger before it unmounts', async () => {
    const user = userEvent.setup();
    render(
      <Popover content={<button type="button">inside</button>} ariaLabel="Filters">
        <button type="button">open</button>
      </Popover>,
    );

    const trigger = screen.getByRole('button', { name: 'open' });
    await user.click(trigger);
    const panel = screen.getByRole('dialog', { name: 'Filters' });
    await user.keyboard('{Escape}');

    expect(panel.isConnected).toBe(true);
    expect(panel.dataset.state).toBe('closed');
    expect(panel.hasAttribute('inert')).toBe(true);
    expect(panel.getAttribute('aria-hidden')).toBe('true');
    await vi.waitFor(() => expect(document.activeElement).toBe(trigger));

    fireEvent.animationEnd(panel);
    expect(panel.isConnected).toBe(false);
  });
});
