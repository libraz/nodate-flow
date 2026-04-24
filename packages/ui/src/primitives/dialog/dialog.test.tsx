import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
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
});
