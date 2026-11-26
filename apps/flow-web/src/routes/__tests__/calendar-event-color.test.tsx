/**
 * How month-grid event pills and the layer chips are coloured: colour is
 * the event's own (`color`, carried as `--evt`), the kind shows as a
 * class, and the kind layer chips carry a line sample instead of a dot.
 */

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { eventPillClassName } from '../../features/calendar-events/event-pill';

/** UTC noon on a day of the month currently on screen. */
function unixAt(day: number): number {
  const now = new Date();
  return Math.floor(Date.UTC(now.getFullYear(), now.getMonth(), day, 12, 0, 0) / 1000);
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
  color: string;
  ownerColor: string;
  labelId: string | null;
  labelName: string | null;
  detailsHidden: boolean;
}

function stubEvent(id: string, title: string, kind: string, over: Partial<StubEvent>): StubEvent {
  return {
    id,
    calendarId: 'cal-1',
    workspaceId: 'ws-1',
    workspaceName: 'WS',
    title,
    kind,
    startAt: unixAt(10),
    endAt: unixAt(10) + 3600,
    timezone: 'UTC',
    allDay: false,
    attendeeCount: 0,
    viewerAttending: false,
    showAs: 'busy',
    visibility: 'default',
    flexibility: 'fixed',
    ownerUserId: 'u1',
    createdAt: 0,
    color: '#26B5B0',
    ownerColor: '#26B5B0',
    labelId: null,
    labelName: null,
    detailsHidden: false,
    ...over,
  };
}

const mocks = vi.hoisted(() => ({
  events: [] as unknown[],
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

beforeEach(() => {
  mocks.events = [];
  mocks.toastShow.mockReset();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('calendar event colour', () => {
  it('paints the pill from the event colour and keeps the kind class', async () => {
    mocks.events = [
      stubEvent('e1', 'Standup', 'event', { color: '#E73B3B' }),
      stubEvent('e2', 'Focus', 'block', {
        color: '#2273C9',
        startAt: unixAt(11),
        endAt: unixAt(11) + 3600,
      }),
      stubEvent('e3', 'Open slot', 'free', {
        color: '#8F8F8F',
        startAt: unixAt(12),
        endAt: unixAt(12) + 3600,
      }),
      stubEvent('e4', 'Launch', 'milestone', {
        color: '#F35F8C',
        startAt: unixAt(13),
        endAt: unixAt(13) + 3600,
      }),
    ];
    renderCalendar();
    // Blocks and free time are off until their layers are switched on.
    fireEvent.click(screen.getByRole('button', { name: 'calendar.layer.blocks' }));
    fireEvent.click(screen.getByRole('button', { name: 'calendar.layer.free' }));

    const expected: [string, string, string][] = [
      ['Standup', 'event', '#E73B3B'],
      ['Focus', 'block', '#2273C9'],
      ['Open slot', 'free', '#8F8F8F'],
      ['Launch', 'milestone', '#F35F8C'],
    ];
    for (const [title, kind, color] of expected) {
      const pill = await eventPill(title);
      expect(pill.style.getPropertyValue('--evt')).toBe(color);
      for (const cls of eventPillClassName(kind).split(' ')) {
        expect(pill.classList.contains(cls)).toBe(true);
      }
    }
  });

  it('names the label in the pill aria-label only when the event has one', async () => {
    mocks.events = [
      stubEvent('e1', 'Labelled', 'event', { labelId: 'l1', labelName: 'Client' }),
      stubEvent('e2', 'Plain', 'event', {}),
    ];
    renderCalendar();

    expect((await eventPill('Labelled')).getAttribute('aria-label')).toBe(
      'calendar.event_detail.open_label_with_label',
    );
    expect((await eventPill('Plain')).getAttribute('aria-label')).toBe(
      'calendar.event_detail.open_label',
    );
  });

  it('shows a line sample, not a dot, on the kind layer chips', () => {
    renderCalendar();

    for (const [name, kind] of [
      ['calendar.layer.events', 'event'],
      ['calendar.layer.blocks', 'block'],
      ['calendar.layer.free', 'free'],
      ['calendar.layer.milestone', 'milestone'],
    ] as const) {
      const chip = screen.getByRole('button', { name });
      expect(chip.querySelector(`[data-kind="${kind}"]`)).not.toBeNull();
      expect(chip.style.getPropertyValue('--chip-color')).toBe('');
      expect(chip.querySelector('[class*="dot"]')).toBeNull();
    }
    // The task chip keeps its dot.
    const tasks = screen.getByRole('button', { name: 'calendar.layer.tasks_due' });
    expect(tasks.querySelector('[class*="dot"]')).not.toBeNull();
  });
});
