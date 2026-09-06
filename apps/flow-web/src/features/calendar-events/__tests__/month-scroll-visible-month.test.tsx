/**
 * The mobile month view tells the route which month it has scrolled to.
 *
 * The view scrolls a year either side of today, while its events, tasks
 * and holidays are fetched for one month at a time. Without this report
 * the route keeps fetching for the month it opened on, and every other
 * month is drawn from a window that was never asked for — a year of
 * empty rows in both directions.
 *
 * The report cannot be made where the month is decided. That is inside
 * the virtualizer's range extractor, which the library also runs while
 * it measures and commits, and a parent `setState` reached from there is
 * an update made from inside another component's render or commit. So
 * the warning check here is a pair with the reporting ones, the same way
 * the measurement tests are: the month is reported, and React is not
 * asked for the update in a place it will refuse it.
 *
 * The row height stubbed here deliberately differs from the estimate in
 * the component — a stub that matches it measures a delta of zero and
 * exercises none of that.
 */

import { Zone } from '@nodate-flow/ui/time';
import { act, render, waitFor } from '@testing-library/react';
import { type ReactElement, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, type Mock, vi } from 'vitest';

import MonthScroll, { VISIBLE_MONTH_SETTLE_MS } from '../month-scroll';

const VIEWPORT_PX = 640;
/** Taller than the component's own `ESTIMATED_WEEK_PX`. */
const ROW_PX = 140;
const HEADER_PX = 24;

/**
 * The fixtures carry no times, so the host zone keeps this file about
 * the scroll position rather than about day boundaries.
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

const noop = (): void => {};

/**
 * The view under a parent that keeps the reported month in state.
 *
 * A spy alone would not be this component's caller: the route answers
 * the report by moving its cursor, and it is that `setState` — reached
 * from wherever the report was made — that decides whether React is
 * asked for a synchronous flush from inside a lifecycle method. A spy
 * that only records would let the synchronous call pass unnoticed.
 *
 * `tasksByDate` stands in for the data the cursor move fetches: the
 * route replaces the map wholesale each time a query settles, so a fresh
 * Map is what an arrived month looks like from in here.
 */
function Harness({
  report,
  tasksByDate,
}: {
  report: (monthKey: string) => void;
  tasksByDate: Map<string, never[]>;
}): ReactElement {
  const [month, setMonth] = useState('');
  return (
    <>
      <span data-testid="cursor">{month}</span>
      <MonthScroll
        events={[]}
        tasksByDate={tasksByDate}
        holidaysByDate={new Map()}
        locale="en"
        weekStart="mon"
        zone={viewZone}
        stateColor={() => 'red'}
        scrollToTodaySignal={0}
        onVisibleMonthChange={(monthKey) => {
          report(monthKey);
          setMonth(monthKey);
        }}
        onDayOpen={noop}
      />
    </>
  );
}

function renderView(report: (monthKey: string) => void): ReturnType<typeof render> {
  return render(<Harness report={report} tasksByDate={new Map()} />);
}

/** The scroll container the week rows live in. */
function scrollBody(container: HTMLElement): HTMLElement {
  const el = container.querySelector<HTMLElement>('[role="grid"]');
  if (!el) throw new Error('month view has no scroll container');
  return el;
}

/**
 * The month the view is drawing as pinned. The pinned header is the one
 * element the virtualizer does not translate into place, since sticky
 * positioning and a transform cannot both apply to it.
 */
function pinnedMonth(container: HTMLElement): string | null {
  const headers = [...container.querySelectorAll<HTMLElement>('[data-month]')];
  const pinned = headers.find((el) => !el.style.transform);
  return pinned?.dataset.month ?? null;
}

/**
 * Turns of the microtask queue that count as "the view has redrawn".
 *
 * The scroll notification, the render it causes and the measurement of
 * the rows that render each land in a turn of their own — and measuring
 * a row can move the pinned header once more. Reading the pinned month
 * before that has drained names one the view is still on its way past.
 */
const REDRAW_TURNS = 10;

/** Let the redraw the last change set in motion finish. */
async function drain(): Promise<void> {
  for (let i = 0; i < REDRAW_TURNS; i++) {
    await act(async () => {
      await Promise.resolve();
    });
  }
}

/** Margin over the component's own wait, for a slow machine. */
const SETTLE_MARGIN_MS = 60;

/**
 * Let the view redraw and then stand still long enough for the report to
 * trail it. Real timers rather than fake ones: the stubs above deliver
 * their observations through microtasks, which fake timers do not drive.
 */
async function settle(): Promise<void> {
  await drain();
  await act(async () => {
    await new Promise((resolve) => {
      setTimeout(resolve, VISIBLE_MONTH_SETTLE_MS + SETTLE_MARGIN_MS);
    });
  });
  await drain();
}

/** Move the scroll without pausing — one flick of a run of them. */
async function flick(container: HTMLElement, top: number): Promise<void> {
  await act(async () => {
    scrollBody(container).scrollTop = top;
    await Promise.resolve();
  });
  await drain();
}

