import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { HttpResponse, http } from 'msw';
import type { ReactElement, ReactNode } from 'react';
import { Suspense } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { API_URL, server, useMockApi } from '../../../../tests/msw/server';
import { authStore } from '../../auth/auth-store';
import type { TimelineEvent } from '../api';
import TimelineView from '../timeline-view';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
    i18n: { resolvedLanguage: 'en' },
  }),
}));

vi.mock('../event-filter-bar', () => ({ default: () => null }));

vi.mock('../event-card', () => ({
  default: ({ event }: { event: TimelineEvent }) => (
    <div data-testid={`timeline-row-${event.id}`}>
      {event.id}:{String(event.wasReversed)}
    </div>
  ),
}));

vi.mock('@nodate-flow/ui/primitives/confirm', () => ({
  confirm: { ask: vi.fn().mockResolvedValue(true) },
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: vi.fn() },
}));

useMockApi();

const pageSize = 3;
const seedEvents = [
  makeEvent('earlier-top', 'signal-earlier-top', false),
  makeEvent('earlier-target', 'signal-earlier-target', true),
  makeEvent('earlier-bottom', 'signal-earlier-bottom', false),
  makeEvent('current-top', 'signal-current-top', false),
  makeEvent('current-target', 'signal-current-target', true),
  makeEvent('current-bottom', 'signal-current-bottom', false),
];
const reversed = new Set<string>();
const timelineRequests: number[] = [];

function makeEvent(id: string, signalId: string, agentOrigin: boolean): TimelineEvent {
  return {
    id,
    type: 'task.updated',
    occurredAt: 1_700_000_000,
    payload: {},
    triggeredBySignalId: signalId,
    ...(agentOrigin ? { actorAgentId: 'agent-1' } : {}),
    wasReversed: false,
  } as unknown as TimelineEvent;
}

function makeClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false },
      mutations: { retry: false, throwOnError: false },
    },
  });
}

function wrapper(client: QueryClient): ({ children }: { children: ReactNode }) => ReactElement {
  return function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return (
      <QueryClientProvider client={client}>
        <Suspense fallback={<p>loading</p>}>{children}</Suspense>
      </QueryClientProvider>
    );
  };
}

beforeEach(() => {
  reversed.clear();
  timelineRequests.length = 0;
  seedAuthToken();

  server.use(
    http.get(`${API_URL}/workspaces/ws-1/timeline`, ({ request }) => {
      const offset = Number(new URL(request.url).searchParams.get('offset') ?? 0);
      timelineRequests.push(offset);
      const events = seedEvents.slice(offset, offset + pageSize).map((event) => ({
        ...event,
        wasReversed: reversed.has(event.id),
      }));
      return HttpResponse.json({ events, total: seedEvents.length, limit: pageSize, offset });
    }),
    http.post(`${API_URL}/workspaces/ws-1/events/:eventId/reverse`, ({ params }) => {
      reversed.add(String(params.eventId));
      return HttpResponse.json({});
    }),
  );
});

afterEach(() => {
  authStore.getState().clearSession();
});

async function loadTwoPages(): Promise<{ container: HTMLElement; client: QueryClient }> {
  const client = makeClient();
  const { container } = render(
    <TimelineView scope={{ kind: 'workspace', id: 'ws-1' }} initialFilters={{ limit: pageSize }} />,
    { wrapper: wrapper(client) },
  );

  await screen.findByTestId('timeline-row-earlier-target');
  fireEvent.click(screen.getByRole('button', { name: 'view.load_more' }));
  await screen.findByTestId('timeline-row-current-target');

  return { container, client };
}

async function reverseAndAssertRefreshed(
  container: HTMLElement,
  eventId: string,
  signalId: string,
): Promise<void> {
  const group = container.querySelector<HTMLElement>(`article[data-signal-id="${signalId}"]`);
  expect(group).not.toBeNull();
  const reverseButtons = within(group as HTMLElement).getAllByRole('button', {
    name: 'signal.reverse',
  });
  fireEvent.click(reverseButtons[0] as HTMLButtonElement);

  await waitFor(() => {
    expect(reversed.has(eventId)).toBe(true);
  });

  await waitFor(() => {
    expect(group?.getAttribute('data-reversed')).toBe('true');
  });
  expect(within(group as HTMLElement).queryByRole('button', { name: 'signal.reverse' })).toBeNull();
  expect(screen.getByTestId(`timeline-row-${eventId}`).textContent).toBe(`${eventId}:true`);
}

function seedAuthToken(): void {
  const header = Buffer.from(JSON.stringify({ alg: 'none', typ: 'JWT' })).toString('base64url');
  const payload = Buffer.from(
    JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 }),
  ).toString('base64url');
  authStore.getState().setAccessToken(`${header}.${payload}.`);
}

describe('TimelineView refreshed pages', () => {
  it('replaces same-ID values on the current page after a reverse and keeps page order unique', async () => {
    const { container } = await loadTwoPages();

    await reverseAndAssertRefreshed(container, 'current-target', 'signal-current-target');

    expect(
      Array.from(container.querySelectorAll('[data-testid^="timeline-row-"]')).map((row) =>
        row.getAttribute('data-testid'),
      ),
    ).toEqual(seedEvents.map((event) => `timeline-row-${event.id}`));
  });

  it('refetches an earlier loaded page after a reverse', async () => {
    const { container } = await loadTwoPages();

    await reverseAndAssertRefreshed(container, 'earlier-target', 'signal-earlier-target');

    expect(timelineRequests).toContain(0);
    expect(timelineRequests).toContain(pageSize);
  });
});
