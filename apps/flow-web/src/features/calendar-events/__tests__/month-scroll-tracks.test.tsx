/**
 * How a week row of the phone month view divides itself into tracks.
 *
 * A multi-day event is drawn as one bar across the columns it covers, so
 * every day of the row has to hold the same track free for it — which
 * makes the number of tracks a property of the row, not of a cell. What
 * these tests pin is that the row is nonetheless tall enough for the
 * chips its days carry: a week with no bar at all still draws its
 * single-day events, the cap still collapses the rest into a count, and
 * no day is given a track its neighbours are not.
 *
 * The test DOM lays nothing out and reports zero for every metric, so
 * the virtualizer is given the sizes a browser would report; told the
 * viewport is zero pixels tall it correctly renders nothing.
 */

import type { components } from '@nodate-flow/sdk';
import { Zone } from '@nodate-flow/ui/time';
import { render, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import MonthScroll from '../month-scroll';

// The suite runs without an i18n instance, and the overflow count is
// only readable if `t` interpolates it.
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, opts?: Record<string, unknown>) =>
      opts && 'count' in opts ? `${key}:${String(opts.count)}` : key,
    i18n: { resolvedLanguage: 'en', language: 'en' },
  }),
}));

type CalendarEvent = components['schemas']['MyCalendarEventResponse'];

const MS_PER_DAY = 86_400_000;
const VIEWPORT_PX = 640;
const ROW_PX = 112;
const PHONE_W = 390;

/** Mirrors `MAX_VISIBLE_TRACKS`; a cell never draws more than this many. */
const MAX_TRACKS = 3;

/**
 * A Wednesday. With `weekStart: 'mon'` the week the view opens on runs
 * from the 30th to the 5th, so a bar can sit on days the chips do not.
 */
const NOW = new Date(2026, 3, 1, 12, 0, 0);

/** The week the fixtures live in, Monday first. */
const MONDAY = new Date(2026, 2, 30);

/**
 * The fixtures are built from local wall clocks and the expected cell
 * keys are read back the same way, so the host zone keeps this file
 * about track layout rather than about day boundaries.
 */
const viewZone = Zone.browser();

/** `YYYY-MM-DD` in local time, matching the view's cell keys. */
function dayKey(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

/** Local noon on the `n`th day of the fixture week (0 = Monday). */
function dayOfWeek(n: number): Date {
  const d = new Date(MONDAY.getTime() + n * MS_PER_DAY);
  d.setHours(12, 0, 0, 0);
  return d;
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
    timezone: 'UTC',
    title,
    viewerAttending: false,
    visibility: 'default',
    workspaceId: 'ws-1',
    workspaceName: 'WS',
  } as CalendarEvent;
}

/**
 * Report the metrics a browser would: a scrollable viewport for the grid
 * and a fixed height for every row.
 */
