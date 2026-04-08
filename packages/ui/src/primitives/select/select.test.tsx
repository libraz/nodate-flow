import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { type ReactElement, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';
import Select from './select';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

function Controlled(): ReactElement {
  const [v, setV] = useState('a');
  return (
    <Select aria-label="ctrl" value={v} onChange={(e) => setV(e.target.value)}>
      <option value="a">A</option>
      <option value="b">B</option>
    </Select>
  );
}

describe.each(THEMES)('Select [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders options and has no a11y violations', async () => {
    const { container } = render(
      <label>
        Pick
        <Select defaultValue="a">
          <option value="a">A</option>
          <option value="b">B</option>
        </Select>
      </label>,
    );
    expect((screen.getByLabelText('Pick') as HTMLSelectElement).value).toBe('a');
    expect(await axe(container)).toHaveNoViolations();
  });

  it('supports controlled selection', async () => {
    const user = userEvent.setup();
    render(<Controlled />);
    const el = screen.getByLabelText('ctrl') as HTMLSelectElement;
    await user.selectOptions(el, 'b');
    expect(el.value).toBe('b');
  });

  it('respects disabled', () => {
    render(
      <Select aria-label="d" disabled>
        <option value="a">A</option>
      </Select>,
    );
    expect((screen.getByLabelText('d') as HTMLSelectElement).disabled).toBe(true);
  });
});
