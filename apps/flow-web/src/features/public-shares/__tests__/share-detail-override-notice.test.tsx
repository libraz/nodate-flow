/**
 * A share can publish the master of a recurring series without the row that
 * replaces one of its occurrences. The public page then keeps advertising the
 * original time for a meeting that has moved, and nobody outside the workspace
 * can tell. The editor is the only place where that is still fixable, so the
 * attached-event table says so on the series it affects.
 *
 * The silence matters as much as the notice: a warning that also appears once
 * the replacement is published, or on a series that never moved, is a warning
 * people learn to scroll past.
 */

import type { components } from '@nodate-flow/sdk';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { type ReactElement, type ReactNode, Suspense } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  del: vi.fn(),
  patch: vi.fn(),
  toastShow: vi.fn(),
}));

vi.mock('../../../lib/sdk', () => ({
  // openapi-fetch keys its client by HTTP method, hence the caps.
  sdk: { GET: mocks.get, POST: mocks.post, DELETE: mocks.del, PATCH: mocks.patch },
  authSdk: { GET: mocks.get, POST: mocks.post, DELETE: mocks.del, PATCH: mocks.patch },
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: mocks.toastShow },
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key, i18n: { resolvedLanguage: 'en' } }),
}));

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children }: { children: ReactNode }): ReactElement => <span>{children}</span>,
}));

vi.mock('../add-events-dialog', () => ({
  default: (): null => null,
}));

import ShareDetail from '../share-detail';

/* ── fixtures ─────────────────────────────────────────────────── */

type ShareEvent = components['schemas']['ShareEventResponse'];
type OverrideWarning = components['schemas']['ShareOverrideWarning'];

const NOTICE_LABEL = 'workspace.public_shares.detail.override_notice.label';
const NOTICE_MOVED = 'workspace.public_shares.detail.override_notice.moved';
const NOTICE_PRIVATE = 'workspace.public_shares.detail.override_notice.confidential';
const NOTICE_ATTACH = 'workspace.public_shares.detail.override_notice.attach';

/** 2026-03-06 09:00Z — the occurrence the page still shows. */
const ORIGINAL_START = 1_772_787_600;
/** 2026-03-09 09:00Z — where that occurrence actually moved to. */
const MOVED_START = 1_773_046_800;

function series(over: Partial<ShareEvent> = {}): ShareEvent {
  return {
    eventId: 'evt-standup',
    linkId: 'link-standup',
    linkSortWeight: 0,
    linkCreatedAt: 1_772_000_000,
    title: 'Weekly standup',
    calendarId: 'cal-team',
    calendarName: 'Team',
    timezone: 'UTC',
    allDay: false,
    startAt: ORIGINAL_START,
    endAt: ORIGINAL_START + 1800,
    visibility: 'default',
    ...over,
  };
}

function replacement(over: Partial<ShareEvent> = {}): ShareEvent {
  return series({
    eventId: 'evt-moved',
    linkId: 'link-moved',
    linkSortWeight: 1,
    startAt: MOVED_START,
    endAt: MOVED_START + 1800,
    ...over,
  });
}

function movedOccurrence(over: Partial<OverrideWarning> = {}): OverrideWarning {
  return {
    eventId: 'evt-moved',
    title: 'Weekly standup',
    originalStart: ORIGINAL_START,
    startAt: MOVED_START,
    visibility: 'default',
    ...over,
  };
}

function serveEvents(events: ShareEvent[]): void {
  mocks.get.mockResolvedValue({
    data: {
      share: { id: 'share-1', title: 'Autumn schedule' },
      events,
    },
    error: null,
    response: new Response(null, { status: 200 }),
  });
}

function renderDetail(): HTMLElement {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false },
      mutations: { retry: false, throwOnError: false },
    },
  });
  function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return (
      <QueryClientProvider client={client}>
        <Suspense fallback={null}>{children}</Suspense>
      </QueryClientProvider>
    );
  }
  const { container } = render(
    <Wrapper>
      <ShareDetail workspaceId="ws-1" shareId="share-1" />
    </Wrapper>,
  );
  return container;
}

beforeEach(() => {
  mocks.get.mockReset();
  mocks.post.mockReset().mockResolvedValue({
    data: { attached: 1, skipped: 0 },
    error: null,
    response: new Response(null, { status: 200 }),
  });
  mocks.del.mockReset();
  mocks.patch.mockReset();
  mocks.toastShow.mockReset();
});

describe('ShareDetail moved-occurrence notice', () => {
  it('warns on a series whose replacement is not published on the share', async () => {
    serveEvents([series({ unpublishedOverrides: [movedOccurrence()] })]);
    renderDetail();

    expect(await screen.findByText('Weekly standup')).toBeDefined();
    expect(screen.getByText(NOTICE_LABEL)).toBeDefined();
    expect(screen.getByText(NOTICE_MOVED)).toBeDefined();
  });

  it('offers to publish the replacement and attaches that event', async () => {
    const user = userEvent.setup();
    serveEvents([series({ unpublishedOverrides: [movedOccurrence()] })]);
    renderDetail();

    await user.click(await screen.findByRole('button', { name: NOTICE_ATTACH }));

    expect(mocks.post).toHaveBeenCalledTimes(1);
    const [path, init] = mocks.post.mock.calls[0] as [
      string,
      { params: unknown; body: { eventIds: string[] } },
    ];
    expect(path).toBe('/workspaces/{wsId}/public-shares/{shareId}/events');
    expect(init.body.eventIds).toEqual(['evt-moved']);
  });

  it('stays silent when the replacement is published alongside the series', async () => {
    serveEvents([series(), replacement()]);
    renderDetail();

    // Both rows carry the series title: the master and the row standing in
    // for the occurrence that moved.
    expect(await screen.findAllByText('Weekly standup')).toHaveLength(2);
    expect(screen.queryByText(NOTICE_LABEL)).toBeNull();
    expect(screen.queryByText(NOTICE_MOVED)).toBeNull();
    expect(screen.queryByRole('button', { name: NOTICE_ATTACH })).toBeNull();
  });

  it('stays silent on a series with no moved occurrences', async () => {
    serveEvents([series({ unpublishedOverrides: null })]);
    renderDetail();

    expect(await screen.findByText('Weekly standup')).toBeDefined();
    expect(screen.queryByText(NOTICE_LABEL)).toBeNull();
    expect(screen.queryByText(NOTICE_MOVED)).toBeNull();
  });

  it('withholds the fix when the replacement is private, and says why', async () => {
    serveEvents([
      series({ unpublishedOverrides: [movedOccurrence({ visibility: 'confidential' })] }),
    ]);
    renderDetail();

    expect(await screen.findByText(NOTICE_PRIVATE)).toBeDefined();
    // Attaching it would be refused, so the row states the problem instead
    // of offering an action that only ever produces an error.
    expect(screen.queryByText(NOTICE_MOVED)).toBeNull();
    expect(screen.queryByRole('button', { name: NOTICE_ATTACH })).toBeNull();
  });

  it('keeps the table valid with a notice row in it', async () => {
    serveEvents([series({ unpublishedOverrides: [movedOccurrence()] })]);
    const container = renderDetail();

    expect(await screen.findByText(NOTICE_MOVED)).toBeDefined();
    expect(await axe(container)).toHaveNoViolations();
  });
});
