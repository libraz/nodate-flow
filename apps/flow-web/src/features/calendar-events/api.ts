/**
 * Calendar events feature — calendar listing, default-calendar resolution,
 * and the create / patch / delete mutations backing the unified event
 * create/edit dialog.
 *
 * The /calendar route itself consumes the cross-workspace
 * `/me/calendar-events` aggregate and never needs the per-workspace
 * calendar list. The create/edit dialog does — it must bind a `calId`
 * to POST / PATCH `/workspaces/{wsId}/calendars/{calId}/events[/{evtId}]`.
 * These hooks isolate that lookup and the "remember last used" policy.
 *
 * Cache invalidation policy
 * -------------------------
 *   - Event Create / Update / Delete → invalidate the two calendar
 *     aggregate roots (`['calendar', 'me-events']` and
 *     `['calendar', 'me-tasks']`) via {@link invalidateCalendarAggregates}.
 *     Event-detail key invalidation lives in `events/api.ts`; this
 *     module focuses on the grid.
 *   - {@link useCreateCalendarTask} → invalidate
 *     `['calendar', 'me-tasks']` and `['me', 'tasks']` so both the
 *     calendar grid and any "my tasks" surface pick up the new row.
 *     This is intentionally narrower than the cross-project list nuke
 *     in tasks/api.ts because the dialog only ever creates a task in
 *     a single project at a time and the grid is the only surface
 *     that observes the result.
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
import { eventDetailKeys } from '../events/api';

export type Calendar = components['schemas']['CalendarResponse'];
export type CalendarEventResponse = components['schemas']['EventResponse'];
export type CreateEventInput = components['schemas']['CreateEventInputBody'];
export type PatchEventInput = components['schemas']['PatchEventInputBody'];

/**
 * Which occurrences of a repeating series a write reaches.
 *
 * Derived from the request body rather than restated so the vocabulary
 * cannot drift from the document the API validates against. Omitting the
 * member entirely means `series`, which is what every write did before
 * the choice existed — so a caller that does not know about scopes keeps
 * behaving exactly as it did.
 */
export type RecurrenceScope = NonNullable<PatchEventInput['scope']>;

type FlowTask = components['schemas']['Task'];
export type CreateTaskInput = components['schemas']['CreateTaskBody'];

/** Roles that grant event-edit permission on a calendar. */
const WRITABLE_ROLES: ReadonlySet<string> = new Set(['owner', 'manager', 'editor']);

const LAST_CALENDAR_KEY_PREFIX = 'nf:lastCalendar:';

/** Build the localStorage key for a given workspace's last-used calendar. */
function storageKey(workspaceId: string): string {
  return `${LAST_CALENDAR_KEY_PREFIX}${workspaceId}`;
}

/** Read the remembered calendar id for the workspace, or null. */
function readRemembered(workspaceId: string): string | null {
  try {
    return window.localStorage.getItem(storageKey(workspaceId));
  } catch {
    // Private-mode / SSR / disabled storage: fall back to no memory.
    return null;
  }
}

/**
 * useCalendarsQuery — list of calendars for the given workspace.
 *
 * Disabled when `workspaceId` is null/empty so we do not fire against
 * `/workspaces//calendars`. Callers treat `data` as `undefined` while
 * loading or disabled; no Suspense integration — the dialog opens over
 * an already-rendered route.
 */
export function useCalendarsQuery(workspaceId: string | null): UseQueryResult<Calendar[], Error> {
  const wsId = workspaceId ?? '';
  return useQuery<Calendar[], Error>({
    queryKey: ['calendar-events', 'calendars', wsId] as const,
    enabled: wsId.length > 0,
    staleTime: 60_000,
    queryFn: async (): Promise<Calendar[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/calendars', {
            params: { path: { wsId } },
          }),
        'Failed to load calendars',
      );
      return data.calendars ?? [];
    },
  });
}

/**
 * useDefaultCalendarId — resolves which calendar the event dialog should
 * preselect for `workspaceId`.
 *
 * Resolution order:
 *   1. localStorage `nf:lastCalendar:<wsId>`, if that id is still in the
 *      current calendar list.
 *   2. The first writable calendar (role in {owner, manager, editor}).
 *   3. The first calendar, if none report a writable role.
 *
 * Returns null while the list is loading, when the workspace is null,
 * or when the workspace has no calendars. Pure derivation — never writes.
 */
