/**
 * Moving an event to another day on the phone month view.
 *
 * The desktop grid and this view press the same gesture, but the phone
 * view is virtualized and scrolls through two years of weeks, so what
 * these tests pin is what only happens here: that a finger which held
 * still long enough moves an event and sends the shifted range, that one
 * which did not opens the day rather than the event — the pills here are
 * drag sources and nothing else — and that a drop in a month other than
 * the one the pill came from arrives with the right delta rather than
 * dying with the row it started in.
 *
 * The route is rendered at a phone width so it mounts the month scroll
 * instead of the grid. The test DOM lays nothing out and reports zero
 * for every metric, so the virtualizer is given the sizes a browser
 * would report and the day columns the geometry a drop can be
 * hit-tested against.
 */

import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { TOUCH_HOLD_MS } from '../../features/calendar-events/lib/pointer-drag';

/**
 * A Wednesday, deliberately the first of its month: the week the view
 * opens on then holds days of two months, and the row above it is a
 * third one, so a drag can cross a month boundary without scrolling.
 */
const NOW = new Date('2026-04-01T12:00:00Z');

const SOURCE = '2026-04-01';
const TARGET = '2026-04-03';
/** Monday of the same week: where the multi-day bar begins. */
const BAR_SOURCE = '2026-03-30';
const BAR_TARGET = '2026-04-03';
/** In the previous month and the previous week row. */
const CROSS_MONTH_TARGET = '2026-03-29';

/** Unix seconds for noon UTC on a `YYYY-MM-DD`. */
function noonAt(dayKey: string): number {
  return Math.floor(Date.parse(`${dayKey}T12:00:00Z`) / 1000);
}

interface StubEvent {
  id: string;
  calendarId: string;
  workspaceId: string;
  workspaceName: string;
  title: string;
  kind: string;
  startAt: number;
  endAt: number;
  timezone: string;
  allDay: boolean;
  attendeeCount: number;
  viewerAttending: boolean;
  showAs: string;
  visibility: string;
  flexibility: string;
  ownerUserId: string;
  createdAt: number;
  recurrenceRule?: { freq: string; interval: number };
}

function baseEvent(): StubEvent {
  return {
    id: 'evt-1',
    calendarId: 'cal-1',
    workspaceId: 'ws-1',
    workspaceName: 'WS',
    title: 'Design review',
    kind: 'event',
    startAt: noonAt(SOURCE),
    endAt: noonAt(SOURCE) + 3600,
    timezone: 'UTC',
    allDay: false,
    attendeeCount: 0,
    viewerAttending: false,
    showAs: 'busy',
    visibility: 'default',
    flexibility: 'fixed',
    ownerUserId: 'u1',
    createdAt: 0,
  };
}

/**
 * A two-day event, which lays out as a bar across the week.
 *
 * It sits on the two days before the source, so the source's own column
 * is free and a chip there shares the row with a bar rather than being
 * pushed below it.
 */
function spanningEvent(): StubEvent {
  return {
    ...baseEvent(),
    id: 'evt-span',
    title: 'Offsite',
    startAt: noonAt(BAR_SOURCE),
    endAt: noonAt('2026-03-31'),
  };
}

/** Repeats yearly, so exactly one occurrence falls in the window. */
function repeatingEvent(): StubEvent {
  return {
    ...baseEvent(),
    id: 'evt-2',
    title: 'Kickoff',
    recurrenceRule: { freq: 'yearly', interval: 1 },
  };
}

const mocks = vi.hoisted(() => ({
  events: [] as unknown[],
  updateMutate: vi.fn(),
  updatePending: false,
  toastShow: vi.fn(),
}));

vi.mock('@tanstack/react-router', () => ({
  createFileRoute:
    () =>
    (options: {
      component: () => ReactElement;
    }): { options: { component: () => ReactElement } } => ({
      options,
    }),
  useNavigate: () => vi.fn(),
  Link: ({
    children,
    ...rest
  }: { children: ReactNode } & Record<string, unknown>): ReactElement => (
    <a href="#task" {...(rest as object)}>
      {children}
    </a>
  ),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
    i18n: { resolvedLanguage: 'en', language: 'en' },
  }),
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: (...args: unknown[]) => mocks.toastShow(...args) },
}));

