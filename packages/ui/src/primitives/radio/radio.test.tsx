import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { type ReactElement, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';
import Radio from './radio';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

function Group(): ReactElement {
  const [v, setV] = useState('a');
  return (
    <fieldset>
      <legend>pick</legend>
      <label>
        A
        <Radio name="g" value="a" checked={v === 'a'} onChange={(e) => setV(e.target.value)} />
      </label>
      <label>
        B
        <Radio name="g" value="b" checked={v === 'b'} onChange={(e) => setV(e.target.value)} />
      </label>
    </fieldset>
  );
}

describe.each(THEMES)('Radio [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders and has no a11y violations', async () => {
    const { container } = render(<Group />);
    expect(await axe(container)).toHaveNoViolations();
  });

  it('selects via click (controlled group)', async () => {
    const user = userEvent.setup();
    render(<Group />);
    const a = screen.getByLabelText('A') as HTMLInputElement;
    const b = screen.getByLabelText('B') as HTMLInputElement;
    expect(a.checked).toBe(true);
    await user.click(b);
    expect(b.checked).toBe(true);
    expect(a.checked).toBe(false);
  });

  it('respects disabled', async () => {
    const user = userEvent.setup();
    render(
      <label>
        d
        <Radio name="x" disabled />
      </label>,
    );
    const el = screen.getByLabelText('d') as HTMLInputElement;
    await user.click(el);
    expect(el.checked).toBe(false);
  });
});
