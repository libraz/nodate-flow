/**
 * @file Timeboxes feature — typed queries, mutations, and a fan-out
 * helper for the persistent app-shell active-timebox bar.
 *
 * The endpoints surfaced here are workspace-scoped and back the
 * `/workspaces/{wsId}/timeboxes` route plus the always-on
 * `<ActiveTimeboxBar />` mounted in the app shell.
 *
 * Day-granularity is intentional: the backend models a timebox as a
 * named span between two `YYYY-MM-DD` dates with a status lifecycle
 * (`planned -> active -> completed | cancelled`). There is no
 * sub-day timer field, so the "active" surface is a currently-running
 * indicator, not a stopwatch.
 *
 * All hooks normalise errors via the shared {@link ApiError} helper so
 * route-level boundaries can branch on `code`.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseQueryResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { ApiError, toApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/** Single timebox DTO as returned by list / detail / mutation endpoints. */
export type Timebox = components['schemas']['TimeboxDTO'];
/** Lightweight task projection inside a timebox (id + state + dates). */
export type TimeboxTask = components['schemas']['TimeboxTaskDTO'];
/** Status lifecycle: `planned -> active -> completed | cancelled`. */
export type TimeboxStatus = 'planned' | 'active' | 'completed' | 'cancelled';

/** Body for `POST /workspaces/{wsId}/timeboxes`. */
export type CreateTimeboxInput = components['schemas']['CreateTimeboxBody'];
/** Body for `PATCH /workspaces/{wsId}/timeboxes/{timeboxId}`. */
export type UpdateTimeboxInput = components['schemas']['UpdateTimeboxBody'];

export { ApiError as TimeboxApiError };

/** Allowed lifecycle states. Mirrors the API's `UpdateTimeboxStatusBody.status`. */
const STATUS_VALUES: readonly TimeboxStatus[] = [
  'planned',
  'active',
  'completed',
  'cancelled',
] as const;

/** Narrow the API's loose `status: string` field to the typed union. */
export function asTimeboxStatus(raw: string): TimeboxStatus {
  return (STATUS_VALUES as readonly string[]).includes(raw) ? (raw as TimeboxStatus) : 'planned';
}

// ---------------------------------------------------------------------------
// Query key factory
// ---------------------------------------------------------------------------

/** Query key factory for the timeboxes feature. */
export const timeboxKeys = {
  all: ['timeboxes'] as const,
  list: (wsId: string) => ['timeboxes', wsId] as const,
  detail: (wsId: string, timeboxId: string) => ['timeboxes', wsId, 'detail', timeboxId] as const,
  tasks: (wsId: string, timeboxId: string) => ['timeboxes', wsId, 'tasks', timeboxId] as const,
};

// ---------------------------------------------------------------------------
// Query hooks
// ---------------------------------------------------------------------------