/** Move the scroll the way a finger would, and let it come to rest. */
async function scrollTo(container: HTMLElement, top: number): Promise<void> {
  await flick(container, top);
  await settle();
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

/**
 * The two ways React objects to a parent updated from inside a child's
 * render or commit: the synchronous flush it declines and defers, and
 * the update made while another component was rendering.
 *
 * This is a canary, not a pinned case. Reporting straight from the range
 * extractor instead of on a timer does not produce either warning here,
 * and no mutation was found that does: on the first render there is no
 * scroll element yet, so the extractor is not reached from render at
 * all, and every later call arrives from the notification path with the
 * pinned index already up to date, so the render-phase call never sees a
 * change to report. The assertion is kept because it costs nothing and
 * would catch the warning if some other change did reach that path — it
 * is not evidence that the deferral is what prevents it.
 */
function updateWarnings(): string[] {
  return consoleError.mock.calls
    .map((args) => args.map((a) => String(a)).join(' '))
    .filter(
      (line) =>
        line.includes('flushSync') || line.includes('while rendering a different component'),
    );
}

describe('MonthScroll visible month', () => {
  it('reports the month a scroll pinned at the top', async () => {
    const onVisibleMonthChange = vi.fn<(monthKey: string) => void>();
    const { container } = renderView(onVisibleMonthChange);
    await waitFor(() => {
      expect(pinnedMonth(container)).not.toBeNull();
    });
    await settle();

    const opened = pinnedMonth(container);
    onVisibleMonthChange.mockClear();

    // Several months further down the list. The exact month depends on
    // where today falls, so it is read back off the view rather than
    // computed here.
    await scrollTo(container, scrollBody(container).scrollTop + 2400);

    const arrived = pinnedMonth(container);
    expect(arrived).not.toBe(opened);
    expect(onVisibleMonthChange).toHaveBeenCalledTimes(1);
    expect(onVisibleMonthChange).toHaveBeenLastCalledWith(arrived);
  });

  it('reports each crossing once and a scroll within a month not at all', async () => {
    const onVisibleMonthChange = vi.fn<(monthKey: string) => void>();
    const { container } = renderView(onVisibleMonthChange);
    await waitFor(() => {
      expect(pinnedMonth(container)).not.toBeNull();
    });
    await settle();
    onVisibleMonthChange.mockClear();

    const start = scrollBody(container).scrollTop;
    await scrollTo(container, start + 2400);
    const first = pinnedMonth(container);

    // Coming to rest at the same offset: the view has not moved to
    // another month, so there is nothing further to report.
    await scrollTo(container, start + 2400);
    expect(onVisibleMonthChange).toHaveBeenCalledTimes(1);

    await scrollTo(container, start + 4800);
    const second = pinnedMonth(container);
    expect(second).not.toBe(first);
    expect(onVisibleMonthChange).toHaveBeenCalledTimes(2);
    expect(onVisibleMonthChange.mock.calls.map((c) => c[0])).toEqual([first, second]);
  });

  it('collapses a run of crossings into the month it came to rest on', async () => {
    const onVisibleMonthChange = vi.fn<(monthKey: string) => void>();
    const { container } = renderView(onVisibleMonthChange);
    await waitFor(() => {
      expect(pinnedMonth(container)).not.toBeNull();
    });
    await settle();
    onVisibleMonthChange.mockClear();

    // One long flick, delivered as the run of scroll notifications a
    // browser would send: several month boundaries go by without the
    // view ever standing still. Each is a fetch if it is reported.
    const start = scrollBody(container).scrollTop;
    const crossings: string[] = [];
    for (const step of [700, 1400, 2100, 2800]) {
      await flick(container, start + step);
      const month = pinnedMonth(container);
      if (month !== null && month !== crossings[crossings.length - 1]) crossings.push(month);
    }
    // Without at least two boundaries in the run there is nothing here
    // to collapse.
    expect(crossings.length).toBeGreaterThan(1);

    await settle();
    expect(onVisibleMonthChange).toHaveBeenCalledTimes(1);
    expect(onVisibleMonthChange).toHaveBeenCalledWith(pinnedMonth(container));
  });

  it('reports without a synchronous flush from inside a lifecycle method', async () => {
    const onVisibleMonthChange = vi.fn<(monthKey: string) => void>();
    const { container } = renderView(onVisibleMonthChange);
    await waitFor(() => {
      expect(pinnedMonth(container)).not.toBeNull();
    });
    await settle();

    await scrollTo(container, scrollBody(container).scrollTop + 2400);
    expect(onVisibleMonthChange).toHaveBeenCalled();
    expect(updateWarnings()).toEqual([]);
  });

  it('stays where it is when the month it reported arrives', async () => {
    const onVisibleMonthChange = vi.fn<(monthKey: string) => void>();
    const { container, rerender } = render(
      <Harness report={onVisibleMonthChange} tasksByDate={new Map()} />,
    );
    await waitFor(() => {
      expect(pinnedMonth(container)).not.toBeNull();
    });
    await settle();

    await scrollTo(container, scrollBody(container).scrollTop + 2400);
    const arrived = pinnedMonth(container);
    const restingAt = scrollBody(container).scrollTop;
    onVisibleMonthChange.mockClear();

    // The report moves the route's cursor, which refetches and hands
    // down new data. Answering a report by scrolling the view back would
    // make the two chase each other.
    rerender(<Harness report={onVisibleMonthChange} tasksByDate={new Map([['2026-01-01', []]])} />);
    await settle();

    expect(scrollBody(container).scrollTop).toBe(restingAt);
    expect(pinnedMonth(container)).toBe(arrived);
    expect(onVisibleMonthChange).not.toHaveBeenCalled();
  });
});
