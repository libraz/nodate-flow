/**
 * How the mobile month view measures its rows.
 *
 * A week with several events is taller than an empty one, so the row
 * heights have to be measured rather than assumed — the estimate the
 * virtualizer starts from is only a guess. But the measurement must not
 * be taken while React is committing: the view opens scrolled a year
 * into the list, so a row first measured above the fold makes the
 * virtualizer correct the scroll offset and re-render synchronously,
 * and React declines a synchronous flush from inside a lifecycle method
 * and defers it to a later render.
 *
 * These two tests are a pair and only mean something together: rows are
 * measured to their real height, and nothing warns while it happens.
 * Pinning the rows to the estimate would satisfy the second alone.
 *
 * The row height stubbed here deliberately differs from the estimate in
 * the component — a stub that matches it measures a delta of zero and
 * exercises none of this.
 */

import { Zone } from '@nodate-flow/ui/time';
import { render, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, type Mock, vi } from 'vitest';

import MonthScroll from '../month-scroll';

const VIEWPORT_PX = 640;
/** Taller than the component's own `ESTIMATED_WEEK_PX`. */
const ROW_PX = 140;
const HEADER_PX = 24;

const TRANSLATE_PREFIX = 'translateY(';

/**
 * The fixtures are built from local wall clocks, so the host zone keeps
 * this file about measurement rather than about day boundaries.
 */
const viewZone = Zone.browser();

/**
 * Report the metrics a browser would, and deliver resize observations
 * out of band the way a browser does — the row heights are only
 * reachable through the observer, and a stub that answered inline would
 * be measuring the test rather than the component.
 */
function stubElementMetrics(): void {
  class ImmediateResizeObserver {
    constructor(private readonly cb: (entries: unknown[]) => void) {}
    observe(el: Element): void {
      queueMicrotask(() =>
        this.cb([{ target: el, contentRect: { width: 390, height: VIEWPORT_PX } }]),
      );
    }
    unobserve(): void {}
    disconnect(): void {}
  }
  vi.stubGlobal('ResizeObserver', ImmediateResizeObserver);
  const isScrollport = (el: HTMLElement): boolean => el.getAttribute('role') === 'grid';
  const heightOf = (el: HTMLElement): number => {
    if (isScrollport(el)) return VIEWPORT_PX;
    return el.hasAttribute('data-month') ? HEADER_PX : ROW_PX;
  };
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
    configurable: true,
    get(this: HTMLElement) {
      return heightOf(this);
    },
  });
  Object.defineProperty(HTMLElement.prototype, 'offsetWidth', {
    configurable: true,
    get: () => 390,
  });
  Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
    configurable: true,
    get(this: HTMLElement) {
      return heightOf(this);
    },
  });
  Object.defineProperty(HTMLElement.prototype, 'scrollHeight', {
    configurable: true,
    get(this: HTMLElement) {
      if (!isScrollport(this)) return ROW_PX;
      const inner = this.firstElementChild as HTMLElement | null;
      return Number.parseInt(inner?.style.blockSize ?? '0', 10) || VIEWPORT_PX;
    },
  });
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (this: Element) {
    const height = heightOf(this as HTMLElement);
    return {
      x: 0,
      y: 0,
      top: 0,
      left: 0,
      right: 390,
      bottom: height,
      width: 390,
      height,
      toJSON: () => ({}),
    } as DOMRect;
  });
  const offsets = new WeakMap<HTMLElement, number>();
  const setOffset = (el: HTMLElement, top: number): void => {
    offsets.set(el, Math.max(0, top));
    // Browsers report a scroll after the fact, never inside the call
    // that caused it.
    queueMicrotask(() => el.dispatchEvent(new Event('scroll')));
  };
  Object.defineProperty(HTMLElement.prototype, 'scrollTop', {
    configurable: true,
    get(this: HTMLElement) {
      return offsets.get(this) ?? 0;
    },
    set(this: HTMLElement, top: number) {
      setOffset(this, top);
    },
  });
  HTMLElement.prototype.scrollTo = function scrollTo(this: HTMLElement, arg?: unknown): void {
    const top = typeof arg === 'object' && arg !== null ? (arg as ScrollToOptions).top : undefined;
    setOffset(this, top ?? 0);
  };
}

function renderView(): ReturnType<typeof render> {
  const noop = (): void => {};
  return render(
    <MonthScroll
      events={[]}
      tasksByDate={new Map()}
      holidaysByDate={new Map()}
      locale="en"
      weekStart="mon"
      zone={viewZone}
      stateColor={() => 'red'}
      scrollToTodaySignal={0}
      onDayOpen={noop}
    />,
  );
}

/** The offset a virtual item was translated to, or NaN if it was not. */
function placedAt(el: HTMLElement): number {
  const transform = el.style.transform;
  if (!transform.startsWith(TRANSLATE_PREFIX)) return Number.NaN;
  return Number.parseFloat(transform.slice(TRANSLATE_PREFIX.length));
}

/** Rendered week rows, in index order, with the offset each was placed at. */
function placedWeeks(container: HTMLElement): { index: number; start: number }[] {
  return [...container.querySelectorAll<HTMLElement>('[data-index]')]
    .filter((el) => el.querySelector('[data-week]') !== null)
    .map((el) => ({ index: Number(el.dataset.index), start: placedAt(el) }))
    .filter((row) => Number.isFinite(row.start))
    .sort((a, b) => a.index - b.index);
}

/** Gaps between week rows that are adjacent in the item list. */
function adjacentGaps(weeks: { index: number; start: number }[]): number[] {
  const gaps: number[] = [];
  for (let i = 1; i < weeks.length; i++) {
    const row = weeks[i];
    const prev = weeks[i - 1];
    if (!row || !prev || row.index !== prev.index + 1) continue;
    gaps.push(row.start - prev.start);
  }
  return gaps;
}

let consoleError: Mock<typeof console.error>;

beforeEach(() => {
  stubElementMetrics();
  consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('MonthScroll row measurement', () => {
  it('places rows at their measured height, not the estimate', async () => {
    const { container } = renderView();
    await waitFor(() => {
      const gaps = adjacentGaps(placedWeeks(container));
      expect(gaps.length).toBeGreaterThan(0);
      // Consecutive week rows sit one measured row apart. Left
      // unmeasured they would sit at the estimate instead.
      expect([...new Set(gaps)]).toEqual([ROW_PX]);
    });
  });

  it('measures without a synchronous flush from inside a lifecycle method', async () => {
    const { container } = renderView();
    await waitFor(() => {
      expect(adjacentGaps(placedWeeks(container)).length).toBeGreaterThan(0);
    });
    const warnings = consoleError.mock.calls
      .map((args) => args.map((a) => String(a)).join(' '))
      .filter((line) => line.includes('flushSync'));
    expect(warnings).toEqual([]);
  });
});
