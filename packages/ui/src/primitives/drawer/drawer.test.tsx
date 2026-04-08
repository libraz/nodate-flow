import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';
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
