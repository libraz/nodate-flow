import { act, fireEvent, render, screen } from '@testing-library/react';
import type { ReactElement } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Collapse from './collapse';

function Section({ open }: { open: boolean }): ReactElement {
  return (
    <Collapse open={open} id="section" className="extra">
      <button type="button">inside</button>
    </Collapse>
  );
}

/** Report a running exit animation on the collapse root, as a browser with the stylesheet would. */
function stubExitAnimation(duration: string): void {
  const real = window.getComputedStyle.bind(window);
  vi.spyOn(window, 'getComputedStyle').mockImplementation((el, pseudo) => {
    const style = real(el, pseudo);
    if ((el as HTMLElement).id !== 'section') return style;
    return {
      ...style,
      animationName: 'collapse',
      animationDuration: duration,
      animationDelay: '0s',
    } as CSSStyleDeclaration;
  });
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('Collapse', () => {
  it('renders nothing while closed from the start', () => {
    render(<Section open={false} />);
    expect(document.getElementById('section')).toBeNull();
  });

  it('passes id and className through and skips the enter when open at mount', () => {
    render(<Section open />);
    const root = document.getElementById('section');
    expect(root?.classList.contains('extra')).toBe(true);
    expect(root?.getAttribute('data-state')).toBe('open');
    expect(root?.hasAttribute('data-skip-enter')).toBe(true);
    expect(screen.getByRole('button', { name: 'inside' })).toBeDefined();
  });

  it('animates the enter when opened after mount', () => {
    const { rerender } = render(<Section open={false} />);
    rerender(<Section open />);
    expect(document.getElementById('section')?.hasAttribute('data-skip-enter')).toBe(false);
  });

  it('unmounts at once when no animation runs', () => {
    const { rerender } = render(<Section open />);
    rerender(<Section open={false} />);
    expect(document.getElementById('section')).toBeNull();
  });

  it('keeps children mounted but inert while the exit plays, then unmounts', () => {
    stubExitAnimation('150ms');
    const { rerender } = render(<Section open />);
    rerender(<Section open={false} />);
    const root = document.getElementById('section');
    expect(root?.getAttribute('data-state')).toBe('closed');
    expect(root?.hasAttribute('inert')).toBe(true);
    expect(screen.getByText('inside')).toBeDefined();
    act(() => {
      if (root) fireEvent.animationEnd(root);
    });
    expect(document.getElementById('section')).toBeNull();
  });
});
