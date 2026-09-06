import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { type ReactElement, StrictMode, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';
import { getOverlayOpenCountForTests } from '../_overlay/overlay-lock';
import Dialog from './dialog';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

function Harness({ onClose }: { onClose?: () => void }) {
  const [open, setOpen] = useState(true);
  return (
    <Dialog
      open={open}
      onClose={() => {
        setOpen(false);
        onClose?.();
      }}
      title="Confirm"
    >
      <button type="button">OK</button>
    </Dialog>
  );
}

describe.each(THEMES)('Dialog [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
    document.getElementById('nf-portal-root')?.remove();
  });

  it('renders open dialog with no a11y violations', async () => {
    const { baseElement } = render(<Harness />);
    expect(screen.getByRole('dialog')).toBeDefined();
    expect(screen.getByRole('dialog').getAttribute('aria-modal')).toBe('true');
    expect(await axe(baseElement)).toHaveNoViolations();
  });

  it('traps focus on first interactive element', () => {
    render(<Harness />);
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'OK' }));
  });

  it('closes on Escape', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(<Harness onClose={fn} />);
    await user.keyboard('{Escape}');
    expect(fn).toHaveBeenCalled();
  });

  it('does not render when closed', () => {
    render(
      <Dialog open={false} onClose={() => {}} title="t">
        body
      </Dialog>,
    );
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('defaults to size="md" with data-size and sizeMd class', () => {
    render(<Harness />);
    const dialog = screen.getByRole('dialog');
    expect(dialog.getAttribute('data-size')).toBe('md');
    expect(dialog.className).toMatch(/sizeMd/);
    expect(dialog.className).not.toMatch(/sizeLg/);
    expect(dialog.className).not.toMatch(/sizeXl/);
  });

  it('applies size="lg" via data-size and sizeLg class', () => {
    render(
      <Dialog open={true} onClose={() => {}} title="t" size="lg">
        <button type="button">OK</button>
      </Dialog>,
    );
    const dialog = screen.getByRole('dialog');
    expect(dialog.getAttribute('data-size')).toBe('lg');
    expect(dialog.className).toMatch(/sizeLg/);
    expect(dialog.className).not.toMatch(/sizeMd/);
    expect(dialog.className).not.toMatch(/sizeXl/);
  });

  it('applies size="xl" via data-size and sizeXl class', () => {
    render(
      <Dialog open={true} onClose={() => {}} title="t" size="xl">
        <button type="button">OK</button>
      </Dialog>,
    );
    const dialog = screen.getByRole('dialog');
    expect(dialog.getAttribute('data-size')).toBe('xl');
    expect(dialog.className).toMatch(/sizeXl/);
    expect(dialog.className).not.toMatch(/sizeMd/);
    expect(dialog.className).not.toMatch(/sizeLg/);
  });

  it('closed dialog leaves baseElement axe-clean', async () => {
    const { baseElement } = render(
      <Dialog open={false} onClose={() => {}} title="t">
        <button type="button">OK</button>
      </Dialog>,
    );
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(await axe(baseElement)).toHaveNoViolations();
  });

  it('returns focus to the opener after close', async () => {
    function Opener(): ReactElement {
      const [open, setOpen] = useState(false);
      return (
        <>
          <button type="button" onClick={() => setOpen(true)}>
            open dialog
          </button>
          {open ? (
            <Dialog open={open} onClose={() => setOpen(false)} title="Confirm">
              <button type="button">OK</button>
            </Dialog>
          ) : null}
        </>
      );
    }

    const user = userEvent.setup();
    render(<Opener />);
    const opener = screen.getByRole('button', { name: 'open dialog' });
    opener.focus();
    expect(document.activeElement).toBe(opener);

    await user.click(opener);
    // While open, the trap moves focus to the first interactive element.
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'OK' }));

    await user.keyboard('{Escape}');
    // After close, useFocusTrap restores focus to the previously focused
    // element so keyboard users land back on the button that opened the
    // dialog.
    expect(document.activeElement).toBe(opener);
  });

  it('keeps Tab focus inside the dialog (trap cycles last -> first)', async () => {
    const user = userEvent.setup();
    render(
      <Dialog open={true} onClose={() => {}} title="Confirm">
        <button type="button">OK</button>
        <button type="button">Cancel</button>
      </Dialog>,
    );
    const ok = screen.getByRole('button', { name: 'OK' });
    const cancel = screen.getByRole('button', { name: 'Cancel' });
    // First focusable is OK after open.
    expect(document.activeElement).toBe(ok);
    await user.tab();
    expect(document.activeElement).toBe(cancel);
    // Tab from the last focusable cycles back to the first — focus never
    // escapes the dialog while it is open.
    await user.tab();
    expect(document.activeElement).toBe(ok);
    // Shift+Tab from the first focusable cycles to the last.
    await user.tab({ shift: true });
    expect(document.activeElement).toBe(cancel);
  });

  it('applies size="sm" via data-size and sizeSm class', () => {
    render(
      <Dialog open={true} onClose={() => {}} title="t" size="sm">
        <button type="button">OK</button>
      </Dialog>,
    );
    const dialog = screen.getByRole('dialog');
    expect(dialog.getAttribute('data-size')).toBe('sm');
    expect(dialog.className).toMatch(/sizeSm/);
    expect(dialog.className).not.toMatch(/sizeMd/);
  });

  it('uses fixed-size container variants so morphic dialogs do not reflow on kind switch', () => {
    // Memory rule: "Morphic UI must not reflow" — kind/mode-switching
    // dialogs need a fixed container size. The Dialog primitive enforces
    // this by exposing four size variants (sm/md/lg/xl), each mapped to a
    // single CSS-module class. Callers pick a size that accommodates the
    // largest variant and stick with it; the size MUST NOT change as a
    // function of inner content.
    for (const size of ['sm', 'md', 'lg', 'xl'] as const) {
      const { unmount } = render(
        <Dialog open={true} onClose={() => {}} title="t" size={size}>
          <button type="button">OK</button>
        </Dialog>,
      );
      const dialog = screen.getByRole('dialog');
      // The size token determines width, not the content. data-size is the
      // stable surface; sizeSm/sizeMd/sizeLg/sizeXl are the CSS-module classes.
      expect(dialog.getAttribute('data-size')).toBe(size);
      unmount();
    }
  });
});