export function useDefaultCalendarId(workspaceId: string | null): string | null {
  const query = useCalendarsQuery(workspaceId);
  const calendars = query.data;
  if (!workspaceId || !calendars || calendars.length === 0) return null;

  const remembered = readRemembered(workspaceId);
  if (remembered && calendars.some((c) => c.id === remembered)) {
    return remembered;
  }

  const writable = calendars.find((c) => WRITABLE_ROLES.has(c.role));
  if (writable) return writable.id;

  const first = calendars[0];
  return first ? first.id : null;
}

/**
 * rememberCalendarChoice — persist the last calendar the user picked
 * for this workspace so the next dialog opens on the same selection.
 * Call from the dialog's create/edit success handler.
 */
export function rememberCalendarChoice(workspaceId: string, calendarId: string): void {
  try {
    window.localStorage.setItem(storageKey(workspaceId), calendarId);
  } catch {
    // Storage unavailable (private mode, quota, SSR): memory is a
    // nice-to-have, not a correctness requirement.
  }
}

/**
 * useEventDetailQuery — GET one event with its full body.
 *
 * The grid feeds the edit dialog a `MyCalendarEventResponse`, which is an
 * aggregate projection: it carries no `memo`, so a dialog seeded from it
 * alone cannot tell "this event has no memo" from "the memo was never
 * loaded". Editing anything else would then round-trip an empty memo back
 * over a stored one. This hook reads the authoritative row so the dialog
 * hydrates from complete values.
 *
 * Shares {@link eventDetailKeys} with the detail route so both surfaces
 * read one cache entry and both are invalidated by the same mutations.
 * Not a Suspense query — the dialog opens over an already-rendered route
 * and hydrates in place rather than blocking its own first paint.
 */
export function useEventDetailQuery(
  workspaceId: string,
  calendarId: string,
  eventId: string,
  enabled: boolean,
): UseQueryResult<CalendarEventResponse, ApiError> {
  return useQuery<CalendarEventResponse, ApiError>({
    queryKey: eventDetailKeys.detail(workspaceId, calendarId, eventId),
    enabled: enabled && workspaceId !== '' && calendarId !== '' && eventId !== '',
    queryFn: async (): Promise<CalendarEventResponse> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/calendars/{calId}/events/{evtId}', {
            params: { path: { wsId: workspaceId, calId: calendarId, evtId: eventId } },
          }),
        'Failed to load event',
      );
      return data;
    },
  });
}

/** Invalidate the two aggregate calendar query roots after any event write. */
function invalidateCalendarAggregates(qc: ReturnType<typeof useQueryClient>): void {
  void qc.invalidateQueries({ queryKey: ['calendar', 'me-events'] });
  void qc.invalidateQueries({ queryKey: ['calendar', 'me-tasks'] });
}

/**
 * useRefreshCalendar — re-read the calendar aggregates on demand.
 *
 * The mutations above already do this when they succeed. This exists for
 * the case they cannot cover: a dialog dismissed while its write is
 * still in flight. Nothing recalls a request that has left, so at that
 * moment the client genuinely does not know whether the write landed —
 * and a scoped write against a series is not something the user can
 * settle by looking at one row. Re-reading is the only answer available,
 * and it is worth taking twice: once when the user walks away, once more
 * when the request eventually settles.
 */
export function useRefreshCalendar(): () => void {
  const qc = useQueryClient();
  return () => {
    invalidateCalendarAggregates(qc);
  };
}

export interface CreateEventArgs {
  workspaceId: string;
  calendarId: string;
  body: CreateEventInput;
}

/**
 * useCreateEvent — POST /workspaces/{wsId}/calendars/{calId}/events.
 *
 * Invalidates the cross-workspace `['calendar', 'me-events']` aggregate on
 * success so the /calendar month grid picks up the new row.
 */
export function useCreateEvent(): UseMutationResult<
  CalendarEventResponse,
  ApiError,
  CreateEventArgs
