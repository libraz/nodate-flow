import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { type ReactElement, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';
import Textarea from './textarea';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

function Controlled(): ReactElement {
  const [v, setV] = useState('');
  return <Textarea aria-label="ctrl" value={v} onChange={(e) => setV(e.target.value)} />;
}

describe.each(THEMES)('Textarea [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders and has no a11y violations when labelled', async () => {
    const { container } = render(
      <label>
        Bio
        <Textarea defaultValue="hello" />
      </label>,
    );
    expect(screen.getByLabelText('Bio')).toBeDefined();
    expect(await axe(container)).toHaveNoViolations();
  });

  it('supports uncontrolled typing', async () => {
    const user = userEvent.setup();
    render(<Textarea aria-label="u" />);
    const el = screen.getByLabelText('u') as HTMLTextAreaElement;
    await user.type(el, 'line');
    expect(el.value).toBe('line');
  });

  it('supports controlled typing', async () => {
    const user = userEvent.setup();
    render(<Controlled />);
    const el = screen.getByLabelText('ctrl') as HTMLTextAreaElement;
    await user.type(el, 'xyz');
    expect(el.value).toBe('xyz');
  });

  it('applies aria-invalid when invalid', () => {
    render(<Textarea aria-label="x" invalid />);
    expect(screen.getByLabelText('x').getAttribute('aria-invalid')).toBe('true');
  });

  it('respects disabled', async () => {
    const user = userEvent.setup();
    render(<Textarea aria-label="d" disabled />);
    const el = screen.getByLabelText('d') as HTMLTextAreaElement;
    await user.type(el, 'no');
    expect(el.value).toBe('');
  });
});