describe('Dialog overlay lock + background inert', () => {
  let bgSibling: HTMLDivElement;

  beforeEach(() => {
    // Pre-existing background content that lives directly under <body>,
    // outside the portal. The lock must inert this on open and restore
    // it on close.
    bgSibling = document.createElement('div');
    bgSibling.id = 'bg-sibling';
    bgSibling.textContent = 'background';
    document.body.appendChild(bgSibling);
  });

  afterEach(() => {
    bgSibling.remove();
    document.getElementById('nf-portal-root')?.remove();
    // Defensive: if a test regression leaves the body locked, scrub state
    // so it does not leak across files.
    document.body.style.removeProperty('overflow');
    document.body.style.removeProperty('padding-inline-end');
    document.body.style.removeProperty('scrollbar-gutter');
    document.body.removeAttribute('data-nf-overlay-lock');
  });

  it('locks body scroll and inerts background siblings while open', () => {
    expect(getOverlayOpenCountForTests()).toBe(0);
    const { unmount } = render(
      <Dialog open onClose={() => {}} title="t">
        <button type="button">OK</button>
      </Dialog>,
    );
    expect(getOverlayOpenCountForTests()).toBe(1);
    expect(document.body.style.overflow).toBe('hidden');
    expect(document.body.getAttribute('data-nf-overlay-lock')).toBe('');
    expect(bgSibling.hasAttribute('inert')).toBe(true);
    expect(bgSibling.getAttribute('aria-hidden')).toBe('true');
    // Portal host itself stays interactive.
    const portal = document.getElementById('nf-portal-root');
    expect(portal).not.toBeNull();
    expect(portal?.hasAttribute('inert')).toBe(false);
    expect(portal?.getAttribute('aria-hidden')).toBeNull();
    unmount();
  });

  it('restores body scroll and background siblings when closed', () => {
    function Controlled({ open }: { open: boolean }) {
      return (
        <Dialog open={open} onClose={() => {}} title="t">
          <button type="button">OK</button>
        </Dialog>
      );
    }
    const { rerender } = render(<Controlled open={true} />);
    expect(document.body.style.overflow).toBe('hidden');
    expect(bgSibling.hasAttribute('inert')).toBe(true);

    rerender(<Controlled open={false} />);
    expect(getOverlayOpenCountForTests()).toBe(0);
    expect(document.body.style.overflow).toBe('');
    expect(document.body.getAttribute('data-nf-overlay-lock')).toBeNull();
    expect(bgSibling.hasAttribute('inert')).toBe(false);
    expect(bgSibling.getAttribute('aria-hidden')).toBeNull();
  });

  it('reference-counts stacked dialogs (open A, open B, close A keeps lock)', () => {
    function Stack({ aOpen, bOpen }: { aOpen: boolean; bOpen: boolean }) {
      return (
        <>
          <Dialog open={aOpen} onClose={() => {}} title="A">
            <button type="button">A-ok</button>
          </Dialog>
          <Dialog open={bOpen} onClose={() => {}} title="B">
            <button type="button">B-ok</button>
          </Dialog>
        </>
      );
    }

    const { rerender, unmount } = render(<Stack aOpen={true} bOpen={false} />);
    expect(getOverlayOpenCountForTests()).toBe(1);
    expect(document.body.style.overflow).toBe('hidden');
    expect(bgSibling.hasAttribute('inert')).toBe(true);

    rerender(<Stack aOpen={true} bOpen={true} />);
    expect(getOverlayOpenCountForTests()).toBe(2);
    expect(document.body.style.overflow).toBe('hidden');
    expect(bgSibling.hasAttribute('inert')).toBe(true);

    // Close the first dialog while the second remains open: lock and
    // inert MUST stay applied.
    rerender(<Stack aOpen={false} bOpen={true} />);
    expect(getOverlayOpenCountForTests()).toBe(1);
    expect(document.body.style.overflow).toBe('hidden');
    expect(document.body.getAttribute('data-nf-overlay-lock')).toBe('');
    expect(bgSibling.hasAttribute('inert')).toBe(true);
    expect(bgSibling.getAttribute('aria-hidden')).toBe('true');

    // Closing the last opener releases the lock.
    rerender(<Stack aOpen={false} bOpen={false} />);
    expect(getOverlayOpenCountForTests()).toBe(0);
    expect(document.body.style.overflow).toBe('');
    expect(document.body.getAttribute('data-nf-overlay-lock')).toBeNull();
    expect(bgSibling.hasAttribute('inert')).toBe(false);
    expect(bgSibling.getAttribute('aria-hidden')).toBeNull();
    unmount();
  });

  it('preserves user-set inert and aria-hidden on background siblings across the lock cycle', () => {
    bgSibling.setAttribute('inert', '');
    bgSibling.setAttribute('aria-hidden', 'true');

    const { unmount } = render(
      <Dialog open onClose={() => {}} title="t">
        <button type="button">OK</button>
      </Dialog>,
    );
    // Still inert (we did not strip it, just re-asserted).
    expect(bgSibling.hasAttribute('inert')).toBe(true);
    expect(bgSibling.getAttribute('aria-hidden')).toBe('true');
    unmount();
    // After release, the user's pre-existing values must remain.
    expect(bgSibling.hasAttribute('inert')).toBe(true);
    expect(bgSibling.getAttribute('aria-hidden')).toBe('true');
  });

  it('restores focus to the opener after close, even when the opener lives inside a background-inerted body sibling', async () => {
    // Reproduces the regression where useOverlayLock inerted the trigger's
    // ancestor before useFocusTrap captured `previouslyFocused`, leaving
    // `document.activeElement === <body>` after the dialog closed. The fix
    // captures the snapshot during render (before any layout effect runs)
    // and defers the restore via queueMicrotask (so the lock cleanup —
    // which runs in declaration order, BEFORE the trap cleanup — has time
    // to remove `inert` from the opener's ancestor first).
    function Opener(): ReactElement {
      const [open, setOpen] = useState(false);
      return (
        <>
          <button type="button" data-testid="trigger" onClick={() => setOpen(true)}>
            open dialog
          </button>
          {open ? (
            <Dialog open onClose={() => setOpen(false)} title="t">
              <button type="button">OK</button>
            </Dialog>
          ) : null}
        </>
      );
    }

    const user = userEvent.setup();
    render(<Opener />);
    const trigger = screen.getByTestId('trigger');
    trigger.focus();
    expect(document.activeElement).toBe(trigger);

    await user.click(trigger);
    // Dialog is now open; the lock has stamped `inert` on the trigger's
    // body-level ancestor. Focus has been moved into the dialog.
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'OK' }));

    await user.keyboard('{Escape}');
    // After close: lock released, inert removed, focus restored to trigger.
    // The microtask scheduled by useFocusTrap cleanup is flushed by the
    // `await` on userEvent.keyboard.
    expect(document.activeElement).toBe(trigger);
  });

  it('restores focus to the opener after close under StrictMode', async () => {
    // Both apps mount under StrictMode, where React runs an effect's
    // setup, cleanup, and setup again without re-rendering in between.
    // The trap's snapshot of the opener is taken during render, so it
    // cannot be re-taken for that second setup: a cleanup that cleared
    // the snapshot left the real close with nothing to focus and the
    // page sitting on `<body>`.
    function Opener(): ReactElement {
      const [open, setOpen] = useState(false);
      return (
        <>
          <button type="button" data-testid="trigger" onClick={() => setOpen(true)}>
            open dialog
          </button>
          {open ? (
            <Dialog open onClose={() => setOpen(false)} title="t">
              <button type="button">OK</button>
            </Dialog>
          ) : null}
        </>
      );
    }

    const user = userEvent.setup();
    render(
      <StrictMode>
        <Opener />
      </StrictMode>,
    );
    const trigger = screen.getByTestId('trigger');
    trigger.focus();

    await user.click(trigger);
    // The remount must not hand focus back to the opener either — the
    // dialog is open, so the trap owns focus.
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'OK' }));

    await user.keyboard('{Escape}');
    expect(document.activeElement).toBe(trigger);
  });

  it('restores focus to the inner dialog when an outer dialog stays open and inner closes', async () => {
    // Stacked overlays must restore focus correctly even when the
    // background remains inerted (because dialog A is still open). The
    // microtask deferral handles this: by the time it fires, the inner
    // dialog has unmounted and B's lock decrement still leaves the body
    // locked under A — but the previouslyFocused element captured for B
    // was inside A, which has never been inerted (it lives in the portal
    // root, which the lock explicitly skips).
    function Stack(): ReactElement {
      const [bOpen, setBOpen] = useState(false);
      return (
        <Dialog open onClose={() => {}} title="A">
          <button type="button" data-testid="open-b" onClick={() => setBOpen(true)}>
            open B
          </button>
          {bOpen ? (
            <Dialog open onClose={() => setBOpen(false)} title="B">
              <button type="button">B-ok</button>
            </Dialog>
          ) : null}
        </Dialog>
      );
    }

    const user = userEvent.setup();
    render(<Stack />);
    // Dialog A opens; first focusable inside A is "open B".
    const openB = screen.getByTestId('open-b');
    expect(document.activeElement).toBe(openB);

    await user.click(openB);
    // Dialog B opens; first focusable inside B is "B-ok".
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'B-ok' }));
    expect(getOverlayOpenCountForTests()).toBe(2);

    await user.keyboard('{Escape}');
    // B closed; focus must return to the opener inside A — NOT to body.
    expect(getOverlayOpenCountForTests()).toBe(1);
    expect(document.activeElement).toBe(openB);
  });
  /**
   * A `click` fires on the nearest common ancestor of pointerdown and
   * pointerup, so dragging a text selection out of a field and releasing
   * past the dialog's edge produced a click on the overlay. The dialog
   * dismissed and everything typed into it went with it. Selecting text
   * is how people edit, so this needed no unusual gesture at all.
   */
  it('does not dismiss when the gesture started inside the dialog', () => {
    const onClose = vi.fn();
    render(
      <Dialog open onClose={onClose} title="t">
        <input aria-label="field" defaultValue="typed" />
      </Dialog>,
    );
    const field = screen.getByLabelText('field');
    const overlay = field.closest('[class*="overlay"]');
    if (!overlay) throw new Error('overlay not found');

    fireEvent.pointerDown(field);
    fireEvent.click(overlay);
    expect(onClose).not.toHaveBeenCalled();
  });

  it('dismisses when the gesture both starts and ends on the overlay', () => {
    const onClose = vi.fn();
    render(
      <Dialog open onClose={onClose} title="t">
        <input aria-label="field" />
      </Dialog>,
    );
    const overlay = screen.getByLabelText('field').closest('[class*="overlay"]');
    if (!overlay) throw new Error('overlay not found');

    fireEvent.pointerDown(overlay);
    fireEvent.click(overlay);
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
