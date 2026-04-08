import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';
import Combobox, { type ComboboxOption } from './combobox';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

const OPTIONS: ComboboxOption[] = [
  { value: 'apple', label: 'Apple' },
  { value: 'banana', label: 'Banana' },
  { value: 'cherry', label: 'Cherry' },
];

describe.each(THEMES)('Combobox [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders combobox role with no a11y violations', async () => {
    const { container } = render(<Combobox options={OPTIONS} aria-label="fruit" />);
    expect(screen.getByRole('combobox')).toBeDefined();
    expect(await axe(container)).toHaveNoViolations();
  });

  it('opens listbox on focus and filters by input', async () => {
    const user = userEvent.setup();
    render(<Combobox options={OPTIONS} aria-label="fruit" />);
    const input = screen.getByRole('combobox');
    await user.click(input);
    await waitFor(() => expect(screen.getByRole('listbox')).toBeDefined());
    await user.type(input, 'ban');
    expect(screen.getByRole('option', { name: 'Banana' })).toBeDefined();
    expect(screen.queryByRole('option', { name: 'Apple' })).toBeNull();
  });

  it('selects on Enter', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(<Combobox options={OPTIONS} aria-label="fruit" onChange={fn} />);
    const input = screen.getByRole('combobox') as HTMLInputElement;
    input.focus();
    await user.keyboard('{ArrowDown}');
    await user.keyboard('{Enter}');
    expect(fn).toHaveBeenCalled();
  });

  it('dismisses on Escape', async () => {
    const user = userEvent.setup();
    render(<Combobox options={OPTIONS} aria-label="fruit" />);
    const input = screen.getByRole('combobox');
    await user.click(input);
    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeNull());
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('listbox')).toBeNull());
  });
});