/** GET /workspaces/{wsId}/timeboxes — workspace timebox list (suspense). */
export function useTimeboxesQuery(wsId: string): UseSuspenseQueryResult<Timebox[]> {
  return useSuspenseQuery({
    queryKey: timeboxKeys.list(wsId),
    queryFn: async (): Promise<Timebox[]> => {
      if (!wsId) return [];
      const { data, error } = await sdk.GET('/workspaces/{wsId}/timeboxes', {
        params: { path: { wsId }, query: { limit: 200 } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load timeboxes');
      return data.timeboxes ?? [];
    },
  });
}

/** GET /workspaces/{wsId}/timeboxes/{timeboxId} — single timebox (suspense). */
export function useTimeboxQuery(wsId: string, timeboxId: string): UseSuspenseQueryResult<Timebox> {
  return useSuspenseQuery({
    queryKey: timeboxKeys.detail(wsId, timeboxId),
    queryFn: async (): Promise<Timebox> => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}/timeboxes/{timeboxId}', {
        params: { path: { wsId, timeboxId } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load timebox');
      return data;
    },
  });
}

/**
 * GET /workspaces/{wsId}/timeboxes/{timeboxId}/tasks — list of tasks
 * linked to a single timebox. Non-suspense + gated by `enabled` so
 * collapsible cards can defer the fetch until the user expands them.
 */
export function useTimeboxTasksQuery(
  wsId: string,
  timeboxId: string,
  enabled = true,
): UseQueryResult<TimeboxTask[], ApiError> {
  return useQuery<TimeboxTask[], ApiError>({
    queryKey: timeboxKeys.tasks(wsId, timeboxId),
    enabled: enabled && wsId.length > 0 && timeboxId.length > 0,
    queryFn: async (): Promise<TimeboxTask[]> => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}/timeboxes/{timeboxId}/tasks', {
        params: { path: { wsId, timeboxId }, query: { limit: 200 } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load timebox tasks');
      return data.tasks ?? [];
    },
  });
}

// ---------------------------------------------------------------------------
// Active-timebox fan-out (drives the app-shell bar)
// ---------------------------------------------------------------------------

/** Active-timebox row with its owning workspace id, used by the persistent bar. */
export interface ActiveTimeboxRow {
  workspaceId: string;
  timebox: Timebox;
}

/**
 * Fans out a list query per workspace and returns the flat set of
 * timeboxes whose `status === 'active'`. Powers the always-on
 * app-shell bar without requiring a dedicated "current session"
 * endpoint.
 *
 * Uses `useQueries` so a slow workspace does not hold up the rest, and
 * silences errors per-workspace so a single ACL failure does not
 * collapse the bar.
 */
export function useActiveTimeboxesQuery(workspaceIds: readonly string[]): {
  active: ActiveTimeboxRow[];
  isLoading: boolean;
} {
  const queries = useQueries({
    queries: workspaceIds.map((wsId) => ({
      queryKey: timeboxKeys.list(wsId),
      // Polling cadence stays at the QueryClient default — invalidations
      // from the create/update mutations push fresh data into the cache
      // so the bar reacts without a refetch interval.
      enabled: wsId.length > 0,
      throwOnError: false,
      queryFn: async (): Promise<Timebox[]> => {
        const { data, error } = await sdk.GET('/workspaces/{wsId}/timeboxes', {
          params: { path: { wsId }, query: { limit: 200 } },
        });
        if (error || !data) return [];
        return data.timeboxes ?? [];
      },
    })),
  });

  const active: ActiveTimeboxRow[] = [];
  let isLoading = false;
  queries.forEach((q, idx) => {
    if (q.isPending) isLoading = true;
    const wsId = workspaceIds[idx];
    if (!wsId || !q.data) return;
    for (const tb of q.data) {
      if (asTimeboxStatus(tb.status) === 'active') {
        active.push({ workspaceId: wsId, timebox: tb });
      }
    }
  });
  return { active, isLoading };
}

// ---------------------------------------------------------------------------
// Mutations
// ---------------------------------------------------------------------------

export interface CreateTimeboxArgs {
  wsId: string;
  body: CreateTimeboxInput;
}

/** POST /workspaces/{wsId}/timeboxes — create a new timebox. */
export function useCreateTimeboxMutation(): UseMutationResult<
  Timebox,
  ApiError,
  CreateTimeboxArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ wsId, body }: CreateTimeboxArgs): Promise<Timebox> => {
      const { data, error } = await sdk.POST('/workspaces/{wsId}/timeboxes', {
        params: { path: { wsId } },
        body,
      });
      if (error || !data) throw toApiError(error, 'Failed to create timebox');
      return data;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: timeboxKeys.list(vars.wsId) });
    },
  });
}

export interface UpdateTimeboxArgs {
  wsId: string;
  timeboxId: string;
  body: UpdateTimeboxInput;
}

