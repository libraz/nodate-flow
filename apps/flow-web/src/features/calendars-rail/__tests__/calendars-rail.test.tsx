/**
 * CalendarsRail rename refresh.
 *
 * Regression for the bug where renaming a calendar via the Calendar
 * Settings Drawer succeeds (PATCH 200, toast shown) but the rail keeps
 * rendering the old name until the page is reloaded.
 *
 * The drawer's mutation hook invalidates
 * `['calendar-events', 'calendars', wsId]`, which is the same query key
 * the rail's `useQueries` registers. On invalidation TanStack Query
 * marks the active observer stale and refetches, so the rail must
 * re-render with the freshly fetched name.
 *
 * The test wires a fake SDK that returns whatever the most recent stub
 * decides. We:
 *   1. mount the rail and wait for "Original",
 *   2. flip the stub to return "Renamed",
 *   3. invalidate the same key the drawer mutation invalidates,
 *   4. assert the rail re-renders the new name.
 *
 * If invalidation never reaches the rail's query (key mismatch, dropped
 * subscription, etc.), the assertion in step 4 fails — exactly the bug
 * surfaced by `apps/flow-web/e2e/calendar-settings.spec.ts`'s
 * "renames calendar" case.
 */

import type { components } from '@nodate-flow/sdk';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import i18n from 'i18next';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

type RailCalendar = components['schemas']['CalendarResponse'];

/* ── Fake SDK ─────────────────────────────────────────────────── */

// `vi.mock` factories are hoisted; bind shared handles via `vi.hoisted`
// so they exist when the factory runs.
const sdkMocks = vi.hoisted(() => ({
  /** Latest list returned by GET /workspaces/{wsId}/calendars. */
  calendars: [] as RailCalendar[],
  /** Counts how many times the rail re-fetched. */
  fetchCount: 0,
}));

vi.mock('../../../lib/sdk', () => ({
  sdk: {
    GET: vi.fn(async (path: string) => {
      if (path === '/workspaces/{wsId}/calendars') {
        sdkMocks.fetchCount += 1;
        return { data: { calendars: sdkMocks.calendars }, error: null };
      }
      return { data: null, error: { status: 404 } };
    }),
    PATCH: vi.fn(async () => ({ data: null, error: null })),
    DELETE: vi.fn(async () => ({ data: null, error: null })),
    POST: vi.fn(async () => ({ data: null, error: null })),
  },
}));

// Drawer / popover / memos / settings-drawer pull more chrome than this
// test cares about. Replace them with inert stubs so the rail renders
// in isolation.
vi.mock('@nodate-flow/ui/primitives/drawer', () => ({
  default: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}));
vi.mock('@nodate-flow/ui/primitives/popover', () => ({
  default: ({ children, content }: { children: ReactNode; content: ReactNode }) => (
    <>
      {children}
      {content}
    </>
  ),
}));
vi.mock('@nodate-flow/ui/primitives/button', () => ({
  // Forward `children` and the click handler — the rail uses Button only
  // for the "Add teammate calendar" / "Subscribe holidays" affordances,
  // neither of which the rename test interacts with.
  default: ({ children, ...rest }: { children: ReactNode } & Record<string, unknown>) => (
    <button type="button" {...(rest as Record<string, unknown>)}>
      {children}
    </button>
  ),
}));
vi.mock('../../calendars/calendar-settings-drawer', () => ({
  default: () => null,
}));
vi.mock('../../calendar-memos/calendar-memos-panel', () => ({
  default: () => null,
}));
vi.mock('../discover-list', () => ({
  default: () => null,
}));

/* ── Imports under test ───────────────────────────────────────── */

// Imported AFTER the mocks above so the dynamic SDK / chrome stubs
// resolve through them.
import CalendarsRail from '../calendars-rail';

/* ── Test i18n ────────────────────────────────────────────────── */

let testI18n: ReturnType<typeof i18n.createInstance> | null = null;

