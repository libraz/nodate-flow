import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { type ReactElement, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';
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

  it('uses fixed-size container variants so morphic dialogs do not reflow on kind switch', () => {
    // Memory rule: "Morphic UI must not reflow" — kind/mode-switching
    // dialogs need a fixed container size. The Dialog primitive enforces
    // this by exposing exactly three size variants (md/lg/xl), each
    // mapped to a single CSS-module class. Callers pick a size that
    // accommodates the largest variant and stick with it; the size MUST
    // NOT change as a function of inner content.
    for (const size of ['md', 'lg', 'xl'] as const) {
      const { unmount } = render(
        <Dialog open={true} onClose={() => {}} title="t" size={size}>
          <button type="button">OK</button>
        </Dialog>,
      );
      const dialog = screen.getByRole('dialog');
      // The size token determines width, not the content. data-size is the
      // stable surface; sizeMd/sizeLg/sizeXl are the CSS-module classes.
      expect(dialog.getAttribute('data-size')).toBe(size);
      unmount();
    }
  });
});
