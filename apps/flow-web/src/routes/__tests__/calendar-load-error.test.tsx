/**
 * What the calendar shows while a window loads, and when one will not.
 *
 * Both queries are keyed on the window they ask for, so moving the
 * cursor asks a question nothing has answered yet. Two different states
 * come out of that, and they are drawn the same way unless something is
 * done about it.
 *
 * While the answer is on its way, the window that loaded last is held on
 * screen — without it the whole month blanks under the reader's finger
 * on every crossing, and on the mobile view the cursor moves with the
 * scroll. A placeholder is only consulted while a query is pending,
 * though, so it covers the wait and not a refusal: a request that fails
 * leaves the month empty, drawn exactly like a month with nothing in it,
 * and neither query surfaces its failure anywhere else. A reader cannot
 * tell "you have nothing scheduled" from "this did not load", and the
 * second is the one they would act on.
 */

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactElement, ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  /** API paths that refuse until they are removed. */
  failing: new Set<string>(),
  /** Resolves the next call to a path held open, keyed by path. */
  held: new Map<string, () => void>(),
  /** Paths whose next call waits until it is released. */
  holding: new Set<string>(),
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

/**
 * The key, followed by whatever the caller interpolated into it.
 *
 * Returning the key alone is the usual shape and it is wrong for any
 * message with a value in it: the assertion then passes whether or not
 * the value ever reached the string, so a message that lost its
 * interpolation ships looking green. Carrying the values through keeps
 * "which message" decidable without pinning the English wording, and
 * makes a dropped one visible.
 */
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, values?: Record<string, unknown>): string => {
      if (values === undefined) return key;
      const parts = Object.entries(values).map(([name, value]) => `${name}=${String(value)}`);
      return parts.length === 0 ? key : `${key} ${parts.join(' ')}`;
    },
    i18n: { resolvedLanguage: 'en', language: 'en' },
  }),
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: vi.fn() },
}));

vi.mock('@nodate-flow/holidays', () => ({
  getOrCreateProvider: () => ({ holidaysBetween: () => [] }),
}));

vi.mock('../../lib/api', () => ({
  apiRequest: async (fn: (client: unknown) => unknown): Promise<unknown> => {
    let path = '';
    let params: { query?: Record<string, unknown> } | undefined;
    const record = (p: string, opts?: unknown): { data: undefined } => {
      path = p;
      params = (opts as { params?: { query?: Record<string, unknown> } } | undefined)?.params;
      return { data: undefined };
    };
    await fn({ GET: record, PATCH: record, POST: record });
    if (mocks.holding.has(path)) {
      mocks.holding.delete(path);
      const waited = path;
      await new Promise<void>((resolve) => {
        mocks.held.set(waited, resolve);
      });
    }
    if (mocks.failing.has(path)) throw new Error(`refused: ${path}`);
    if (path === '/me/tasks-with-dates') {
      // Named after the window asked for, so a held window and the one
      // that replaces it are told apart on screen.
      //
      // Due on the window's own last day, and that day specifically.
      // The desktop grid draws the cursor month's cells, so the days on
      // screen move with the cursor: a task in the middle of a month
      // disappears the moment the reader steps to the next one, whether
      // or not the previous window was held. The last cell of a month's
      // grid falls in the first days of the month after it, which is
      // the one day both grids always draw — so it is the only place a
      // task can sit where staying on screen means the window was held
      // and nothing else. Moving it to a rounder date makes the test
      // pass or fail on grid geometry instead.
      const from = String(params?.query?.from ?? '');
      const to = String(params?.query?.to ?? '');
      return {
        tasks: [
          {
            id: `task-${from}`,
            title: `Task for ${from}`,
            workspaceName: 'WS',
            derivedState: 'open',
            priority: 1,
            dueOn: to,
          },
        ],
      };
    }
    if (path === '/me/calendar-events') return { events: [] };
    if (path === '/workspaces/{wsId}/projects') return { projects: [] };
    return {};
  },
}));

