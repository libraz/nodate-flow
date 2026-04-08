import { act, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';
import { ToastProvider, toaster } from './toast';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

describe.each(THEMES)('Toast [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    toaster.clear();
    document.documentElement.removeAttribute('data-theme');
    document.getElementById('nf-toast-root')?.remove();
  });

  it('renders viewport with a toast and has no a11y violations', async () => {
    render(<ToastProvider label="Notifications" />);
    act(() => {
      toaster.show({ message: 'Hello world' });
    });
    expect(screen.getByText('Hello world')).toBeDefined();
    const portal = document.getElementById('nf-toast-root');
    expect(portal).not.toBeNull();
    if (portal) {
      expect(await axe(portal)).toHaveNoViolations();
    }
  });

  it('shows a toast via imperative API', () => {
    render(<ToastProvider label="Notifications" />);
    act(() => {
      toaster.show({ message: 'Saved' });
    });
    expect(screen.getByText('Saved')).toBeDefined();
  });

  it('auto-dismisses after duration', () => {
    vi.useFakeTimers();
    try {
      render(<ToastProvider label="Notifications" />);
      act(() => {
        toaster.show({ message: 'Bye', duration: 1000 });
      });
      expect(screen.getByText('Bye')).toBeDefined();
      act(() => {
        vi.advanceTimersByTime(1100);
      });
      expect(screen.queryByText('Bye')).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it('dismiss removes a toast immediately', () => {
    render(<ToastProvider label="Notifications" />);
    let id = '';
    act(() => {
      id = toaster.show({ message: 'Hello' });
    });
    expect(screen.getByText('Hello')).toBeDefined();
    act(() => {
      toaster.dismiss(id);
    });
    expect(screen.queryByText('Hello')).toBeNull();
  });

  it('supports tone variants', () => {
    render(<ToastProvider label="Notifications" />);
    act(() => {
      toaster.show({ message: 'Warn', tone: 'warning' });
    });
    expect(screen.getByText('Warn').getAttribute('data-tone')).toBe('warning');
  });
});
