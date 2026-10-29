import { act, fireEvent, render, screen } from '@testing-library/react';
import type { ReactElement } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { usePresence } from './use-presence';

function Panel({ open }: { open: boolean }): ReactElement | null {
  const presence = usePresence<HTMLDivElement>(open);
  if (!presence.mounted) return null;
  return (
    <div ref={presence.ref} data-testid="panel" data-state={presence.state}>
      <span data-testid="child" />
    </div>
  );
}

/** Report a running exit animation for the panel, as a browser with the stylesheet would. */
function stubExitAnimation(duration: string, delay = '0s'): void {
  const real = window.getComputedStyle.bind(window);
  vi.spyOn(window, 'getComputedStyle').mockImplementation((el, pseudo) => {
    const style = real(el, pseudo);
    if ((el as HTMLElement).dataset?.testid !== 'panel') return style;
    return {
      ...style,
      animationName: 'nf-fade-out',
      animationDuration: duration,
      animationDelay: delay,
    } as CSSStyleDeclaration;
  });
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe('usePresence', () => {
  it('stays unmounted while closed from the start', () => {
    render(<Panel open={false} />);
    expect(screen.queryByTestId('panel')).toBeNull();
  });

  it('unmounts in the same commit when no animation is computed', () => {
    const { rerender } = render(<Panel open />);
    expect(screen.getByTestId('panel').dataset.state).toBe('open');
    rerender(<Panel open={false} />);
    expect(screen.queryByTestId('panel')).toBeNull();
  });

  it('unmounts in the same commit when the animation duration is 0ms', () => {
    stubExitAnimation('0ms');
    const { rerender } = render(<Panel open />);
    rerender(<Panel open={false} />);
    expect(screen.queryByTestId('panel')).toBeNull();
  });

  it('keeps the element in the closed state until its own animationend', () => {
    stubExitAnimation('120ms');
    const { rerender } = render(<Panel open />);
    rerender(<Panel open={false} />);

    const panel = screen.getByTestId('panel');
    expect(panel.dataset.state).toBe('closed');

    // A descendant finishing its own animation does not end the exit.
    fireEvent.animationEnd(screen.getByTestId('child'));
    expect(screen.getByTestId('panel')).toBe(panel);

    fireEvent.animationEnd(panel);
    expect(screen.queryByTestId('panel')).toBeNull();
  });

  it('falls back to a timeout derived from duration plus delay', () => {
    vi.useFakeTimers();
    stubExitAnimation('0.12s', '40ms');
    const { rerender } = render(<Panel open />);
    rerender(<Panel open={false} />);

    act(() => {
      vi.advanceTimersByTime(160);
    });
    expect(screen.getByTestId('panel').dataset.state).toBe('closed');

    act(() => {
      vi.advanceTimersByTime(100);
    });
    expect(screen.queryByTestId('panel')).toBeNull();
  });

  it('reopening during the exit keeps the same element and returns it to open', () => {
    vi.useFakeTimers();
    stubExitAnimation('120ms');
    const { rerender } = render(<Panel open />);
    const panel = screen.getByTestId('panel');
    rerender(<Panel open={false} />);
    rerender(<Panel open />);

    expect(screen.getByTestId('panel')).toBe(panel);
    expect(panel.dataset.state).toBe('open');

    // The cancelled exit's timer must not unmount the reopened element.
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(screen.getByTestId('panel')).toBe(panel);
  });
});