vi.mock('@nodate-flow/holidays', () => ({
  getOrCreateProvider: () => ({ holidaysBetween: () => [] }),
}));

vi.mock('../../lib/api', () => ({
  apiRequest: async (fn: (client: unknown) => unknown): Promise<unknown> => {
    let path = '';
    const record = (p: string): { data: undefined } => {
      path = p;
      return { data: undefined };
    };
    await fn({ GET: record, PATCH: record, POST: record });
    if (path === '/me/tasks-with-dates') return { tasks: [] };
    if (path === '/me/calendar-events') return { events: mocks.events };
    if (path === '/workspaces/{wsId}/projects') return { projects: [] };
    return {};
  },
}));

vi.mock('../../features/settings/api', () => ({
  useMeQuery: () => ({
    data: {
      id: 'u1',
      country: null,
      timezone: 'UTC',
      weekStart: 'mon',
      locale: 'en',
    },
  }),
}));

vi.mock('../../features/workspaces/api', () => ({
  useWorkspacesQuery: () => ({
    data: [{ id: 'ws-1', name: 'WS', country: null, timezone: 'UTC' }],
  }),
}));

vi.mock('../../lib/use-current-workspace', () => ({
  useActiveWorkspaceId: () => 'ws-1',
}));

vi.mock('../../features/calendar-events/api', () => ({
  useUpdateEvent: () => ({ mutate: mocks.updateMutate, isPending: mocks.updatePending }),
}));

vi.mock('../../features/calendar-events/event-dialog', () => ({
  default: (): ReactElement => <div data-testid="event-dialog" />,
}));

vi.mock('../../features/calendars-rail/calendars-rail', () => ({
  default: (): ReactElement => <div />,
}));

vi.mock('../../features/calendar-invites/pending-invites-panel', () => ({
  default: (): ReactElement => <div />,
}));

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { Route } from '../_authenticated.calendar';

const PHONE_W = 390;
const VIEWPORT_PX = 640;
const ROW_PX = 112;
const CELL_W = 100;
const CELL_H = 80;
/** Two columns wide, so a press in its second half grabs the second day. */
const BAR_W = 200;

/**
 * Whether an element carries a CSS-module class, which the bundler
 * renames to `_<local>_<hash>`. Matched on the whole leading segment so
 * a modifier (`bar--clipStart`) is not mistaken for its base.
 */
function hasStyle(el: Element, local: string): boolean {
  return [...el.classList].some((c) => c.startsWith(`_${local}_`));
}

function rectOf(left: number, width: number, height: number): DOMRect {
  return {
    x: left,
    y: 0,
    left,
    right: left + width,
    top: 0,
    bottom: height,
    width,
    height,
    toJSON: () => ({}),
  } as DOMRect;
}

/**
 * Report the metrics a browser would: a scrollable viewport for the
 * month body, a fixed height for every row the virtualizer measures, and
 * a column of its own for each day cell so exactly one of them is under
 * the pointer at any x.
 */
function stubMetrics(): void {
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

  const isScrollport = (el: Element): boolean => el.getAttribute('role') === 'grid';
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

  const columnOf = new Map<string, number>();
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (
    this: Element,
  ): DOMRect {
    const key = this.getAttribute?.('data-cell-key');
    if (key !== null && key !== undefined) {
      let column = columnOf.get(key);
      if (column === undefined) {
        column = columnOf.size;
        columnOf.set(key, column);
      }
      return rectOf(column * CELL_W, CELL_W, CELL_H);
    }
    if (isScrollport(this)) return rectOf(0, PHONE_W, VIEWPORT_PX);
    if (hasStyle(this, 'bar')) return rectOf(0, BAR_W, 16);
    return rectOf(0, PHONE_W, ROW_PX);
  });
}

