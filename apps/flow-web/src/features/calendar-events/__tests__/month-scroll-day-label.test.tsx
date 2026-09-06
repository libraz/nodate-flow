/**
 * What a day column of the phone month view announces.
 *
 * The column is the only control this view has, and everything drawn
 * inside it — chips, bars, task rows — is presentational. So its
 * accessible name is the whole of what a reader who cannot see the grid
 * gets: which day it is, and whether anything is on it. Both halves are
 * easy to lose quietly. The date was once interpolated as the raw
 * `YYYY-MM-DD` cell key, which a translation cannot fix from its side,
 * and the count is computed from the day rather than from what the row
 * had room to draw, so a cap collapsing entries into "+N" must not
 * change it.
 *
 * `t` is mocked to echo its interpolation values, since what is being
 * pinned here is what the component hands the translator, not what any
 * one language does with it.
 *
 * The test DOM lays nothing out and reports zero for every metric, so
 * the virtualizer is given the sizes a browser would report; told the
 * viewport is zero pixels tall it correctly renders nothing.
 */

import type { HolidayEntry } from '@nodate-flow/holidays';
import type { components } from '@nodate-flow/sdk';
import { Zone } from '@nodate-flow/ui/time';
import { render, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, opts?: Record<string, unknown>) =>
      opts === undefined ? key : `${key}(${JSON.stringify(opts)})`,
    i18n: { resolvedLanguage: 'en', language: 'en' },
  }),
}));

import MonthScroll from '../month-scroll';

type CalendarEvent = components['schemas']['MyCalendarEventResponse'];
type CalendarTask = components['schemas']['MyTaskListItem'];

const MS_PER_DAY = 86_400_000;
const VIEWPORT_PX = 640;
const ROW_PX = 112;
const PHONE_W = 390;

/** A Wednesday. With `weekStart: 'mon'` its week runs from the 30th. */
const NOW = new Date(2026, 3, 1, 12, 0, 0);
/** Monday of the fixture week. */
const MONDAY = new Date(2026, 2, 30);

/** The day under test, and what `Intl` calls it in `en`. */
const DAY = '2026-04-01';
const DAY_NAME = 'Wednesday, April 1, 2026';

/**
 * The fixtures are built from local wall clocks and the expected cell
 * keys are read back the same way, so the host zone keeps this file
 * about labels rather than about day boundaries.
 */
const viewZone = Zone.browser();

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

function makeTask(id: string, title: string): CalendarTask {
  return {
    actorRole: 'owner',
    createdAt: 0,
    derivedState: 'active',
    dueOn: DAY,
    id,
    priority: 1,
    projectId: 'p1',
    title,
    workspaceId: 'ws-1',
    workspaceName: 'WS',
  } as CalendarTask;
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

function renderView(options: {
  events?: CalendarEvent[];
  tasks?: CalendarTask[];
  holidays?: HolidayEntry[];
}): void {
  const noop = (): void => {};
  render(
    <MonthScroll
      events={options.events ?? []}
      tasksByDate={options.tasks ? new Map([[DAY, options.tasks]]) : new Map()}
      holidaysByDate={options.holidays ? new Map([[DAY, options.holidays]]) : new Map()}
      locale="en"
      weekStart="mon"
      zone={viewZone}
      stateColor={() => 'red'}
      scrollToTodaySignal={0}
      onDayOpen={noop}
    />,
  );
}

/** The accessible name of a day column, once its week is on screen. */
async function cellLabel(key: string): Promise<string> {
  let found: HTMLElement | null = null;
  await waitFor(() => {
    found = document.querySelector<HTMLElement>(`[data-cell-key="${key}"]`);
    if (!found) throw new Error(`no cell for ${key}`);
  });
  const label = (found as HTMLElement | null)?.getAttribute('aria-label');
  expect(label, `cell ${key} has no accessible name`).not.toBeNull();
  return label ?? '';
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

describe('MonthScroll day column label', () => {
  it('names the day in the reader locale, not by its cell key', async () => {
    renderView({});

    const label = await cellLabel(DAY);
    expect(label).toContain(DAY_NAME);
    // The regression a correct translation cannot repair from its side:
    // the raw key handed straight to `{date}` reads out as digits and
    // punctuation in every language.
    expect(label).not.toContain(DAY);
  });

  it('says how many entries the day holds, counting what a cap hid', async () => {
    // A bar crossing the day, a chip on it, and two tasks — four
    // entries, of which the cell draws at most three and the task list
    // at most two. The label answers for the day, not for the drawing.
    // The disagreement is the point, not a defect to reconcile: making
    // the label match the rendering would make it wrong, because the
    // reader who depends on the label is the one who cannot see the
    // rendering.
    renderView({
      events: [
        makeEvent('bar', dayOfWeek(0), 'Offsite', 3),
        makeEvent('chip', dayOfWeek(2), 'Standup'),
      ],
      tasks: [makeTask('t1', 'Ship the report'), makeTask('t2', 'Review the draft')],
    });

    expect(await cellLabel(DAY)).toContain('"count":4');
  });

  it('says a day is empty rather than leaving the count off', async () => {
    renderView({});

    expect(await cellLabel(DAY)).toContain('"count":0');
  });

  it('names the holiday, the day, and the count together', async () => {
    renderView({
      events: [makeEvent('chip', dayOfWeek(2), 'Standup')],
      holidays: [
        { date: DAY, name: 'Founding Day', localNames: { en: 'Founding Day' }, type: 'public' },
      ],
    });

    const label = await cellLabel(DAY);
    expect(label).toContain('calendar.month_scroll.day_label_holiday');
    expect(label).toContain(DAY_NAME);
    expect(label).toContain('"holiday":"Founding Day"');
    expect(label).toContain('"count":1');
  });

  it('keys the plain label off the absence of a holiday', async () => {
    renderView({});

    expect(await cellLabel(DAY)).toContain('calendar.month_scroll.day_label(');
  });
});
