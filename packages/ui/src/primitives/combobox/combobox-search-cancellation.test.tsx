import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import Combobox, { type ComboboxOption } from './combobox';

const OPTIONS: ComboboxOption[] = [{ value: 'alice', label: 'Alice' }];

describe('Combobox pending async search cancellation', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('cancels a pending search on Escape and searches fresh input after reopening', () => {
    const onSearch = vi.fn();
    render(
      <Combobox
        options={[]}
        aria-label="users"
        onSearch={onSearch}
        emptyMessage="No users found"
        searchDebounceMs={200}
      />,
    );

    const input = screen.getByRole('combobox') as HTMLInputElement;
    fireEvent.change(input, { target: { value: 'discarded' } });
    expect(screen.getByRole('listbox')).toBeDefined();

    fireEvent.keyDown(input, { key: 'Escape' });

    expect(screen.queryByRole('listbox')).toBeNull();
    expect(input.value).toBe('');
    act(() => {
      vi.advanceTimersByTime(200);
    });
    expect(onSearch).not.toHaveBeenCalled();

    fireEvent.change(input, { target: { value: 'fresh' } });
    expect(screen.getByRole('listbox')).toBeDefined();
    act(() => {
      vi.advanceTimersByTime(200);
    });

    expect(onSearch).toHaveBeenCalledTimes(1);
    expect(onSearch).toHaveBeenLastCalledWith('fresh');
  });

  it('cancels a pending search when an option is selected', () => {
    const onChange = vi.fn();
    const onSearch = vi.fn();
    render(
      <Combobox
        options={OPTIONS}
        aria-label="users"
        onChange={onChange}
        onSearch={onSearch}
        searchDebounceMs={200}
      />,
    );

    const input = screen.getByRole('combobox') as HTMLInputElement;
    fireEvent.change(input, { target: { value: 'ali' } });
    const option = screen.getByRole('option', { name: 'Alice' });

    fireEvent.click(option);

    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith('alice');
    expect(input.value).toBe('Alice');
    expect(screen.queryByRole('listbox')).toBeNull();
    act(() => {
      vi.advanceTimersByTime(200);
    });
    expect(onSearch).not.toHaveBeenCalled();
  });

  it('cancels a pending search when an outside pointer press dismisses the popup', () => {
    const onSearch = vi.fn();
    render(
      <Combobox
        options={[]}
        aria-label="users"
        onSearch={onSearch}
        emptyMessage="No users found"
        searchDebounceMs={200}
      />,
    );

    const input = screen.getByRole('combobox');
    fireEvent.change(input, { target: { value: 'discarded' } });
    expect(screen.getByRole('listbox')).toBeDefined();

    fireEvent.pointerDown(document.body);

    expect(screen.queryByRole('listbox')).toBeNull();
    act(() => {
      vi.advanceTimersByTime(200);
    });
    expect(onSearch).not.toHaveBeenCalled();
  });

  it('cancels a pending search on unmount', () => {
    const onSearch = vi.fn();
    const { unmount } = render(
      <Combobox
        options={[]}
        aria-label="users"
        onSearch={onSearch}
        emptyMessage="No users found"
        searchDebounceMs={200}
      />,
    );

    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'discarded' } });
    unmount();
    act(() => {
      vi.advanceTimersByTime(200);
    });

    expect(onSearch).not.toHaveBeenCalled();
  });

  it('fires an active search once after the debounce window', () => {
    const onSearch = vi.fn();
    render(<Combobox options={[]} aria-label="users" onSearch={onSearch} searchDebounceMs={200} />);

    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'active' } });

    act(() => {
      vi.advanceTimersByTime(199);
    });
    expect(onSearch).not.toHaveBeenCalled();
    act(() => {
      vi.advanceTimersByTime(1);
    });

    expect(onSearch).toHaveBeenCalledTimes(1);
    expect(onSearch).toHaveBeenCalledWith('active');
  });
});