function cellElement(cellKey: string): HTMLElement {
  const cell = document.querySelector<HTMLElement>(`[data-cell-key="${cellKey}"]`);
  if (!cell) throw new Error(`no cell for ${cellKey}`);
  return cell;
}

/** The horizontal centre of a rendered day column. */
function centreOf(cellKey: string): number {
  return cellElement(cellKey).getBoundingClientRect().left + CELL_W / 2;
}

function renderCalendar(): void {
  const Page = Route.options.component as () => ReactElement;
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={qc}>
      <Page />
    </QueryClientProvider>,
  );
}

/** A single-day chip, once the query settles and the row is rendered. */
async function chip(title: string): Promise<HTMLElement> {
  return pillWithClass('chip', title);
}

/** A multi-day bar. */
async function bar(title: string): Promise<HTMLElement> {
  return pillWithClass('bar', title);
}

/**
 * A pill is drawn rather than pressed — it is a drag source and nothing
 * else, so it is not a button — and the class is what names it.
 */
async function pillWithClass(local: string, title: string): Promise<HTMLElement> {
  let found: HTMLElement | undefined;
  await waitFor(() => {
    found = [...document.querySelectorAll<HTMLElement>('[data-week] *')].find(
      (el) => hasStyle(el, local) && (el.textContent?.includes(title) ?? false),
    );
    if (!found) throw new Error(`no ${local} for ${title}`);
  });
  if (!found) throw new Error(`no ${local} for ${title}`);
  return found;
}

/**
 * Press, hold past the threshold, move to another day, release — the
 * gesture a finger produces.
 */