/** PATCH /workspaces/{wsId}/timeboxes/{timeboxId} — patch metadata. */
export function useUpdateTimeboxMutation(): UseMutationResult<
  Timebox,
  ApiError,
  UpdateTimeboxArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ wsId, timeboxId, body }: UpdateTimeboxArgs): Promise<Timebox> => {
      const { data, error } = await sdk.PATCH('/workspaces/{wsId}/timeboxes/{timeboxId}', {
        params: { path: { wsId, timeboxId } },
        body,
      });
      if (error || !data) throw toApiError(error, 'Failed to update timebox');
      return data;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: timeboxKeys.list(vars.wsId) });
      void qc.invalidateQueries({
        queryKey: timeboxKeys.detail(vars.wsId, vars.timeboxId),
      });
    },
  });
}

export interface DeleteTimeboxArgs {
  wsId: string;
  timeboxId: string;
}

/** DELETE /workspaces/{wsId}/timeboxes/{timeboxId} — soft delete. */
export function useDeleteTimeboxMutation(): UseMutationResult<void, ApiError, DeleteTimeboxArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ wsId, timeboxId }: DeleteTimeboxArgs): Promise<void> => {
      const { error } = await sdk.DELETE('/workspaces/{wsId}/timeboxes/{timeboxId}', {
        params: { path: { wsId, timeboxId } },
      });
      if (error) throw toApiError(error, 'Failed to delete timebox');
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: timeboxKeys.list(vars.wsId) });
    },
  });
}

export interface UpdateTimeboxStatusArgs {
  wsId: string;
  timeboxId: string;
  status: TimeboxStatus;
}

/** POST /workspaces/{wsId}/timeboxes/{timeboxId}/status — transition state. */
export function useUpdateTimeboxStatusMutation(): UseMutationResult<
  Timebox,
  ApiError,
  UpdateTimeboxStatusArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ wsId, timeboxId, status }: UpdateTimeboxStatusArgs): Promise<Timebox> => {
      const { data, error } = await sdk.POST('/workspaces/{wsId}/timeboxes/{timeboxId}/status', {
        params: { path: { wsId, timeboxId } },
        body: { status },
      });
      if (error || !data) throw toApiError(error, 'Failed to update status');
      return data;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: timeboxKeys.list(vars.wsId) });
      void qc.invalidateQueries({
        queryKey: timeboxKeys.detail(vars.wsId, vars.timeboxId),
      });
    },
  });
}

export interface AddTimeboxTaskArgs {
  wsId: string;
  timeboxId: string;
  taskId: string;
}

/** POST /workspaces/{wsId}/timeboxes/{timeboxId}/tasks — link a task. */
export function useAddTimeboxTaskMutation(): UseMutationResult<void, ApiError, AddTimeboxTaskArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ wsId, timeboxId, taskId }: AddTimeboxTaskArgs): Promise<void> => {
      const { error } = await sdk.POST('/workspaces/{wsId}/timeboxes/{timeboxId}/tasks', {
        params: { path: { wsId, timeboxId } },
        body: { taskId },
      });
      if (error) throw toApiError(error, 'Failed to add task to timebox');
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: timeboxKeys.tasks(vars.wsId, vars.timeboxId),
      });
      // Progress numerator depends on the linked task list — refresh
      // the workspace list as well so derived UI counters update.
      void qc.invalidateQueries({ queryKey: timeboxKeys.list(vars.wsId) });
    },
  });
}

export interface RemoveTimeboxTaskArgs {
  wsId: string;
  timeboxId: string;
  taskId: string;
}

/** DELETE /workspaces/{wsId}/timeboxes/{timeboxId}/tasks/{taskId} — unlink a task. */
export function useRemoveTimeboxTaskMutation(): UseMutationResult<
  void,
  ApiError,
  RemoveTimeboxTaskArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ wsId, timeboxId, taskId }: RemoveTimeboxTaskArgs): Promise<void> => {
      const { error } = await sdk.DELETE(
        '/workspaces/{wsId}/timeboxes/{timeboxId}/tasks/{taskId}',
        { params: { path: { wsId, timeboxId, taskId } } },
      );
      if (error) throw toApiError(error, 'Failed to remove task from timebox');
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: timeboxKeys.tasks(vars.wsId, vars.timeboxId),
      });
      void qc.invalidateQueries({ queryKey: timeboxKeys.list(vars.wsId) });
    },
  });
}
