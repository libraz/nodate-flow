import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
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

  it('shows all options when opened and the query still matches the selected label', async () => {
    const user = userEvent.setup();
    render(<Combobox options={OPTIONS} defaultValue="banana" aria-label="fruit" />);
    const input = screen.getByRole('combobox');
    await user.click(input);
    await waitFor(() => expect(screen.getByRole('listbox')).toBeDefined());
    expect(screen.getAllByRole('option')).toHaveLength(3);
  });

  it('filters as soon as the user types something that diverges from the selected label', async () => {
    const user = userEvent.setup();
    render(<Combobox options={OPTIONS} defaultValue="banana" aria-label="fruit" />);
    const input = screen.getByRole('combobox');
    await user.click(input);
    await waitFor(() => expect(screen.getByRole('listbox')).toBeDefined());
    await user.clear(input);
    await user.type(input, 'ch');
    expect(screen.getAllByRole('option')).toHaveLength(1);
    expect(screen.getByRole('option', { name: 'Cherry' })).toBeDefined();
  });

  it('selects all input text when the input receives focus', async () => {
    render(<Combobox options={OPTIONS} defaultValue="banana" aria-label="fruit" />);
    const input = screen.getByRole('combobox') as HTMLInputElement;
    expect(input.value).toBe('Banana');
    act(() => {
      input.focus();
    });
    expect(input.selectionStart).toBe(0);
    expect(input.selectionEnd).toBe(input.value.length);
  });

  it('does not select on Enter during IME composition', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(<Combobox options={OPTIONS} aria-label="fruit" onChange={fn} />);
    const input = screen.getByRole('combobox') as HTMLInputElement;
    await user.click(input);
    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeNull());
    // Simulate a kana commit: browsers fire keydown for Enter with
    // isComposing=true (and legacy keyCode=229) when the IME owns the key.
    fireEvent.keyDown(input, { key: 'Enter', isComposing: true, keyCode: 229 });
    expect(fn).not.toHaveBeenCalled();
    expect(screen.queryByRole('listbox')).not.toBeNull();
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

  it('ArrowDown moves the active option and aria-activedescendant follows', async () => {
    const user = userEvent.setup();
    render(<Combobox options={OPTIONS} aria-label="fruit" />);
    const input = screen.getByRole('combobox') as HTMLInputElement;
    await user.click(input);
    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeNull());

    fireEvent.keyDown(input, { key: 'ArrowDown' }); // 0 -> 1
    const optionsList = screen.getAllByRole('option');
    const second = optionsList[1] as HTMLElement;
    expect(input.getAttribute('aria-activedescendant')).toBe(second.id);

    fireEvent.keyDown(input, { key: 'ArrowDown' }); // 1 -> 2
    const third = optionsList[2] as HTMLElement;
    expect(input.getAttribute('aria-activedescendant')).toBe(third.id);

    fireEvent.keyDown(input, { key: 'ArrowDown' }); // 2 -> wrap to 0
    const first = optionsList[0] as HTMLElement;
    expect(input.getAttribute('aria-activedescendant')).toBe(first.id);
  });

  it('ArrowUp wraps from the top to the last option', async () => {
    const user = userEvent.setup();
    render(<Combobox options={OPTIONS} aria-label="fruit" />);
    const input = screen.getByRole('combobox') as HTMLInputElement;
    await user.click(input);
    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeNull());

    fireEvent.keyDown(input, { key: 'ArrowUp' }); // from 0 -> last
    const optionsList = screen.getAllByRole('option');
    const last = optionsList[optionsList.length - 1] as HTMLElement;
    expect(input.getAttribute('aria-activedescendant')).toBe(last.id);
  });

  it('Home jumps to first and End jumps to last', async () => {
    const user = userEvent.setup();
    render(<Combobox options={OPTIONS} aria-label="fruit" />);
    const input = screen.getByRole('combobox') as HTMLInputElement;
    await user.click(input);
    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeNull());

    fireEvent.keyDown(input, { key: 'End' });
    const optionsList = screen.getAllByRole('option');
    const last = optionsList[optionsList.length - 1] as HTMLElement;
    expect(input.getAttribute('aria-activedescendant')).toBe(last.id);

    fireEvent.keyDown(input, { key: 'Home' });
    const first = optionsList[0] as HTMLElement;
    expect(input.getAttribute('aria-activedescendant')).toBe(first.id);
  });

  it('async onSearch fires once after the debounce window', async () => {
    vi.useFakeTimers();
    try {
      const onSearch = vi.fn();
      render(
        <Combobox options={[]} aria-label="users" onSearch={onSearch} searchDebounceMs={200} />,
      );
      const input = screen.getByRole('combobox') as HTMLInputElement;
      // Type without debounce flushing in between
      fireEvent.change(input, { target: { value: 'a' } });
      fireEvent.change(input, { target: { value: 'al' } });
      fireEvent.change(input, { target: { value: 'ali' } });
      expect(onSearch).not.toHaveBeenCalled();
      act(() => {
        vi.advanceTimersByTime(199);
      });
      expect(onSearch).not.toHaveBeenCalled();
      act(() => {
        vi.advanceTimersByTime(2);
      });
      expect(onSearch).toHaveBeenCalledTimes(1);
      expect(onSearch).toHaveBeenLastCalledWith('ali');
    } finally {
      vi.useRealTimers();
    }
  });

  it('async mode does not filter options client-side', async () => {
    const user = userEvent.setup();
    const onSearch = vi.fn();
    // Caller-supplied options that do not match the typed query.
    // Static mode would filter these out; async mode must show them as-is.
    render(
      <Combobox
        options={[{ value: 'u1', label: 'Alice' }]}
        aria-label="users"
        onSearch={onSearch}
      />,
    );
    const input = screen.getByRole('combobox');
    await user.click(input);
    await user.type(input, 'zzz');
    expect(screen.getByRole('option', { name: 'Alice' })).toBeDefined();
  });

  it('renders emptyMessage when options is empty and not loading', async () => {
    const user = userEvent.setup();
    render(
      <Combobox
        options={[]}
        aria-label="users"
        onSearch={() => {}}
        emptyMessage="No users found"
      />,
    );
    const input = screen.getByRole('combobox');
    await user.click(input);
    await user.type(input, 'q');
    await waitFor(() => expect(screen.getByText('No users found')).toBeDefined());
  });

  it('does not point aria-controls at a missing popup when no rows are rendered', async () => {
    const user = userEvent.setup();
    render(<Combobox options={[]} aria-label="users" />);

    const input = screen.getByRole('combobox');
    await user.click(input);

    expect(screen.queryByRole('listbox')).toBeNull();
    expect(input.getAttribute('aria-expanded')).toBe('false');
    expect(input.hasAttribute('aria-controls')).toBe(false);
  });

  it('shows loading row when isLoading and options is empty', async () => {
    const user = userEvent.setup();
    render(
      <Combobox
        options={[]}
        aria-label="users"
        onSearch={() => {}}
        isLoading
        loadingMessage="Searching"
        emptyMessage="No users found"
      />,
    );
    const input = screen.getByRole('combobox');
    await user.click(input);
    expect(screen.getByText('Searching')).toBeDefined();
    expect(screen.queryByText('No users found')).toBeNull();
  });

  it('renderItem replaces the default row content', async () => {
    const user = userEvent.setup();
    render(
      <Combobox
        options={OPTIONS}
        aria-label="fruit"
        renderItem={(o) => <span data-testid={`row-${o.value}`}>{o.label.toUpperCase()}</span>}
      />,
    );
    const input = screen.getByRole('combobox');
    await user.click(input);
    expect(screen.getByTestId('row-apple').textContent).toBe('APPLE');
  });

  it('defaults dir to "auto"', () => {
    render(<Combobox options={OPTIONS} aria-label="auto" />);
    expect(screen.getByRole('combobox').getAttribute('dir')).toBe('auto');
  });

  it('forwards dir="rtl" to the underlying input', () => {
    render(<Combobox options={OPTIONS} aria-label="rtl" dir="rtl" />);
    expect(screen.getByRole('combobox').getAttribute('dir')).toBe('rtl');
  });
});
