import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { type ReactElement, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';
import Input from './input';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

function Controlled(): ReactElement {
  const [v, setV] = useState('');
  return <Input aria-label="ctrl" value={v} onChange={(e) => setV(e.target.value)} />;
}

describe.each(THEMES)('Input [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders and has no a11y violations when labelled', async () => {
    const { container } = render(
      <label>
        Name
        <Input defaultValue="ok" />
      </label>,
    );
    expect(screen.getByLabelText('Name')).toBeDefined();
    expect(await axe(container)).toHaveNoViolations();
  });

  it('supports uncontrolled typing', async () => {
    const user = userEvent.setup();
    render(<Input aria-label="uncontrolled" />);
    const el = screen.getByLabelText('uncontrolled') as HTMLInputElement;
    await user.type(el, 'hi');
    expect(el.value).toBe('hi');
  });

  it('supports controlled typing', async () => {
    const user = userEvent.setup();
    render(<Controlled />);
    const el = screen.getByLabelText('ctrl') as HTMLInputElement;
    await user.type(el, 'abc');
    expect(el.value).toBe('abc');
  });

  it('applies aria-invalid when invalid', () => {
    render(<Input aria-label="x" invalid />);
    expect(screen.getByLabelText('x').getAttribute('aria-invalid')).toBe('true');
  });

  it('respects disabled', async () => {
    const user = userEvent.setup();
    render(<Input aria-label="d" disabled />);
    const el = screen.getByLabelText('d') as HTMLInputElement;
    await user.type(el, 'no');
    expect(el.value).toBe('');
  });

  it('defaults dir to "auto" so the browser flips per content', () => {
    render(<Input aria-label="auto-dir" />);
    expect(screen.getByLabelText('auto-dir').getAttribute('dir')).toBe('auto');
  });

  it('forwards explicit dir="rtl"', () => {
    render(<Input aria-label="rtl-input" dir="rtl" />);
    expect(screen.getByLabelText('rtl-input').getAttribute('dir')).toBe('rtl');
  });
});
