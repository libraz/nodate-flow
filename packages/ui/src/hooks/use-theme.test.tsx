import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { useTheme } from './use-theme';

describe('useTheme', () => {
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
    localStorage.clear();
  });

  it('applies the default theme to <html data-theme>', () => {
    const { result } = renderHook(() => useTheme({ defaultTheme: 'modern-dark' }));
    expect(result.current.theme).toBe('modern-dark');
    expect(document.documentElement.getAttribute('data-theme')).toBe('modern-dark');
  });

  it('updates the attribute when setTheme is called', () => {
    const { result } = renderHook(() => useTheme({ defaultTheme: 'glass-light' }));
    act(() => result.current.setTheme('glass-dark'));
    expect(result.current.theme).toBe('glass-dark');
    expect(document.documentElement.getAttribute('data-theme')).toBe('glass-dark');
  });

  it('persists to localStorage when storageKey is provided', () => {
    const { result } = renderHook(() =>
      useTheme({ defaultTheme: 'glass-light', storageKey: 'nf:theme' }),
    );
    act(() => result.current.setTheme('modern-light'));
    expect(localStorage.getItem('nf:theme')).toBe('modern-light');
  });

  it('reads existing data-theme on mount', () => {
    document.documentElement.setAttribute('data-theme', 'modern-light');
    const { result } = renderHook(() => useTheme());
    expect(result.current.theme).toBe('modern-light');
  });
});
