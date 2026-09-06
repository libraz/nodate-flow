/**
 * Opening a day from the phone month view.
 *
 * The month grid packs its chips small enough that a whole week stays
 * legible, which is below a usable touch target, so nothing in the grid
 * is operated on directly any more: a tap anywhere in a day column —
 * including on a chip — opens that day in a sheet, and the sheet is
 * where an event or a task is opened and where a new event is created.
 *
 * What these pin is the part a reader can only get wrong once: that the
 * tap no longer creates an event behind their back, that a chip does not
 * swallow the tap, that the sheet lists a day in the order it promises,
 * and that the gesture the chips kept — dragging one onto another day —
 * still moves the event now that their click path is gone.
 *
 * The route is rendered at a phone width so it mounts the month scroll
 * instead of the grid. The test DOM lays nothing out and reports zero
 * for every metric, so the virtualizer is given the sizes a browser
 * would report and the day columns the geometry a drop can be
 * hit-tested against.
 */

import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { TOUCH_HOLD_MS } from '../../features/calendar-events/lib/pointer-drag';

/** A Wednesday; the day every fixture is filed under. */
const NOW = new Date('2026-04-01T12:00:00Z');

const DAY = '2026-04-01';
/** Another day in the same week, for the drag target. */
const OTHER_DAY = '2026-04-03';
/** A day nothing is filed under. */
const EMPTY_DAY = '2026-04-02';
/** The two days a multi-day bar covers, in the same week as the rest. */
const BAR_FIRST_DAY = '2026-03-30';
const BAR_SECOND_DAY = '2026-03-31';

/** Unix seconds for a wall clock on a `YYYY-MM-DD`, read in UTC. */
function utcAt(dayKey: string, hhmm: string): number {
  return Math.floor(Date.parse(`${dayKey}T${hhmm}:00Z`) / 1000);
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
}

