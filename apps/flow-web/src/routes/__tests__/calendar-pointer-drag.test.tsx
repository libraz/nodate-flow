/**
 * Moving something on the month grid with a pointer.
 *
 * The grid used HTML5 drag-and-drop, which fires nothing for a finger, so
 * these tests drive the gesture the way every input device now produces
 * it: pointerdown on the pill, pointermove across the grid, pointerup
 * over a day. What they pin is the part a person can lose work to — which
 * request a drop sends, what a repeating event asks first, and that a
 * press that was never meant as a drag sends nothing at all.
 *
 * The test DOM lays nothing out, so every element reports a zero-sized
 * rect and the drop target under the pointer would always be "none". The
 * cells are given the geometry a browser would report before each test.
 */

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { TOUCH_HOLD_MS } from '../../features/calendar-events/lib/pointer-drag';

/** UTC noon on a day of the month currently on screen. */
function unixAt(day: number): number {
  const now = new Date();
  return Math.floor(Date.UTC(now.getFullYear(), now.getMonth(), day, 12, 0, 0) / 1000);
}

/** The grid's `YYYY-MM-DD` key for a day of the month currently on screen. */
function keyAt(day: number): string {
  const now = new Date();
  const month = String(now.getMonth() + 1).padStart(2, '0');
  return `${now.getFullYear()}-${month}-${String(day).padStart(2, '0')}`;
}

const SOURCE_DAY = 10;
const TARGET_DAY = 15;

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