function touchDrag(pill: HTMLElement, fromX: number, toKey: string): void {
  fireEvent.pointerDown(pill, {
    pointerId: 1,
    pointerType: 'touch',
    clientX: fromX,
    clientY: 10,
  });
  act(() => {
    vi.advanceTimersByTime(TOUCH_HOLD_MS + 10);
  });
  fireEvent.pointerMove(window, {
    pointerId: 1,
    pointerType: 'touch',
    clientX: centreOf(toKey),
    clientY: 40,
  });
  fireEvent.pointerUp(window, {
    pointerId: 1,
    pointerType: 'touch',
    clientX: centreOf(toKey),
    clientY: 40,
  });
}

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true, toFake: ['setTimeout', 'clearTimeout', 'Date'] });
  vi.setSystemTime(NOW);
  mocks.events = [];
  mocks.updatePending = false;
  mocks.updateMutate.mockReset();
  mocks.toastShow.mockReset();
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: PHONE_W });
  stubMetrics();
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe('month scroll pointer drag', () => {
  it('renders the phone month view, not the desktop grid', async () => {
    mocks.events = [spanningEvent()];
    renderCalendar();
    await bar('Offsite');
    expect(document.querySelector('[role="grid"]')).not.toBeNull();
  });

  it('moves an event a finger held still on, and sends the shifted range', async () => {
    // Alongside a bar, so the chip the gesture picks up is one sharing
    // its row with a multi-day event rather than one standing alone.
    mocks.events = [spanningEvent(), baseEvent()];
    renderCalendar();
    const pill = await chip('Design review');

    touchDrag(pill, centreOf(SOURCE), TARGET);

    await waitFor(() => {
      expect(mocks.updateMutate).toHaveBeenCalledTimes(1);
    });
    const [vars] = mocks.updateMutate.mock.calls[0] as [
      { eventId: string; body: Record<string, unknown> },
    ];
    expect(vars.eventId).toBe('evt-1');
    expect(vars.body.startAt).toBe(noonAt(TARGET));
    expect(vars.body.endAt).toBe(noonAt(TARGET) + 3600);
    // A row that does not repeat has no occurrences to choose between.
    expect(vars.body.scope).toBeUndefined();

    // The release that ended the drag reaches the pill as a click, which
    // bubbles to the day cell. It must not also open anything on top of
    // the move.
    fireEvent.click(pill);
    expect(screen.queryByTestId('event-dialog')).toBeNull();
    expect(screen.queryByTestId('day-detail-sheet')).toBeNull();
  });

  it('opens the day when a finger lets go before the hold', async () => {
    mocks.events = [baseEvent()];
    renderCalendar();
    const pill = await chip('Design review');

    fireEvent.pointerDown(pill, {
      pointerId: 2,
      pointerType: 'touch',
      clientX: centreOf(SOURCE),
      clientY: 10,
    });
    act(() => {
      vi.advanceTimersByTime(TOUCH_HOLD_MS - 100);
    });
    fireEvent.pointerUp(window, {
      pointerId: 2,
      pointerType: 'touch',
      clientX: centreOf(SOURCE),
      clientY: 10,
    });
    fireEvent.click(pill);

    // A tap that lands on a pill is a tap on the day under it: the pill
    // is 18px tall, which is not a target a finger can aim at.
    expect(await screen.findByTestId('day-detail-sheet')).toBeTruthy();
    expect(screen.queryByTestId('event-dialog')).toBeNull();
    expect(mocks.updateMutate).not.toHaveBeenCalled();
  });

  it('abandons the press when the finger pans instead of holding', async () => {
    mocks.events = [baseEvent()];
    renderCalendar();
    const pill = await chip('Design review');

    fireEvent.pointerDown(pill, {
      pointerId: 3,
      pointerType: 'touch',
      clientX: centreOf(SOURCE),
      clientY: 10,
    });
    // Moving further than the slop during the hold is a scroll, and the
    // gesture hands the press back to the browser.
    fireEvent.pointerMove(window, {
      pointerId: 3,
      pointerType: 'touch',
      clientX: centreOf(SOURCE),
      clientY: 60,
    });
    act(() => {
      vi.advanceTimersByTime(TOUCH_HOLD_MS + 10);
    });
    fireEvent.pointerMove(window, {
      pointerId: 3,
      pointerType: 'touch',
      clientX: centreOf(TARGET),
      clientY: 60,
    });
    fireEvent.pointerUp(window, {
      pointerId: 3,
      pointerType: 'touch',
      clientX: centreOf(TARGET),
      clientY: 60,
    });

    expect(mocks.updateMutate).not.toHaveBeenCalled();
  });

  it('asks a repeating event which occurrences the move reaches', async () => {
    mocks.events = [repeatingEvent()];
    renderCalendar();
    const pill = await chip('Kickoff');

    touchDrag(pill, centreOf(SOURCE), TARGET);

    // Nothing is sent until the question is answered.
    expect(mocks.updateMutate).not.toHaveBeenCalled();
    const confirm = await screen.findByText('recurrence.scope.save.confirm');
    fireEvent.click(confirm);

    await waitFor(() => {
      expect(mocks.updateMutate).toHaveBeenCalledTimes(1);
    });
    const [vars] = mocks.updateMutate.mock.calls[0] as [{ body: Record<string, unknown> }];
    // The dialog defaults to the single occurrence, named by the start
    // the drag picked up rather than by where it landed.
    expect(vars.body.scope).toBe('occurrence');
    expect(vars.body.occurrenceStart).toBe(noonAt(SOURCE));
    expect(vars.body.startAt).toBe(noonAt(TARGET));
  });

  it('sends nothing when the scope question is dismissed', async () => {
    mocks.events = [repeatingEvent()];
    renderCalendar();
    const pill = await chip('Kickoff');

    touchDrag(pill, centreOf(SOURCE), TARGET);

    const cancel = await screen.findByText('recurrence.scope.cancel');
    fireEvent.click(cancel);

    await waitFor(() => {
      expect(screen.queryByText('recurrence.scope.save.confirm')).toBeNull();
    });
    expect(mocks.updateMutate).not.toHaveBeenCalled();
    // The pill never left the day it was drawn on.
    expect(cellElement(SOURCE).textContent).toContain('Kickoff');
  });

  it('leaves the pill where it was and says why when the move is refused', async () => {
    mocks.events = [baseEvent()];
    mocks.updateMutate.mockImplementation(
      (_vars: unknown, opts: { onError?: (err: unknown) => void }) => {
        opts.onError?.(new Error('calendar is read-only'));
      },
    );
    renderCalendar();
    const pill = await chip('Design review');

    touchDrag(pill, centreOf(SOURCE), TARGET);

    await waitFor(() => {
      expect(mocks.toastShow).toHaveBeenCalled();
    });
    expect(mocks.toastShow).toHaveBeenCalledWith(
      expect.objectContaining({ tone: 'danger', message: 'calendar is read-only' }),
    );
    // Nothing moved before the server agreed, so there is nothing to put
    // back: the pill is still drawn on the day the drag started from.
    expect(cellElement(SOURCE).textContent).toContain('Design review');
    expect(cellElement(TARGET).textContent).not.toContain('Design review');
  });

  it('moves an event dropped in the month above the one it came from', async () => {
    mocks.events = [baseEvent()];
    renderCalendar();
    const pill = await chip('Design review');

    // The target is in the previous month and in the row above, which
    // the virtualizer has rendered as overscan.
    touchDrag(pill, centreOf(SOURCE), CROSS_MONTH_TARGET);

    await waitFor(() => {
      expect(mocks.updateMutate).toHaveBeenCalledTimes(1);
    });
    const [vars] = mocks.updateMutate.mock.calls[0] as [{ body: Record<string, unknown> }];
    expect(vars.body.startAt).toBe(noonAt(CROSS_MONTH_TARGET));
  });

  it('keeps the row a drag started in mounted after the view has scrolled past it', async () => {
    mocks.events = [baseEvent()];
    renderCalendar();
    const pill = await chip('Design review');

    fireEvent.pointerDown(pill, {
      pointerId: 9,
      pointerType: 'touch',
      clientX: centreOf(SOURCE),
      clientY: 10,
    });
    act(() => {
      vi.advanceTimersByTime(TOUCH_HOLD_MS + 10);
    });

    // Months away — far outside the window the virtualizer renders, so
    // the source row would be recycled if the drag did not hold it.
    const scroller = document.querySelector<HTMLElement>('[role="grid"]');
    if (!scroller) throw new Error('no scroll body');
    const before = scroller.scrollTop;
    act(() => {
      scroller.scrollTop = before + 4000;
    });

    await waitFor(() => {
      // The week after the source has been recycled: the view renders
      // the rows around its new position, not around this one.
      expect(document.querySelector('[data-cell-key="2026-04-08"]')).toBeNull();
    });
    // The source row survived that, pill and all, and is still a drop
    // target — held in the range for as long as the drag lasts.
    expect(cellElement(SOURCE).textContent).toContain('Design review');

    fireEvent.pointerUp(window, {
      pointerId: 9,
      pointerType: 'touch',
      clientX: centreOf(SOURCE),
      clientY: 10,
    });
    // Released over the day it came from, so there is nothing to move —
    // but the gesture reached its end instead of dying with the row.
    expect(mocks.updateMutate).not.toHaveBeenCalled();
  });

  it('moves a multi-day bar by the distance it travelled, not by where it starts', async () => {
    mocks.events = [spanningEvent()];
    renderCalendar();
    const pill = await bar('Offsite');

    // Grabbed in its second half — the second of the two days it covers,
    // 2026-03-31 — so a drop on the 3rd is a three-day move, and the
    // whole range travels that far rather than landing on the target.
    touchDrag(pill, BAR_W * 0.75, BAR_TARGET);

    await waitFor(() => {
      expect(mocks.updateMutate).toHaveBeenCalledTimes(1);
    });
    const [vars] = mocks.updateMutate.mock.calls[0] as [{ body: Record<string, unknown> }];
    expect(vars.body.startAt).toBe(noonAt('2026-04-02'));
    expect(vars.body.endAt).toBe(noonAt('2026-04-03'));
  });
});
