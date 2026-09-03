/**
 * Calendars rail feature — react-query hooks for the right-rail
 * "Calendars" panel and the "Add teammate calendar" drawer on
 * `/calendar`.
 *
 * Three writes are exposed:
 *
 *   - {@link useSubscribeToCalendarMutation}   POST /workspaces/{wsId}/calendars/{calId}/subscribe
 *   - {@link usePatchOwnSubscriptionMutation}  PATCH /workspaces/{wsId}/calendars/{calId}/subscription
 *   - {@link useUnsubscribeMutation}           DELETE /workspaces/{wsId}/calendars/{calId}/members/{userId}
 *
 * The "leave calendar" action funnels through the same
 * `members-remove` endpoint that the calendar membership UI uses; the
 * caller passes the actor's own user public id to drop themselves.
 *
 * On success every mutation invalidates the per-workspace calendar
 * list (`['calendar-events', 'calendars', wsId]`, owned by the
 * calendar-events feature) plus the cross-workspace event aggregate
 * (`['calendar', 'me-events']`) so the month grid refreshes if a
 * just-toggled calendar's events appear or disappear.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseQueryResult,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { apiRequest } from '../../lib/api';
import type { ApiError } from '../../lib/api-error';

/**
 * Subscribed calendar row as returned by
 * `GET /workspaces/{wsId}/calendars`. Re-exported so the rail
 * components can consume the SDK schema without re-importing
 * `@nodate-flow/sdk` directly.
 */
export type RailCalendar = components['schemas']['CalendarResponse'];

/**
 * Discoverable (subscribable) calendar row as returned by
 * `GET /workspaces/{wsId}/discoverable-calendars`. Each row carries
 * the calendar owner's display name + avatar so the drawer can render
 * them next to the calendar name.
 */
export type DiscoverableCalendar = components['schemas']['DiscoverableCalendarResponse'];

/**
 * Body shape for {@link usePatchOwnSubscriptionMutation}. Each field
 * is optional — the caller patches only what changed (typically just
 * `visible`).
 */
export type PatchOwnSubscriptionBody = components['schemas']['PatchOwnSubscriptionInputBody'];

/**
 * Query key root for the calendars-rail feature. The discoverable
 * list lives under `['calendar', 'discoverable', wsId]` so it sits
 * next to the existing `['calendar', 'me-events']` aggregate but does
 * not collide with the per-workspace `['calendar-events', 'calendars', wsId]`
 * list owned by the calendar-events feature.
 */
export const calendarsRailKeys = {
  discoverable: (wsId: string) => ['calendar', 'discoverable', wsId] as const,
};

/**
 * Invalidate every cache that should refresh after a subscription
 * write succeeds: the workspace's subscribed calendar list, the
 * discoverable list (subscribing removes a row, unsubscribing adds
 * one back), and the cross-workspace event aggregate.
 */
function invalidateRailCaches(qc: ReturnType<typeof useQueryClient>, wsId: string): void {
  void qc.invalidateQueries({ queryKey: ['calendar-events', 'calendars', wsId] });
  void qc.invalidateQueries({ queryKey: calendarsRailKeys.discoverable(wsId) });
  void qc.invalidateQueries({ queryKey: ['calendar', 'me-events'] });
}

/**
 * useDiscoverableCalendarsQuery — list teammate personal calendars
 * the actor can subscribe to in `wsId`.
 *
 * Disabled when `wsId` is null/empty so the drawer can mount before a
 * workspace is picked. The query is non-suspense — the drawer renders
 * its own loading state alongside the route.
 */
export function useDiscoverableCalendarsQuery(
  wsId: string | null,
): UseQueryResult<DiscoverableCalendar[], ApiError> {
  const id = wsId ?? '';
  return useQuery<DiscoverableCalendar[], ApiError>({
    queryKey: calendarsRailKeys.discoverable(id),
    enabled: id.length > 0,
    staleTime: 30_000,
    queryFn: async (): Promise<DiscoverableCalendar[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/discoverable-calendars', {
            params: { path: { wsId: id } },
          }),
        'Failed to load discoverable calendars',
      );
      return data.calendars ?? [];
    },
  });
}

export interface SubscribeToCalendarArgs {
  wsId: string;
  calId: string;
}

/**
 * useSubscribeToCalendarMutation — POST
 * `/workspaces/{wsId}/calendars/{calId}/subscribe`. Idempotent on the
 * backend; the rail simply invalidates the calendar list on success
 * so the new row shows up.
 */
export function useSubscribeToCalendarMutation(): UseMutationResult<
  void,
  ApiError,
  SubscribeToCalendarArgs
> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, SubscribeToCalendarArgs>({
    mutationFn: async ({ wsId, calId }): Promise<void> => {
      await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/calendars/{calId}/subscribe', {
            params: { path: { wsId, calId } },
          }),
        'Failed to subscribe to calendar',
      );
    },
    onSuccess: (_void, { wsId }) => {
      invalidateRailCaches(qc, wsId);
    },
  });
}

export interface PatchOwnSubscriptionArgs {
  wsId: string;
  calId: string;
  body: PatchOwnSubscriptionBody;
}

/**
 * usePatchOwnSubscriptionMutation — PATCH
 * `/workspaces/{wsId}/calendars/{calId}/subscription`. The path
 * carries no userId; the backend resolves the subscription against
 * the authenticated caller.
 */
export function usePatchOwnSubscriptionMutation(): UseMutationResult<
  void,
  ApiError,
  PatchOwnSubscriptionArgs
> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, PatchOwnSubscriptionArgs>({
    mutationFn: async ({ wsId, calId, body }): Promise<void> => {
      await apiRequest(
        (client) =>
          client.PATCH('/workspaces/{wsId}/calendars/{calId}/subscription', {
            params: { path: { wsId, calId } },
            body,
          }),
        'Failed to update subscription',
      );
    },
    onSuccess: (_void, { wsId }) => {
      // PATCH does not change discoverability, so we only refresh the
      // subscribed list + the event aggregate (visibility flips drop
      // events out of the grid).
      void qc.invalidateQueries({ queryKey: ['calendar-events', 'calendars', wsId] });
      void qc.invalidateQueries({ queryKey: ['calendar', 'me-events'] });
    },
  });
}

export interface UnsubscribeArgs {
  wsId: string;
  calId: string;
  /** Actor's own user public id when leaving a teammate calendar. */
  userId: string;
}

/**
 * useUnsubscribeMutation — DELETE
 * `/workspaces/{wsId}/calendars/{calId}/members/{userId}`. The rail
 * uses this for self-leave by passing the actor's own user public id
 * as `userId`.
 */
export function useUnsubscribeMutation(): UseMutationResult<void, ApiError, UnsubscribeArgs> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, UnsubscribeArgs>({
    mutationFn: async ({ wsId, calId, userId }): Promise<void> => {
      await apiRequest(
        (client) =>
          client.DELETE('/workspaces/{wsId}/calendars/{calId}/members/{userId}', {
            params: { path: { wsId, calId, userId } },
          }),
        'Failed to leave calendar',
      );
    },
    onSuccess: (_void, { wsId }) => {
      invalidateRailCaches(qc, wsId);
    },
  });
}
