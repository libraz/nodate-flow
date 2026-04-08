import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { useTheme } from './use-theme';

describe('useTheme', () => {
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
    localStorage.clear();
  });

  it('applies the default theme to <html data-theme>', () => {
    const { result } = renderHook(() => useTheme({ defaultTheme: 'dotline-dark' }));
    expect(result.current.theme).toBe('dotline-dark');
    expect(document.documentElement.getAttribute('data-theme')).toBe('dotline-dark');
  });

  it('updates the attribute when setTheme is called', () => {
    const { result } = renderHook(() => useTheme({ defaultTheme: 'aurora-light' }));
    act(() => result.current.setTheme('aurora-dark'));
    expect(result.current.theme).toBe('aurora-dark');
    expect(document.documentElement.getAttribute('data-theme')).toBe('aurora-dark');
  });

  it('persists to localStorage when storageKey is provided', () => {
    const { result } = renderHook(() =>
      useTheme({ defaultTheme: 'aurora-light', storageKey: 'nf:theme' }),
    );
    act(() => result.current.setTheme('dotline-light'));
    expect(localStorage.getItem('nf:theme')).toBe('dotline-light');
  });

  it('reads existing data-theme on mount', () => {
    document.documentElement.setAttribute('data-theme', 'dotline-light');
    const { result } = renderHook(() => useTheme());
    expect(result.current.theme).toBe('dotline-light');
  });
});