> {
  const qc = useQueryClient();
  return useMutation<CalendarEventResponse, ApiError, CreateEventArgs>({
    mutationFn: async ({ workspaceId, calendarId, body }): Promise<CalendarEventResponse> => {
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/calendars/{calId}/events', {
            params: { path: { wsId: workspaceId, calId: calendarId } },
            body,
          }),
        'Failed to create event',
      );
      return data;
    },
    onSuccess: () => {
      invalidateCalendarAggregates(qc);
    },
  });
}

export interface UpdateEventArgs {
  workspaceId: string;
  calendarId: string;
  eventId: string;
  body: PatchEventInput;
}

/** useUpdateEvent — PATCH /workspaces/{wsId}/calendars/{calId}/events/{evtId}. */
export function useUpdateEvent(): UseMutationResult<
  CalendarEventResponse,
  ApiError,
  UpdateEventArgs
> {
  const qc = useQueryClient();
  return useMutation<CalendarEventResponse, ApiError, UpdateEventArgs>({
    mutationFn: async ({
      workspaceId,
      calendarId,
      eventId,
      body,
    }): Promise<CalendarEventResponse> => {
      const data = await apiRequest(
        (client) =>
          client.PATCH('/workspaces/{wsId}/calendars/{calId}/events/{evtId}', {
            params: { path: { wsId: workspaceId, calId: calendarId, evtId: eventId } },
            body,
          }),
        'Failed to update event',
      );
      return data;
    },
    onSuccess: () => {
      invalidateCalendarAggregates(qc);
    },
  });
}

export interface DeleteEventArgs {
  workspaceId: string;
  calendarId: string;
  eventId: string;
  /**
   * Which occurrences the delete reaches. Omit for the whole series —
   * the API's own default, and the only shape a non-repeating row has.
   */
  scope?: RecurrenceScope;
  /**
   * The deleted occurrence's start under the series rule, as unix
   * seconds. Required by every scope other than `series`, which is how
   * the API identifies the instance the caller meant.
   */
  occurrenceStart?: number;
}

/**
 * useDeleteEvent — DELETE /workspaces/{wsId}/calendars/{calId}/events/{evtId}.
 *
 * `scope` / `occurrenceStart` travel as query parameters here rather
 * than in a body, since the method carries none.
 */
export function useDeleteEvent(): UseMutationResult<void, ApiError, DeleteEventArgs> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, DeleteEventArgs>({
    mutationFn: async ({
      workspaceId,
      calendarId,
      eventId,
      scope,
      occurrenceStart,
    }): Promise<void> => {
      await apiRequest(
        (client) =>
          client.DELETE('/workspaces/{wsId}/calendars/{calId}/events/{evtId}', {
            params: {
              path: { wsId: workspaceId, calId: calendarId, evtId: eventId },
              query: {
                ...(scope === undefined ? {} : { scope }),
                ...(occurrenceStart === undefined ? {} : { occurrenceStart }),
              },
            },
          }),
        'Failed to delete event',
      );
    },
    onSuccess: () => {
      invalidateCalendarAggregates(qc);
    },
  });
}

/**
 * useCreateTask — flow-api POST /tasks, shared with the rest of the app
 * but kept here so the calendar event dialog can branch on kind without
 * pulling the full tasks mutation graph (which invalidates per-project
 * lists unrelated to the calendar view).
 *
 * Invalidates both `['calendar', 'me-tasks']` and `['me', 'tasks']` so
 * the calendar grid and any "my tasks" surface pick up the new row.
 */
export function useCreateCalendarTask(): UseMutationResult<FlowTask, ApiError, CreateTaskInput> {
  const qc = useQueryClient();
  return useMutation<FlowTask, ApiError, CreateTaskInput>({
    mutationFn: async (input: CreateTaskInput): Promise<FlowTask> => {
      const data = await apiRequest(
        (client) => client.POST('/tasks', { body: input }),
        'Failed to create task',
      );
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['calendar', 'me-tasks'] });
      void qc.invalidateQueries({ queryKey: ['me', 'tasks'] });
    },
  });
}
