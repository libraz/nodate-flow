import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { type ReactElement, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';
import Checkbox from './checkbox';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

function Controlled(): ReactElement {
  const [on, setOn] = useState(false);
  return (
    <label>
      agree
      <Checkbox checked={on} onChange={(e) => setOn(e.target.checked)} />
    </label>
  );
}

describe.each(THEMES)('Checkbox [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders and has no a11y violations', async () => {
    const { container } = render(
      <label>
        accept
        <Checkbox defaultChecked />
      </label>,
    );
    expect((screen.getByLabelText('accept') as HTMLInputElement).checked).toBe(true);
    expect(await axe(container)).toHaveNoViolations();
  });

  it('toggles via click (controlled)', async () => {
    const user = userEvent.setup();
    render(<Controlled />);
    const el = screen.getByLabelText('agree') as HTMLInputElement;
    expect(el.checked).toBe(false);
    await user.click(el);
    expect(el.checked).toBe(true);
  });

  it('applies indeterminate', () => {
    render(
      <label>
        mixed
        <Checkbox indeterminate />
      </label>,
    );
    expect((screen.getByLabelText('mixed') as HTMLInputElement).indeterminate).toBe(true);
  });

  it('respects disabled', async () => {
    const user = userEvent.setup();
    render(
      <label>
        d
        <Checkbox disabled />
      </label>,
    );
    const el = screen.getByLabelText('d') as HTMLInputElement;
    await user.click(el);
    expect(el.checked).toBe(false);
  });
});