function stubElementMetrics(): void {
  class ImmediateResizeObserver {
    constructor(private readonly cb: (entries: unknown[]) => void) {}
    observe(el: Element): void {
      queueMicrotask(() =>
        this.cb([{ target: el, contentRect: { width: PHONE_W, height: VIEWPORT_PX } }]),
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
    get: () => PHONE_W,
  });
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
      right: PHONE_W,
      bottom: height,
      width: PHONE_W,
      height,
      toJSON: () => ({}),
    } as DOMRect;
  });
  const offsets = new WeakMap<HTMLElement, number>();
  const setOffset = (el: HTMLElement, top: number): void => {
    offsets.set(el, Math.max(0, top));
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

/**
 * Whether an element carries a CSS-module class, which the bundler
 * renames to `_<local>_<hash>`. Matched on the whole leading segment so
 * a modifier (`bar--clipStart`) is not mistaken for its base.
 */
function hasStyle(el: Element, local: string): boolean {
  return [...el.classList].some((c) => c.startsWith(`_${local}_`));
}

function renderView(events: CalendarEvent[]): void {
  const noop = (): void => {};
  render(
    <MonthScroll
      events={events}
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

/** A rendered day column, once the week it belongs to is on screen. */
async function cell(key: string): Promise<HTMLElement> {
  let found: HTMLElement | undefined;
  await waitFor(() => {
    found = document.querySelector<HTMLElement>(`[data-cell-key="${key}"]`) ?? undefined;
    if (!found) throw new Error(`no cell for ${key}`);
  });
  if (!found) throw new Error(`no cell for ${key}`);
  return found;
}

/** The track column of a day cell: one child per track the row renders. */
function trackArea(el: HTMLElement): HTMLElement {
  const area = [...el.children].find((c) => hasStyle(c, 'trackArea'));
  if (!(area instanceof HTMLElement)) throw new Error('no track area');
  return area;
}

/** Titles of the single-day chips drawn in a day cell, in track order. */
function chipTitles(el: HTMLElement): string[] {
  return [...trackArea(el).children]
    .filter((c) => hasStyle(c, 'chip'))
    .map((c) => c.textContent ?? '');
}

/** The "+N" the cell shows for what it could not draw, or null. */
function overflowLabel(el: HTMLElement): string | null {
  const label = [...el.children].find((c) => hasStyle(c, 'more'));
  return label?.textContent ?? null;
}

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true, toFake: ['Date'] });
  vi.setSystemTime(NOW);
  stubElementMetrics();
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe('MonthScroll week tracks', () => {
  it('draws the single-day events of a week that has no multi-day bar', async () => {
    renderView([makeEvent('e1', dayOfWeek(2), 'Standup')]);

    const wednesday = await cell(dayKey(dayOfWeek(2)));
    expect(chipTitles(wednesday)).toEqual(['Standup']);
    expect(overflowLabel(wednesday)).toBeNull();
  });

  it('draws a bar and the chips on the days it does not cover', async () => {
    renderView([
      makeEvent('bar', dayOfWeek(0), 'Offsite', 2),
      makeEvent('e1', dayOfWeek(3), 'Standup'),
      makeEvent('e2', dayOfWeek(4), 'Retro'),
    ]);

    const thursday = await cell(dayKey(dayOfWeek(3)));
    expect(chipTitles(thursday)).toEqual(['Standup']);
    expect(chipTitles(await cell(dayKey(dayOfWeek(4))))).toEqual(['Retro']);

    // A bar is drawn rather than pressed, so it is not a button; the
    // class is what names it.
    const bars = [...document.querySelectorAll('[data-week] *')].filter((el) =>
      hasStyle(el, 'bar'),
    );
    expect(bars.map((el) => el.textContent)).toContain('Offsite');
  });

  it('leaves the track a bar holds free on the days it crosses', async () => {
    renderView([
      makeEvent('bar', dayOfWeek(0), 'Offsite', 3),
      makeEvent('e1', dayOfWeek(1), 'Standup'),
    ]);

    // The bar takes track 0 of every column it spans, so the chip on a
    // covered day starts at track 1 and the spacer above it holds the
    // bar's place.
    const tuesday = await cell(dayKey(dayOfWeek(1)));
    const tracks = [...trackArea(tuesday).children];
    expect(tracks.map((c) => hasStyle(c, 'trackSpacer'))).toEqual([true, false]);
    expect(tracks[1]?.textContent).toBe('Standup');
  });

  it('caps the tracks and counts what it could not draw', async () => {
    const titles = ['One', 'Two', 'Three', 'Four', 'Five'];
    renderView(titles.map((title, i) => makeEvent(`e${i}`, dayOfWeek(2), title)));

    const wednesday = await cell(dayKey(dayOfWeek(2)));
    expect(chipTitles(wednesday)).toEqual(titles.slice(0, MAX_TRACKS));
    expect(overflowLabel(wednesday)).toBe(`calendar.more:${titles.length - MAX_TRACKS}`);
  });

  it('counts a bar the cap pushed out of the row on every day it covers', async () => {
    // Four overlapping bars stack four tracks deep across the whole week;
    // the fourth has nowhere to go and is missing from each of its days.
    const events = [0, 1, 2, 3].map((i) => makeEvent(`bar${i}`, dayOfWeek(0), `Bar ${i}`, 5));
    renderView(events);

    for (const col of [0, 1, 2, 3, 4]) {
      const day = await cell(dayKey(dayOfWeek(col)));
      expect(overflowLabel(day)).toBe('calendar.more:1');
    }
  });

  it('gives every day of a row the same number of tracks', async () => {
    renderView([
      makeEvent('bar', dayOfWeek(0), 'Offsite', 2),
      makeEvent('e1', dayOfWeek(3), 'Standup'),
      makeEvent('e2', dayOfWeek(3), 'Retro'),
    ]);

    // Thursday is the busiest column with two chips and no bar over it,
    // so the whole row is two tracks tall — including the empty days.
    const counts: number[] = [];
    for (const col of [0, 1, 2, 3, 4, 5, 6]) {
      const day = await cell(dayKey(dayOfWeek(col)));
      counts.push(trackArea(day).children.length);
    }
    expect(counts).toEqual([2, 2, 2, 2, 2, 2, 2]);
  });

  it('shows nothing but the day number in a week with no events', async () => {
    renderView([]);

    const monday = await cell(dayKey(dayOfWeek(0)));
    expect(trackArea(monday).children).toHaveLength(0);
    expect(overflowLabel(monday)).toBeNull();
  });
});