function timedEvent(id: string, title: string, hhmm: string): StubEvent {
  return {
    id,
    calendarId: 'cal-1',
    workspaceId: 'ws-1',
    workspaceName: 'WS',
    title,
    kind: 'event',
    startAt: utcAt(DAY, hhmm),
    endAt: utcAt(DAY, hhmm) + 3600,
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

/** An all-day row, stored at midnight UTC the way the API normalises it. */
function allDayEvent(id: string, title: string): StubEvent {
  return {
    ...timedEvent(id, title, '00:00'),
    id,
    title,
    allDay: true,
    startAt: utcAt(DAY, '00:00'),
    endAt: utcAt(DAY, '00:00'),
  };
}

/**
 * A two-day event, which lays out as a bar across two columns of the
 * week — so a press in its second half lands on a different day from the
 * one it begins on.
 */
function spanningEvent(): StubEvent {
  return {
    ...timedEvent('evt-span', 'Offsite', '12:00'),
    startAt: utcAt(BAR_FIRST_DAY, '12:00'),
    endAt: utcAt(BAR_SECOND_DAY, '12:00'),
  };
}

interface StubTask {
  id: string;
  title: string;
  dueOn: string;
  derivedState: string;
  priority: number;
  workspaceId: string;
  workspaceName: string;
  projectId: string;
  actorRole: string;
  createdAt: number;
}

function task(id: string, title: string): StubTask {
  return {
    id,
    title,
    dueOn: DAY,
    derivedState: 'active',
    priority: 1,
    workspaceId: 'ws-1',
    workspaceName: 'WS',
    projectId: 'p1',
    actorRole: 'owner',
    createdAt: 0,
  };
}

/**
 * A day carrying one of everything the sheet orders: an all-day row, two
 * timed rows that are deliberately out of clock order in the payload, and
 * a task. A day with one event, or with events all of one kind, cannot
 * fail an ordering assertion.
 */
function fullDay(): { events: StubEvent[]; tasks: StubTask[] } {
  return {
    events: [
      timedEvent('evt-late', 'Retro', '15:00'),
      allDayEvent('evt-allday', 'Company holiday'),
      timedEvent('evt-early', 'Standup', '09:00'),
    ],
    tasks: [task('task-1', 'Ship the report')],
  };
}

const mocks = vi.hoisted(() => ({
  events: [] as unknown[],
  tasks: [] as unknown[],
  updateMutate: vi.fn(),
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
    t: (key: string, opts?: Record<string, unknown>) =>
      opts && 'count' in opts ? `${key}:${String(opts.count)}` : key,
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
    if (path === '/me/tasks-with-dates') return { tasks: mocks.tasks };
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
  useUpdateEvent: () => ({ mutate: mocks.updateMutate, isPending: false }),
}));

/**
 * The dialog reports the mode it was opened in, so a test can tell an
 * edit from a create and read the day a create was seeded with.
 */
vi.mock('../../features/calendar-events/event-dialog', () => ({
  default: ({ mode }: { mode: { kind: string; date?: string } }): ReactElement => (
    <div data-testid="event-dialog" data-mode={mode.kind} data-date={mode.date ?? ''} />
  ),
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
/** Two columns wide, so a press in its second half lands on the second day. */
const BAR_W = 200;

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

/**
 * Whether an element carries a CSS-module class, which the bundler
 * renames to `_<local>_<hash>`. Matched on the whole leading segment so
 * a modifier (`bar--clipStart`) is not mistaken for its base.
 */
function hasStyle(el: Element, local: string): boolean {
  return [...el.classList].some((c) => c.startsWith(`_${local}_`));
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

function cellElement(cellKey: string): HTMLElement {
  const cell = document.querySelector<HTMLElement>(`[data-cell-key="${cellKey}"]`);
  if (!cell) throw new Error(`no cell for ${cellKey}`);
  return cell;
}

/** The horizontal centre of a rendered day column. */
function centreOf(cellKey: string): number {
  return cellElement(cellKey).getBoundingClientRect().left + CELL_W / 2;
}

/** A day column, once the query has settled and its week is rendered. */
async function cell(cellKey: string): Promise<HTMLElement> {
  let found: HTMLElement | null = null;
  await waitFor(() => {
    found = document.querySelector<HTMLElement>(`[data-cell-key="${cellKey}"]`);
    if (!found) throw new Error(`no cell for ${cellKey}`);
  });
  if (!found) throw new Error(`no cell for ${cellKey}`);
  return found;
}

/** A single-day chip drawn inside a day column. */
async function chip(title: string): Promise<HTMLElement> {
  let found: HTMLElement | undefined;
  await waitFor(() => {
    found = [...document.querySelectorAll<HTMLElement>('[data-cell-key] *')].find(
      (el) => hasStyle(el, 'chip') && (el.textContent?.includes(title) ?? false),
    );
    if (!found) throw new Error(`no chip for ${title}`);
  });
  if (!found) throw new Error(`no chip for ${title}`);
  return found;
}

/** A multi-day bar, which is drawn in an overlay above the day columns. */
async function bar(title: string): Promise<HTMLElement> {
  let found: HTMLElement | undefined;
  await waitFor(() => {
    found = [...document.querySelectorAll<HTMLElement>('[data-week] *')].find(
      (el) => hasStyle(el, 'bar') && (el.textContent?.includes(title) ?? false),
    );
    if (!found) throw new Error(`no bar for ${title}`);
  });
  if (!found) throw new Error(`no bar for ${title}`);
  return found;
}

/** The open day sheet. */
async function sheet(): Promise<HTMLElement> {
  return await screen.findByTestId('day-detail-sheet');
}

/** Row labels of the open sheet, in the order it drew them. */
async function sheetRows(): Promise<string[]> {
  const rows = within(await sheet()).getAllByRole('button');
  return rows.map((el) => el.textContent ?? '');
}

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true, toFake: ['setTimeout', 'clearTimeout', 'Date'] });
  vi.setSystemTime(NOW);
  mocks.events = [];
  mocks.tasks = [];
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

describe('month scroll day sheet', () => {
  it('opens the day rather than creating an event when a cell is tapped', async () => {
    renderCalendar();

    fireEvent.click(await cell(EMPTY_DAY));

    await sheet();
    // The gesture the cell used to carry. Creating an event from a tap
    // meant to look at a day is the thing this replaces.
    expect(screen.queryByTestId('event-dialog')).toBeNull();
  });

  it('opens the day when the tap lands on a chip', async () => {
    mocks.events = [timedEvent('evt-early', 'Standup', '09:00')];
    renderCalendar();

    fireEvent.click(await chip('Standup'));

    await sheet();
    expect(screen.queryByTestId('event-dialog')).toBeNull();
  });

  it('lists all-day rows, then timed rows in clock order, then tasks', async () => {
    const day = fullDay();
    mocks.events = day.events;
    mocks.tasks = day.tasks;
    renderCalendar();

    fireEvent.click(await cell(DAY));

    const rows = await sheetRows();
    // The create button is the last control in the sheet, so the four
    // rows before it are the whole day and nothing is being sliced off.
    expect(rows).toEqual([
      expect.stringContaining('Company holiday'),
      expect.stringContaining('Standup'),
      expect.stringContaining('Retro'),
      expect.stringContaining('Ship the report'),
      expect.stringContaining('calendar.day_detail.create'),
    ]);
  });

  it('says so for a day with nothing on it', async () => {
    renderCalendar();

    fireEvent.click(await cell(EMPTY_DAY));

    expect(within(await sheet()).getByText('calendar.day_detail.empty')).toBeTruthy();
  });

  it('opens an event from its row', async () => {
    mocks.events = [timedEvent('evt-early', 'Standup', '09:00')];
    renderCalendar();

    fireEvent.click(await cell(DAY));
    const row = within(await sheet()).getByText('Standup');
    fireEvent.click(row);

    const dialog = await screen.findByTestId('event-dialog');
    expect(dialog.getAttribute('data-mode')).toBe('edit');
  });

  it('creates an event on the day it was opened from', async () => {
    renderCalendar();

    fireEvent.click(await cell(EMPTY_DAY));
    fireEvent.click(within(await sheet()).getByText('calendar.day_detail.create'));

    const dialog = await screen.findByTestId('event-dialog');
    // Exactly what tapping the cell used to produce: a create seeded
    // with that day, and the event kind rather than a block.
    expect(dialog.getAttribute('data-mode')).toBe('create');
    expect(dialog.getAttribute('data-date')).toBe(EMPTY_DAY);
  });

  it('opens the day a multi-day bar was tapped over, not the day it begins on', async () => {
    mocks.events = [spanningEvent()];
    renderCalendar();
    const pill = await bar('Offsite');

    // The bar floats in an overlay above the columns, so this press
    // reaches no cell to bubble from and has to resolve the day itself.
    // Tapped in its second half — the second of the two days it covers.
    fireEvent.click(pill, { clientX: BAR_W * 0.75, clientY: 10 });

    const header = within(await sheet()).getByText(/2026/);
    expect(header.textContent).toBe('Tuesday, March 31, 2026');
  });

  it('still moves an event dragged from a chip onto another day', async () => {
    mocks.events = [timedEvent('evt-early', 'Standup', '09:00')];
    renderCalendar();
    const pill = await chip('Standup');

    fireEvent.pointerDown(pill, {
      pointerId: 1,
      pointerType: 'touch',
      clientX: centreOf(DAY),
      clientY: 10,
    });
    act(() => {
      vi.advanceTimersByTime(TOUCH_HOLD_MS + 10);
    });
    fireEvent.pointerMove(window, {
      pointerId: 1,
      pointerType: 'touch',
      clientX: centreOf(OTHER_DAY),
      clientY: 40,
    });
    fireEvent.pointerUp(window, {
      pointerId: 1,
      pointerType: 'touch',
      clientX: centreOf(OTHER_DAY),
      clientY: 40,
    });

    await waitFor(() => {
      expect(mocks.updateMutate).toHaveBeenCalledTimes(1);
    });
    const [vars] = mocks.updateMutate.mock.calls[0] as [{ body: Record<string, unknown> }];
    expect(vars.body.startAt).toBe(utcAt(OTHER_DAY, '09:00'));

    // The release that ended the drag reaches the cell as a click. A
    // move must not also leave the day sheet standing open over it.
    fireEvent.click(pill);
    expect(screen.queryByTestId('day-detail-sheet')).toBeNull();
  });
});