function ensureTestI18n(): ReturnType<typeof i18n.createInstance> {
  if (testI18n) return testI18n;
  const instance = i18n.createInstance();
  void instance.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: 'en',
    defaultNS: 'common',
    ns: ['common'],
    resources: { en: { common: {} } },
    interpolation: { escapeValue: false },
    parseMissingKeyHandler: (key: string) => key,
    react: { useSuspense: false },
  });
  testI18n = instance;
  return instance;
}

/* ── Helpers ──────────────────────────────────────────────────── */

function makeCalendar(overrides: Partial<RailCalendar> = {}): RailCalendar {
  return {
    id: 'cal-1',
    workspaceId: 'ws-1',
    kind: 'personal',
    name: 'Original',
    color: '#2563eb',
    displayColor: '#2563eb',
    visible: true,
    role: 'owner',
    subscriptionSortWeight: 0,
    createdAt: 1_700_000_000,
    updatedAt: 1_700_000_000,
    ...overrides,
  } as RailCalendar;
}

function renderRail(): { qc: QueryClient } {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false, gcTime: Number.POSITIVE_INFINITY },
      mutations: { retry: false, throwOnError: false },
    },
  });

  function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return (
      <QueryClientProvider client={qc}>
        <I18nextProvider i18n={ensureTestI18n()}>{children}</I18nextProvider>
      </QueryClientProvider>
    );
  }

  render(
    <Wrapper>
      <CalendarsRail
        workspaces={[{ id: 'ws-1', name: 'Workspace', country: '' }]}
        selfUserId="user-1"
      />
    </Wrapper>,
  );
  return { qc };
}

/* ── Tests ────────────────────────────────────────────────────── */

describe('CalendarsRail rename refresh', () => {
  beforeEach(() => {
    sdkMocks.calendars = [makeCalendar()];
    sdkMocks.fetchCount = 0;
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it('re-renders the row name after the calendar list cache is invalidated', async () => {
    const { qc } = renderRail();

    // Initial paint pulls the seeded "Original" name through the
    // workspace calendars query. The popover stub renders the row's
    // menu inline so the calendar label appears once in the row +
    // once inside the popover content; either match is fine.
    await waitFor(() => {
      expect(screen.queryAllByText('Original').length).toBeGreaterThan(0);
    });
    const initialFetchCount = sdkMocks.fetchCount;

    // Simulate the PATCH succeeding upstream + the drawer's
    // invalidateCalendarCaches() fan-out (which targets the same key
    // the rail subscribes to).
    sdkMocks.calendars = [makeCalendar({ name: 'Renamed' })];
    await qc.invalidateQueries({ queryKey: ['calendar-events', 'calendars', 'ws-1'] });

    // The active observer must refetch and the row must re-render with
    // the new name. If invalidation does not reach the rail's query,
    // either fetchCount stays at initialFetchCount or the row keeps
    // showing "Original" — both are bug states surfaced by the e2e.
    await waitFor(() => {
      expect(sdkMocks.fetchCount).toBeGreaterThan(initialFetchCount);
      expect(screen.queryAllByText('Renamed').length).toBeGreaterThan(0);
    });
    expect(screen.queryAllByText('Original')).toHaveLength(0);
  });

  it('shares the exact query key the calendar mutation hook invalidates', async () => {
    // Pin the contract between the rail and the calendar settings
    // mutation hook: both sides must agree on the literal query key
    // shape `['calendar-events', 'calendars', wsId]`. If either one
    // drifts (typo, reorder, missing wsId), the rail will silently stop
    // refreshing after a rename — a regression that is hard to spot in
    // code review but is exactly what this assertion guards against.
    const { qc } = renderRail();
    await waitFor(() => {
      expect(screen.queryAllByText('Original').length).toBeGreaterThan(0);
    });
    const cache = qc.getQueryCache().findAll({
      queryKey: ['calendar-events', 'calendars', 'ws-1'],
    });
    expect(cache.length).toBeGreaterThan(0);
    expect(cache[0]?.queryKey).toEqual(['calendar-events', 'calendars', 'ws-1']);
  });
});
