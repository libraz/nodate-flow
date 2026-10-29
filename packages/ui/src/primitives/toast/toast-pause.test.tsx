import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { type ToastOptions, ToastProvider, toaster } from './toast';

const DURATION = 5000;

function renderToast(message: ToastOptions['message'], duration = DURATION): HTMLElement {
  render(<ToastProvider label="Notifications" />);
  act(() => {
    toaster.show({ message, duration });
  });
  const toast = document.querySelector<HTMLElement>('#nf-toast-root [data-tone]');
  if (!toast) throw new Error('Toast item was not rendered');
  return toast;
}

function advanceTime(milliseconds: number): void {
  act(() => {
    vi.advanceTimersByTime(milliseconds);
  });
}

function dispatch(event: () => boolean): void {
  act(() => {
    event();
  });
}

describe('Toast countdown pause interactions', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
  });

  afterEach(() => {
    cleanup();
    toaster.clear();
    document.getElementById('nf-toast-root')?.remove();
    vi.clearAllTimers();
    vi.useRealTimers();
  });

  it('pauses and resumes for hover alone', () => {
    const toast = renderToast(<button type="button">Open</button>, 1000);

    advanceTime(400);
    dispatch(() => fireEvent.mouseEnter(toast));
    advanceTime(1000);
    expect(screen.getByRole('button', { name: 'Open' })).toBeDefined();

    dispatch(() => fireEvent.mouseLeave(toast));
    advanceTime(599);
    expect(screen.getByRole('button', { name: 'Open' })).toBeDefined();
    advanceTime(1);
    expect(screen.queryByRole('button', { name: 'Open' })).toBeNull();
  });

  it('pauses and resumes for focus alone', () => {
    renderToast(<a href="#details">Details</a>, 1000);
    const link = screen.getByRole('link', { name: 'Details' });

    advanceTime(400);
    dispatch(() => fireEvent.focus(link));
    advanceTime(1000);
    expect(screen.getByRole('link', { name: 'Details' })).toBeDefined();

    dispatch(() => fireEvent.blur(link, { relatedTarget: null }));
    advanceTime(599);
    expect(screen.getByRole('link', { name: 'Details' })).toBeDefined();
    advanceTime(1);
    expect(screen.queryByRole('link', { name: 'Details' })).toBeNull();
  });

  it('stays paused after mouseleave while a child still has focus', () => {
    const toast = renderToast(<button type="button">Continue</button>);
    const button = screen.getByRole('button', { name: 'Continue' });

    advanceTime(1000);
    dispatch(() => fireEvent.mouseEnter(toast));
    dispatch(() => fireEvent.focus(button));
    advanceTime(2500);

    dispatch(() => fireEvent.mouseLeave(toast));
    advanceTime(DURATION + 1);
    expect(screen.getByRole('button', { name: 'Continue' })).toBeDefined();

    dispatch(() => fireEvent.blur(button, { relatedTarget: null }));
    advanceTime(DURATION - 1000 - 1);
    expect(screen.getByRole('button', { name: 'Continue' })).toBeDefined();
    advanceTime(1);
    expect(screen.queryByRole('button', { name: 'Continue' })).toBeNull();
  });

  it('stays paused after blur while the toast is still hovered', () => {
    const toast = renderToast(<a href="#details">Details</a>);
    const link = screen.getByRole('link', { name: 'Details' });

    advanceTime(500);
    dispatch(() => fireEvent.focus(link));
    dispatch(() => fireEvent.mouseEnter(toast));
    advanceTime(2000);

    dispatch(() => fireEvent.blur(link, { relatedTarget: null }));
    advanceTime(DURATION + 1);
    expect(screen.getByRole('link', { name: 'Details' })).toBeDefined();

    dispatch(() => fireEvent.mouseLeave(toast));
    advanceTime(DURATION - 500 - 1);
    expect(screen.getByRole('link', { name: 'Details' })).toBeDefined();
    advanceTime(1);
    expect(screen.queryByRole('link', { name: 'Details' })).toBeNull();
  });

  it('keeps focus pause when focus moves between toast children', () => {
    renderToast(
      <>
        <button type="button">First action</button>
        <button type="button">Second action</button>
      </>,
    );
    const first = screen.getByRole('button', { name: 'First action' });
    const second = screen.getByRole('button', { name: 'Second action' });

    advanceTime(1000);
    dispatch(() => fireEvent.focus(first));
    advanceTime(1000);
    dispatch(() => fireEvent.blur(first, { relatedTarget: second }));
    expect(vi.getTimerCount()).toBe(0);
    dispatch(() => fireEvent.focus(second));
    advanceTime(DURATION + 1);
    expect(screen.getByRole('button', { name: 'First action' })).toBeDefined();
    expect(screen.getByRole('button', { name: 'Second action' })).toBeDefined();

    dispatch(() => fireEvent.blur(second, { relatedTarget: null }));
    advanceTime(DURATION - 1000 - 1);
    expect(screen.getByRole('button', { name: 'First action' })).toBeDefined();
    advanceTime(1);
    expect(screen.queryByRole('button', { name: 'First action' })).toBeNull();
  });
});