function plainEvent(): StubEvent {
  return {
    id: 'evt-1',
    calendarId: 'cal-1',
    workspaceId: 'ws-1',
    workspaceName: 'WS',
    title: 'Design review',
    kind: 'event',
    startAt: unixAt(SOURCE_DAY),
    endAt: unixAt(SOURCE_DAY) + 3600,
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

/** Repeats yearly, so exactly one occurrence falls in a 42-day window. */
function repeatingEvent(): StubEvent {
  return {
    ...plainEvent(),
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
    // The real Link renders an anchor; the drag only needs the element
    // and its handlers, not the router.
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

const CELL_W = 100;
const CELL_H = 80;

/**
 * Report the geometry a browser would: each day cell occupies its own
 * column, keyed off `data-cell-key`, and everything else is a small box
 * at the origin. Without this every rect is zero and no drop target is
 * ever under the pointer.
 */
function stubGeometry(): void {
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
      const left = column * CELL_W;
      return {
        x: left,
        y: 0,
        left,
        right: left + CELL_W,
        top: 0,
        bottom: CELL_H,
        width: CELL_W,
        height: CELL_H,
        toJSON: () => ({}),
      } as DOMRect;
    }
    return {
      x: 0,
      y: 0,
      left: 0,
      right: 60,
      top: 0,
      bottom: 16,
      width: 60,
      height: 16,
      toJSON: () => ({}),
    } as DOMRect;
  });
}

/** The horizontal centre of a rendered day cell. */
function centreOf(cellKey: string): number {
  const cell = cellElement(cellKey);
  const rect = cell.getBoundingClientRect();
  return rect.left + CELL_W / 2;
}

function cellElement(cellKey: string): HTMLElement {
  const cell = document.querySelector<HTMLElement>(`[data-cell-key="${cellKey}"]`);
  if (!cell) throw new Error(`no cell for ${cellKey}`);
  return cell;
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

/**
 * The pill button rendered for an event title, once the query settles.
 * Matched on the CSS-module class rather than the role, because the day
 * cell around it is also a button and also contains the title.
 */
async function eventPill(title: string): Promise<HTMLElement> {
  let found: HTMLElement | undefined;
  await waitFor(() => {
    found = [...document.querySelectorAll<HTMLElement>('button[class*="eventPill"]')].find((el) =>
      el.textContent?.includes(title),
    );
    if (!found) throw new Error(`no pill for ${title}`);
  });
  if (!found) throw new Error(`no pill for ${title}`);
  return found;
}

/** Press, move to another day, release. */
function dragWithMouse(pill: HTMLElement, from: string, to: string): void {
  fireEvent.pointerDown(pill, {
    pointerId: 1,
    pointerType: 'mouse',
    button: 0,
    clientX: centreOf(from),
    clientY: 10,
  });
  fireEvent.pointerMove(window, {
    pointerId: 1,
    pointerType: 'mouse',
    clientX: centreOf(to),
    clientY: 40,
  });
  fireEvent.pointerUp(window, {
    pointerId: 1,
    pointerType: 'mouse',
    clientX: centreOf(to),
    clientY: 40,
  });
}

beforeEach(() => {
  mocks.events = [];
  mocks.updatePending = false;
  mocks.updateMutate.mockReset();
  mocks.toastShow.mockReset();
  stubGeometry();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe('month grid pointer drag', () => {
  it('moves a non-repeating event and sends the shifted range', async () => {
    mocks.events = [plainEvent()];
    renderCalendar();
    const pill = await eventPill('Design review');

    // The pill is drawn on the day the event starts on.
    expect(pill.closest('[data-cell-key]')?.getAttribute('data-cell-key')).toBe(keyAt(SOURCE_DAY));

    dragWithMouse(pill, keyAt(SOURCE_DAY), keyAt(TARGET_DAY));

    await waitFor(() => {
      expect(mocks.updateMutate).toHaveBeenCalledTimes(1);
    });
    const [vars] = mocks.updateMutate.mock.calls[0] as [
      { eventId: string; body: Record<string, unknown> },
    ];
    expect(vars.eventId).toBe('evt-1');
    // Five whole days later, duration intact, and no scope — a row that
    // does not repeat has no occurrences to choose between.
    expect(vars.body.startAt).toBe(unixAt(TARGET_DAY));
    expect(vars.body.endAt).toBe(unixAt(TARGET_DAY) + 3600);
    expect(vars.body.scope).toBeUndefined();
    expect(vars.body.occurrenceStart).toBeUndefined();
  });

  it('asks a repeating event which occurrences the move reaches', async () => {
    mocks.events = [repeatingEvent()];
    renderCalendar();
    const pill = await eventPill('Kickoff');

    dragWithMouse(pill, keyAt(SOURCE_DAY), keyAt(TARGET_DAY));

    // Nothing is sent until the question is answered.
    expect(mocks.updateMutate).not.toHaveBeenCalled();
    const confirm = await screen.findByText('recurrence.scope.save.confirm');
    fireEvent.click(confirm);

    await waitFor(() => {
      expect(mocks.updateMutate).toHaveBeenCalledTimes(1);
    });
    const [vars] = mocks.updateMutate.mock.calls[0] as [{ body: Record<string, unknown> }];
    // The dialog defaults to the single occurrence, and the occurrence is
    // named by the start the drag picked up — not by where it landed.
    expect(vars.body.scope).toBe('occurrence');
    expect(vars.body.occurrenceStart).toBe(unixAt(SOURCE_DAY));
    expect(vars.body.startAt).toBe(unixAt(TARGET_DAY));
  });

  it('sends nothing when the scope question is dismissed', async () => {
    mocks.events = [repeatingEvent()];
    renderCalendar();
    const pill = await eventPill('Kickoff');

    dragWithMouse(pill, keyAt(SOURCE_DAY), keyAt(TARGET_DAY));

    const cancel = await screen.findByText('recurrence.scope.cancel');
    fireEvent.click(cancel);

    await waitFor(() => {
      expect(screen.queryByText('recurrence.scope.save.confirm')).toBeNull();
    });
    expect(mocks.updateMutate).not.toHaveBeenCalled();
    // The pill never left the day it was drawn on.
    expect(cellElement(keyAt(SOURCE_DAY)).textContent).toContain('Kickoff');
  });

  it('does not start a drag when a finger releases before the hold', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    mocks.events = [plainEvent()];
    renderCalendar();
    const pill = await eventPill('Design review');

    fireEvent.pointerDown(pill, {
      pointerId: 2,
      pointerType: 'touch',
      clientX: centreOf(keyAt(SOURCE_DAY)),
      clientY: 10,
    });
    vi.advanceTimersByTime(TOUCH_HOLD_MS - 100);
    fireEvent.pointerMove(window, {
      pointerId: 2,
      pointerType: 'touch',
      clientX: centreOf(keyAt(TARGET_DAY)),
      clientY: 40,
    });
    fireEvent.pointerUp(window, {
      pointerId: 2,
      pointerType: 'touch',
      clientX: centreOf(keyAt(TARGET_DAY)),
      clientY: 40,
    });

    expect(mocks.updateMutate).not.toHaveBeenCalled();
  });

  it('lifts once a finger has held still long enough', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    mocks.events = [plainEvent()];
    renderCalendar();
    const pill = await eventPill('Design review');

    fireEvent.pointerDown(pill, {
      pointerId: 3,
      pointerType: 'touch',
      clientX: centreOf(keyAt(SOURCE_DAY)),
      clientY: 10,
    });
    vi.advanceTimersByTime(TOUCH_HOLD_MS + 10);
    fireEvent.pointerMove(window, {
      pointerId: 3,
      pointerType: 'touch',
      clientX: centreOf(keyAt(TARGET_DAY)),
      clientY: 40,
    });
    fireEvent.pointerUp(window, {
      pointerId: 3,
      pointerType: 'touch',
      clientX: centreOf(keyAt(TARGET_DAY)),
      clientY: 40,
    });

    await waitFor(() => {
      expect(mocks.updateMutate).toHaveBeenCalledTimes(1);
    });
  });

  it('leaves the pill where it was and says why when the move is refused', async () => {
    mocks.events = [plainEvent()];
    mocks.updateMutate.mockImplementation(
      (_vars: unknown, opts: { onError?: (err: unknown) => void }) => {
        opts.onError?.(new Error('calendar is read-only'));
      },
    );
    renderCalendar();
    const pill = await eventPill('Design review');

    dragWithMouse(pill, keyAt(SOURCE_DAY), keyAt(TARGET_DAY));

    await waitFor(() => {
      expect(mocks.toastShow).toHaveBeenCalled();
    });
    expect(mocks.toastShow).toHaveBeenCalledWith(
      expect.objectContaining({ tone: 'danger', message: 'calendar is read-only' }),
    );
    // Nothing moved optimistically, so there is nothing to put back: the
    // pill is still drawn on the day the drag started from.
    expect(cellElement(keyAt(SOURCE_DAY)).textContent).toContain('Design review');
    expect(cellElement(keyAt(TARGET_DAY)).textContent).not.toContain('Design review');
  });

  it('releasing outside every day sends nothing', async () => {
    mocks.events = [plainEvent()];
    renderCalendar();
    const pill = await eventPill('Design review');

    fireEvent.pointerDown(pill, {
      pointerId: 4,
      pointerType: 'mouse',
      button: 0,
      clientX: centreOf(keyAt(SOURCE_DAY)),
      clientY: 10,
    });
    // Below every cell: the grid's rows end at CELL_H.
    fireEvent.pointerMove(window, {
      pointerId: 4,
      pointerType: 'mouse',
      clientX: 20,
      clientY: CELL_H + 200,
    });
    fireEvent.pointerUp(window, {
      pointerId: 4,
      pointerType: 'mouse',
      clientX: 20,
      clientY: CELL_H + 200,
    });

    expect(mocks.updateMutate).not.toHaveBeenCalled();
  });

  it('Escape during a drag cancels it', async () => {
    mocks.events = [plainEvent()];
    renderCalendar();
    const pill = await eventPill('Design review');

    fireEvent.pointerDown(pill, {
      pointerId: 5,
      pointerType: 'mouse',
      button: 0,
      clientX: centreOf(keyAt(SOURCE_DAY)),
      clientY: 10,
    });
    fireEvent.pointerMove(window, {
      pointerId: 5,
      pointerType: 'mouse',
      clientX: centreOf(keyAt(TARGET_DAY)),
      clientY: 40,
    });
    fireEvent.keyDown(window, { key: 'Escape' });
    fireEvent.pointerUp(window, {
      pointerId: 5,
      pointerType: 'mouse',
      clientX: centreOf(keyAt(TARGET_DAY)),
      clientY: 40,
    });

    expect(mocks.updateMutate).not.toHaveBeenCalled();
  });
});