vi.mock('../../features/settings/api', () => ({
  useMeQuery: () => ({
    data: { id: 'u1', country: null, timezone: 'UTC', weekStart: 'mon', locale: 'en' },
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
  useUpdateEvent: () => ({ mutate: vi.fn(), isPending: false }),
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

const TASK_PATH = '/me/tasks-with-dates';
const EVENT_PATH = '/me/calendar-events';
const ERROR_KEY = 'calendar.load_error.message';
const RETRY_LABEL = 'calendar.load_error.retry';
const NEXT_LABEL = 'calendar.next';

/**
 * The month label the route builds for `monthsAhead` months from now,
 * formatted the way the toolbar formats it.
 *
 * The message names the month it failed for, and naming it is the whole
 * point — "this did not load" without saying which month is a sentence
 * a reader cannot act on. So the assertions below match the month too,
 * which is only possible because the `t` stub above carries the
 * interpolated values through.
 */
function monthLabelAhead(monthsAhead: number): string {
  const now = new Date();
  const first = new Date(now.getFullYear(), now.getMonth() + monthsAhead, 1);
  return new Intl.DateTimeFormat('en', { year: 'numeric', month: 'long' }).format(first);
}

/** The rendered text of the failure message for a given month. */
function errorTextFor(monthsAhead: number): string {
  return `${ERROR_KEY} month=${monthLabelAhead(monthsAhead)}`;
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

/** The title of every task drawn on the grid right now. */
function drawnTasks(): string[] {
  return [...document.querySelectorAll('a[class*="taskPill"]')]
    .map((el) => el.textContent ?? '')
    .filter((text) => text.startsWith('Task for '));
}

beforeEach(() => {
  mocks.failing.clear();
  mocks.held.clear();
  mocks.holding.clear();
  // Keeps the route on its desktop branch: the mobile view virtualizes,
  // and this file is about what is said, not about which grid says it.
  vi.stubGlobal('innerWidth', 1440);
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: false,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('calendar window loading', () => {
  it('holds the last window on screen while the next one is in flight', async () => {
    renderCalendar();
    const opened = await waitFor(() => {
      const drawn = drawnTasks();
      if (drawn.length === 0) throw new Error('nothing drawn yet');
      return drawn[0] as string;
    });

    // The next window is held open, so this is the moment the grid used
    // to blank: the key has changed and nothing has answered it yet.
    mocks.holding.add(TASK_PATH);
    mocks.holding.add(EVENT_PATH);
    await userEvent.click(screen.getByRole('button', { name: NEXT_LABEL }));

    await waitFor(() => {
      expect(mocks.held.size).toBeGreaterThan(0);
    });
    expect(drawnTasks()).toEqual([opened]);

    for (const release of mocks.held.values()) release();
    mocks.held.clear();
    await waitFor(() => {
      expect(drawnTasks()).not.toEqual([opened]);
    });
  });

  it('says a month did not load rather than drawing it as empty', async () => {
    mocks.failing.add(TASK_PATH);
    mocks.failing.add(EVENT_PATH);
    renderCalendar();

    // Matched with the month in it: the message names the month it
    // failed for, and an assertion on the key alone would pass just as
    // well if that name never arrived.
    await screen.findByText(errorTextFor(0));
    expect(drawnTasks()).toEqual([]);
  });

  it('says so for a window that fails after another one loaded', async () => {
    renderCalendar();
    await waitFor(() => {
      expect(drawnTasks().length).toBeGreaterThan(0);
    });

    mocks.failing.add(TASK_PATH);
    mocks.failing.add(EVENT_PATH);
    await userEvent.click(screen.getByRole('button', { name: NEXT_LABEL }));

    // The month moved with the click, so the message has to name the
    // month the reader is now on, not the one that loaded.
    await screen.findByText(errorTextFor(1));
    expect(screen.queryByText(errorTextFor(0))).toBeNull();
    // The held window does not survive the refusal — a placeholder is
    // consulted only while a query is pending. So the grid empties, and
    // the message is the only thing that distinguishes this from a month
    // with nothing in it.
    expect(drawnTasks()).toEqual([]);
  });

  it('clears the message when a retry succeeds', async () => {
    mocks.failing.add(TASK_PATH);
    mocks.failing.add(EVENT_PATH);
    renderCalendar();
    await screen.findByText(errorTextFor(0));

    mocks.failing.clear();
    await userEvent.click(screen.getByRole('button', { name: RETRY_LABEL }));

    await waitFor(() => {
      expect(screen.queryByText(errorTextFor(0))).toBeNull();
    });
    await waitFor(() => {
      expect(drawnTasks().length).toBeGreaterThan(0);
    });
  });
});
