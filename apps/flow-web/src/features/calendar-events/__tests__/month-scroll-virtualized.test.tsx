/**
 * The mobile month view renders two years of week rows, but only the
 * ones near the viewport are in the DOM.
 *
 * What must survive virtualizing it: the scroll range still covers every
 * week in the range, the view still opens on today's week, the pinned
 * month header still names the month at the top, and a rendered row
 * still shows the events that belong to it. These tests drive the real
 * component with the element metrics a browser would report, since the
 * test DOM reports zero for all of them and a virtualizer told the
 * viewport is zero pixels tall correctly renders nothing.
 */

import type { components } from '@nodate-flow/sdk';
import { Zone } from '@nodate-flow/ui/time';
import { render, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import MonthScroll from '../month-scroll';

type CalendarEvent = components['schemas']['MyCalendarEventResponse'];

const MS_PER_DAY = 86_400_000;
const VIEWPORT_PX = 640;
const ROW_PX = 112;
const TZ = 'UTC';

/**
 * The fixtures are built from local wall clocks and the expected cell
 * keys are read back the same way, so the host zone keeps this file
 * about virtualisation rather than about day boundaries.
 */
const viewZone = Zone.browser();

/** `YYYY-MM-DD` in local time, matching the view's cell keys. */
function dayKey(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

/** @param spanDays Calendar days the event covers; 1 is a single-day event. */
function makeEvent(id: string, start: Date, title: string, spanDays = 1): CalendarEvent {
  return {
    allDay: false,
    attendeeCount: 0,
    calendarId: 'cal-1',
    createdAt: 0,
    endAt: Math.floor((start.getTime() + (spanDays - 1) * MS_PER_DAY + 3_600_000) / 1000),
    flexibility: 'fixed',
    id,
    kind: 'event',
    ownerUserId: 'u1',
    showAs: 'busy',
    startAt: Math.floor(start.getTime() / 1000),
    timezone: TZ,
    title,
    viewerAttending: false,
    visibility: 'default',
    workspaceId: 'ws-1',
    workspaceName: 'WS',
  } as CalendarEvent;
}

/**
 * Report the metrics a browser would: a scrollable viewport for the grid
 * and a fixed height for every row. Without these the test DOM reports
 * zero and nothing is ever considered visible.
 */
function stubElementMetrics(): void {
  class ImmediateResizeObserver {
    constructor(private readonly cb: (entries: unknown[]) => void) {}
    observe(el: Element): void {
      // Deferred for the same reason as the scroll event above.
      queueMicrotask(() =>
        this.cb([{ target: el, contentRect: { width: 390, height: VIEWPORT_PX } }]),
      );
    }
    unobserve(): void {}
    disconnect(): void {}
  }
  vi.stubGlobal('ResizeObserver', ImmediateResizeObserver);
  const isScrollport = (el: HTMLElement): boolean => el.getAttribute('role') === 'grid';
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
    configurable: true,
    get(this: HTMLElement) {
      return isScrollport(this) ? VIEWPORT_PX : ROW_PX;
    },
  });
  Object.defineProperty(HTMLElement.prototype, 'offsetWidth', {
    configurable: true,
    get: () => 390,
  });
  // The virtualizer clamps every scroll target to scrollHeight -
  // clientHeight. The test DOM reports both as zero, which clamps every
  // target to the top and would make a correct "scroll to today" look
  // like it never moved.
  Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
    configurable: true,
    get(this: HTMLElement) {
      return isScrollport(this) ? VIEWPORT_PX : ROW_PX;
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
    const height = isScrollport(this as HTMLElement) ? VIEWPORT_PX : ROW_PX;
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
  // A scroll container that actually scrolls: the test DOM accepts
  // scrollTo and then reports scrollTop as 0 forever, so a virtualizer
  // that scrolled correctly would still look like it never moved.
  const offsets = new WeakMap<HTMLElement, number>();
  const setOffset = (el: HTMLElement, top: number): void => {
    offsets.set(el, Math.max(0, top));
    // Browsers report a scroll after the fact, never inside the call
    // that caused it. Dispatching synchronously would have React
    // re-render from within a layout effect.
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

const today = new Date();
today.setHours(0, 0, 0, 0);
const todayEvent = makeEvent('today-evt', new Date(today.getTime() + 9 * 3_600_000), 'Standup');
// A second event two months out, far outside the rendered window.
const farEvent = makeEvent('far-evt', new Date(today.getTime() + 60 * MS_PER_DAY), 'Offsite');

function renderView(): ReturnType<typeof render> {
  const noop = (): void => {};
  return render(
    <MonthScroll
      events={[todayEvent, farEvent]}
      tasksByDate={new Map()}
      holidaysByDate={new Map()}
      locale="en"
      weekStart="mon"
      zone={viewZone}
      stateColor={() => 'red'}
      scrollToTodaySignal={0}
      onDayCreate={noop}
      onEventOpen={noop}
      onTaskOpen={noop}
    />,
  );
}

beforeEach(() => {
  stubElementMetrics();
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('MonthScroll virtualization', () => {
  it('keeps the full two-year scroll range', () => {
    const { container } = renderView();
    const inner = container.querySelector<HTMLElement>('[style*="block-size"]');
    // 12 months either side of today, week rows plus a header per month.
    // The exact pixel total moves with the estimates; what matters is
    // that it is the whole range and not just what is on screen.
    const total = Number.parseInt(inner?.style.blockSize ?? '0', 10);
    expect(total).toBeGreaterThan(100 * ROW_PX);
  });

  it('mounts only the rows near the viewport', () => {
    const { container } = renderView();
    const rows = container.querySelectorAll('[data-week]').length;
    expect(rows).toBeGreaterThan(0);
    // A viewport this tall holds about six rows; the previous
    // implementation mounted all 109 of them.
    expect(rows).toBeLessThan(20);
  });

  it("opens on today's week", async () => {
    const { container } = renderView();
    // The row holding today is among the ones rendered at rest.
    await waitFor(() => {
      expect(container.querySelector(`[data-cell-key="${dayKey(today)}"]`)).not.toBeNull();
    });
  });

  it('shows a rendered row the events that belong to it', async () => {
    const { container } = renderView();
    await waitFor(() => {
      expect(container.textContent).toContain('Standup');
    });
    // The far event belongs to a row that is not on screen, and the
    // grouping must not leak it into one that is.
    expect(container.textContent).not.toContain('Offsite');
  });

  it('pins one month header at the top', () => {
    const { container } = renderView();
    const headers = [...container.querySelectorAll<HTMLElement>('[data-month]')];
    const pinned = headers.filter((el) => !el.style.transform);
    expect(pinned).toHaveLength(1);
  });
});
