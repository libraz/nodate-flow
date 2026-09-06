import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { StrictMode, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';
import { getOverlayOpenCountForTests } from '../_overlay/overlay-lock';
import Drawer, { type DrawerSide } from './drawer';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

function Harness({ side, onClose }: { side?: DrawerSide; onClose?: () => void }) {
  const [open, setOpen] = useState(true);
  return (
    <Drawer
      open={open}
      side={side ?? 'inline-end'}
      onClose={() => {
        setOpen(false);
        onClose?.();
      }}
      title="Filters"
    >
      <button type="button">Apply</button>
    </Drawer>
  );
}

describe.each(THEMES)('Drawer [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
    document.getElementById('nf-portal-root')?.remove();
  });

  it('renders open drawer with no a11y violations', async () => {
    const { baseElement } = render(<Harness />);
    expect(screen.getByRole('dialog')).toBeDefined();
    expect(await axe(baseElement)).toHaveNoViolations();
  });

  it('supports all four logical sides', () => {
    const sides: DrawerSide[] = ['inline-start', 'inline-end', 'block-start', 'block-end'];
    for (const side of sides) {
      const { unmount } = render(<Harness side={side} />);
      expect(screen.getByRole('dialog').getAttribute('data-side')).toBe(side);
      unmount();
      document.getElementById('nf-portal-root')?.remove();
    }
  });

  it('closes on Escape', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(<Harness onClose={fn} />);
    await user.keyboard('{Escape}');
    expect(fn).toHaveBeenCalled();
  });

  it('traps focus on first interactive element', () => {
    render(<Harness />);
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Apply' }));
  });
});

describe('Drawer overlay lock + background inert', () => {
  let bgSibling: HTMLDivElement;

  beforeEach(() => {
    bgSibling = document.createElement('div');
    bgSibling.id = 'bg-sibling-drawer';
    bgSibling.textContent = 'background';
    document.body.appendChild(bgSibling);
  });

  afterEach(() => {
    bgSibling.remove();
    document.getElementById('nf-portal-root')?.remove();
    document.body.style.removeProperty('overflow');
    document.body.style.removeProperty('padding-inline-end');
    document.body.style.removeProperty('scrollbar-gutter');
    document.body.removeAttribute('data-nf-overlay-lock');
  });

  it('locks body scroll and inerts background siblings while open', () => {
    expect(getOverlayOpenCountForTests()).toBe(0);
    const { unmount } = render(
      <Drawer open onClose={() => {}} title="t">
        <button type="button">Apply</button>
      </Drawer>,
    );
    expect(getOverlayOpenCountForTests()).toBe(1);
    expect(document.body.style.overflow).toBe('hidden');
    expect(document.body.getAttribute('data-nf-overlay-lock')).toBe('');
    expect(bgSibling.hasAttribute('inert')).toBe(true);
    expect(bgSibling.getAttribute('aria-hidden')).toBe('true');
    unmount();
    expect(getOverlayOpenCountForTests()).toBe(0);
    expect(document.body.style.overflow).toBe('');
    expect(bgSibling.hasAttribute('inert')).toBe(false);
    expect(bgSibling.getAttribute('aria-hidden')).toBeNull();
  });

  it('restores focus to the opener after close, even when the opener lives inside a background-inerted body sibling', async () => {
    // Drawer composes the same useFocusTrap + useOverlayLock pair as
    // Dialog, so it is subject to the same focus-restoration regression.
    // Pin the corrected behaviour here too.
    function Opener() {
      const [open, setOpen] = useState(false);
      return (
        <>
          <button type="button" data-testid="trigger" onClick={() => setOpen(true)}>
            open drawer
          </button>
          {open ? (
            <Drawer open onClose={() => setOpen(false)} title="t">
              <button type="button">Apply</button>
            </Drawer>
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
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Apply' }));

    await user.keyboard('{Escape}');
    expect(document.activeElement).toBe(trigger);
  });

  it('restores focus to the opener after close under StrictMode', async () => {
    // The apps mount under StrictMode, which runs an effect's setup,
    // cleanup, and setup again without re-rendering. The trap captures
    // the opener during render, so that second setup gets no fresh
    // capture: a cleanup that cleared the snapshot left the real close
    // with nothing to focus and dropped the page onto `<body>`.
    function Opener() {
      const [open, setOpen] = useState(false);
      return (
        <>
          <button type="button" data-testid="trigger" onClick={() => setOpen(true)}>
            open drawer
          </button>
          {open ? (
            <Drawer open onClose={() => setOpen(false)} title="t">
              <button type="button">Apply</button>
            </Drawer>
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
    // drawer is open, so the trap owns focus.
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Apply' }));

    await user.keyboard('{Escape}');
    expect(document.activeElement).toBe(trigger);
  });
});
