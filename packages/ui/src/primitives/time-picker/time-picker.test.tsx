import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import TimePicker from './time-picker';

const THEMES = [
  'glass-light',
  'glass-dark',
  'modern-light',
  'modern-dark',
  'washi-light',
  'washi-dark',
  'classic-light',
  'classic-dark',
  'nothing-light',
  'nothing-dark',
] as const;

describe.each(THEMES)('TimePicker [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('exposes the slot listbox under the listLabel name', async () => {
    const user = userEvent.setup();
    render(<TimePicker value="09:00" onChange={() => undefined} listLabel="Start time" />);
    await user.click(screen.getByRole('button', { name: '09:00' }));
    expect(screen.getByRole('listbox', { name: 'Start time' })).toBeDefined();
  });

  it('styles the trigger as a form field only for the field variant', () => {
    const { rerender } = render(
      <TimePicker value="09:00" onChange={() => undefined} listLabel="Start time" />,
    );
    expect(screen.getByRole('button', { name: '09:00' }).className).not.toContain('field');
    rerender(
      <TimePicker
        value="09:00"
        onChange={() => undefined}
        listLabel="Start time"
        variant="field"
      />,
    );
    expect(screen.getByRole('button', { name: '09:00' }).className).toContain('field');
  });

  it('calls onChange with the selected slot', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<TimePicker value="09:00" onChange={onChange} listLabel="Start time" />);
    await user.click(screen.getByRole('button', { name: '09:00' }));
    await user.click(screen.getByRole('option', { name: '10:30' }));
    expect(onChange).toHaveBeenCalledWith('10:30');
  });
});
