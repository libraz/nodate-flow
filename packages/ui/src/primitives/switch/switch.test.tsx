import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { type ReactElement, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';
import Switch from './switch';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

function Controlled({ onChange }: { onChange: (v: boolean) => void }): ReactElement {
  const [v, setV] = useState(false);
  return (
    <Switch
      aria-label="ctrl"
      checked={v}
      onCheckedChange={(n) => {
        setV(n);
        onChange(n);
      }}
    />
  );
}

describe.each(THEMES)('Switch [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders with role switch and has no a11y violations', async () => {
    const { container } = render(<Switch aria-label="notify" />);
    const el = screen.getByRole('switch', { name: 'notify' });
    expect(el.getAttribute('aria-checked')).toBe('false');
    expect(await axe(container)).toHaveNoViolations();
  });

  it('toggles uncontrolled', async () => {
    const user = userEvent.setup();
    render(<Switch aria-label="u" defaultChecked={false} />);
    const el = screen.getByRole('switch', { name: 'u' });
    await user.click(el);
    expect(el.getAttribute('aria-checked')).toBe('true');
  });

  it('toggles controlled and fires onCheckedChange', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(<Controlled onChange={fn} />);
    const el = screen.getByRole('switch', { name: 'ctrl' });
    await user.click(el);
    expect(fn).toHaveBeenCalledWith(true);
    expect(el.getAttribute('aria-checked')).toBe('true');
  });

  it('respects disabled', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(<Switch aria-label="d" disabled onCheckedChange={fn} />);
    await user.click(screen.getByRole('switch', { name: 'd' }));
    expect(fn).not.toHaveBeenCalled();
  });

  it('toggles via keyboard (Space)', async () => {
    const user = userEvent.setup();
    render(<Switch aria-label="k" />);
    const el = screen.getByRole('switch', { name: 'k' });
    el.focus();
    await user.keyboard(' ');
    expect(el.getAttribute('aria-checked')).toBe('true');
  });
});
