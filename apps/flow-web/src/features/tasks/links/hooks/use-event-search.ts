/**
 * useEventSearch — debounced calendar event search for the picker.
 *
 * The backend exposes `GET /workspaces/{wsId}/calendar-events?start=&end=`
 * which returns every event in the workspace within the given range.
 * There is no first-party `?q=` server filter today, so the hook
 * fetches a windowed range (default ±60 days around now) and applies a
 * locale-aware client-side substring match on `title` for the query
 * string. When `q` is empty the popover renders the next 14 days as
 * default suggestions; the same fetch covers both modes.
 *
 * The 200ms debounce lives in the consumer (popover) — the hook simply
 * accepts a `query` argument and recomputes whenever it changes. Empty
 * `q` is a valid input (returns the upcoming-window default).
 */

import { type UseQueryResult, useQuery } from '@tanstack/react-query';
import { apiRequest } from '../../../../lib/api';
import type { CalendarEventListItem } from '../types';

const DAY_MS = 86_400_000;
/** How far back the picker fetches when q is empty. */
const PAST_WINDOW_DAYS = 14;
/** How far forward the picker fetches when q is empty. */
const UPCOMING_WINDOW_DAYS = 60;

/** Query key factory for the workspace-wide event search. */
export const eventSearchKeys = {
  all: ['event-search'] as const,
  list: (workspaceId: string, q: string) =>
    [...eventSearchKeys.all, 'list', workspaceId, q] as const,
};

export interface EventSearchResult {
  events: CalendarEventListItem[];
  /** Echoes the input query for downstream rendering ("no results for {q}"). */
  q: string;
}

/**
 * Format a `Date` as an ISO datetime stripped to second precision so
 * the workspace `/calendar-events` endpoint accepts it as a range bound.
 */
function toIsoSecond(date: Date): string {
  return `${date.toISOString().slice(0, 19)}Z`;
}

/**
 * Compare two events for sort order:
 * 1. Upcoming first (chronological), then past (reverse chronological).
 * 2. Items missing `startAt` (rare; data quality issue) sink to the bottom.
 */
function compareEvents(a: CalendarEventListItem, b: CalendarEventListItem, nowSec: number): number {
  const aStart = a.startAt;
  const bStart = b.startAt;
  if (aStart === undefined && bStart === undefined) return 0;
  if (aStart === undefined) return 1;
  if (bStart === undefined) return -1;
  const aFuture = aStart >= nowSec;
  const bFuture = bStart >= nowSec;
  if (aFuture && !bFuture) return -1;
  if (!aFuture && bFuture) return 1;
  return aFuture ? aStart - bStart : bStart - aStart;
}

/**
 * Run the search. Disabled when no workspace is available; otherwise
 * always enabled so an empty `query` produces the default upcoming list.
 */
export function useEventSearch(
  workspaceId: string,
  query: string,
  enabled = true,
): UseQueryResult<EventSearchResult> {
  const trimmed = query.trim();
  return useQuery({
    queryKey: eventSearchKeys.list(workspaceId, trimmed),
    enabled: enabled && workspaceId.length > 0,
    staleTime: 30_000,
    queryFn: async (): Promise<EventSearchResult> => {
      const now = Date.now();
      const start = toIsoSecond(new Date(now - DAY_MS * PAST_WINDOW_DAYS));
      const end = toIsoSecond(new Date(now + DAY_MS * UPCOMING_WINDOW_DAYS));
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/calendar-events', {
            params: { path: { wsId: workspaceId }, query: { start, end } },
          }),
        'Failed to load events',
      );
      const all = data.events ?? [];
      const nowSec = Math.floor(now / 1000);
      let filtered = all;
      if (trimmed.length > 0) {
        const needle = trimmed.toLocaleLowerCase();
        filtered = all.filter((event) => {
          const haystack = (event.title ?? '').toLocaleLowerCase();
          return haystack.includes(needle);
        });
      }
      const sorted = [...filtered].sort((a, b) => compareEvents(a, b, nowSec));
      return { events: sorted, q: trimmed };
    },
  });
}
