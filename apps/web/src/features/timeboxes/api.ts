/**
 * Timeboxes feature — query key factory, types, and hooks for
 * timebox CRUD, status transitions, and task management.
 *
 * Types are defined inline because the SDK may not yet include these
 * endpoints. API calls use raw fetch via the shared base URL and auth
 * store token (same pattern as notifications).
 */

import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { apiBaseUrl } from '../../lib/sdk';
import { authStore } from '../auth/auth-store';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/** Status lifecycle: planned -> active -> completed | cancelled. */
export type TimeboxStatus = 'planned' | 'active' | 'completed' | 'cancelled';

/** Timebox item returned by the list / detail API. Timestamps are unix seconds. */
export interface TimeboxItem {
  id: string;
  projectId?: string;
  projectName?: string;
  creatorId: string;
  creatorDisplayName: string;
  name: string;
  description?: string;
  startsOn: string; // YYYY-MM-DD
  endsOn: string; // YYYY-MM-DD
  status: TimeboxStatus;
  updatedAt: number;
  createdAt: number;
  total: number;
}

/** Body for POST /workspaces/{wsId}/timeboxes. */
export interface CreateTimeboxInput {
  name: string;
  description?: string;
  startsOn: string;
  endsOn: string;
  projectId?: string;
}

/** Body for PATCH /workspaces/{wsId}/timeboxes/{timeboxId}. */
export interface UpdateTimeboxInput {
  name?: string;
  description?: string;
  startsOn?: string;
  endsOn?: string;
}

/** Body for POST /workspaces/{wsId}/timeboxes/{timeboxId}/status. */
export interface UpdateTimeboxStatusInput {
  status: TimeboxStatus;
}

/** Lightweight task reference returned by the timebox tasks endpoint. */
export interface TimeboxTaskItem {
  id: string;
  name: string;
  derivedState: string;
  assigneeId?: string;
  assigneeDisplayName?: string;
  total: number;
}

// ---------------------------------------------------------------------------
// Query key factory
// ---------------------------------------------------------------------------

/** Query key factory for the timeboxes feature. */
export const timeboxKeys = {
  all: ['timeboxes'] as const,
  list: (wsId: string) => [...timeboxKeys.all, 'list', wsId] as const,
  detail: (id: string) => [...timeboxKeys.all, 'detail', id] as const,
  tasks: (id: string) => [...timeboxKeys.all, 'detail', id, 'tasks'] as const,
};

// ---------------------------------------------------------------------------
// Error helper
// ---------------------------------------------------------------------------

/** Lightweight error thrown when an API call fails. */
export class TimeboxApiError extends Error {
  readonly code: string | undefined;
  constructor(code: string | undefined, message: string) {
    super(message);
    this.name = 'TimeboxApiError';
    this.code = code;
  }
}

function toError(err: unknown, fallback: string): TimeboxApiError {
  if (err && typeof err === 'object') {
    const obj = err as { detail?: unknown; title?: unknown; type?: unknown };
    const message =
      (typeof obj.detail === 'string' && obj.detail) ||
      (typeof obj.title === 'string' && obj.title) ||
      fallback;
    const code = typeof obj.type === 'string' ? obj.type : undefined;
    return new TimeboxApiError(code, message);
  }
  return new TimeboxApiError(undefined, fallback);
}

// ---------------------------------------------------------------------------
// Fetch helpers
// ---------------------------------------------------------------------------

function authHeaders(): HeadersInit {
  const token = authStore.getState().accessToken;
  // biome-ignore lint/style/useNamingConvention: HTTP header name
  return token ? { Authorization: `Bearer ${token}` } : {};
}

async function fetchJson<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    ...init,
    credentials: 'include',
    headers: {
      ...authHeaders(),
      ...init?.headers,
    },
  });
  if (!res.ok) {
    const body = (await res.json().catch(() => null)) as unknown;
    throw toError(body, `Request failed with status ${String(res.status)}`);
  }
  return (await res.json()) as T;
}

async function fetchVoid(url: string, init?: RequestInit): Promise<void> {
  const res = await fetch(url, {
    ...init,
    credentials: 'include',
    headers: {
      ...authHeaders(),
      ...init?.headers,
    },
  });
  if (!res.ok) {
    const body = (await res.json().catch(() => null)) as unknown;
    throw toError(body, `Request failed with status ${String(res.status)}`);
  }
}

// ---------------------------------------------------------------------------
// Query hooks
// ---------------------------------------------------------------------------

/** GET /workspaces/{wsId}/timeboxes — suspense query for the timebox list. */
export function useTimeboxesQuery(wsId: string): UseSuspenseQueryResult<TimeboxItem[]> {
  return useSuspenseQuery({
    queryKey: timeboxKeys.list(wsId),
    queryFn: async (): Promise<TimeboxItem[]> => {
      const data = await fetchJson<{ items?: TimeboxItem[] }>(
        `${apiBaseUrl}/workspaces/${wsId}/timeboxes?limit=200`,
      );
      return data.items ?? [];
    },
  });
}

/** GET /workspaces/{wsId}/timeboxes/{timeboxId} — suspense query for a single timebox. */
export function useTimeboxQuery(
  wsId: string,
  timeboxId: string,
): UseSuspenseQueryResult<TimeboxItem> {
  return useSuspenseQuery({
    queryKey: timeboxKeys.detail(timeboxId),
    queryFn: async (): Promise<TimeboxItem> => {
      return fetchJson<TimeboxItem>(`${apiBaseUrl}/workspaces/${wsId}/timeboxes/${timeboxId}`);
    },
  });
}

/** GET /workspaces/{wsId}/timeboxes/{timeboxId}/tasks — suspense query for timebox tasks. */
export function useTimeboxTasksQuery(
  wsId: string,
  timeboxId: string,
): UseSuspenseQueryResult<TimeboxTaskItem[]> {
  return useSuspenseQuery({
    queryKey: timeboxKeys.tasks(timeboxId),
    queryFn: async (): Promise<TimeboxTaskItem[]> => {
      const data = await fetchJson<{ items?: TimeboxTaskItem[] }>(
        `${apiBaseUrl}/workspaces/${wsId}/timeboxes/${timeboxId}/tasks?limit=200`,
      );
      return data.items ?? [];
    },
  });
}

// ---------------------------------------------------------------------------
// Mutation hooks
// ---------------------------------------------------------------------------

export interface CreateTimeboxArgs {
  input: CreateTimeboxInput;
}

/** POST /workspaces/{wsId}/timeboxes — create a new timebox. */
export function useCreateTimebox(
  wsId: string,
): UseMutationResult<TimeboxItem, TimeboxApiError, CreateTimeboxArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ input }: CreateTimeboxArgs): Promise<TimeboxItem> => {
      return fetchJson<TimeboxItem>(`${apiBaseUrl}/workspaces/${wsId}/timeboxes`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      });
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: timeboxKeys.list(wsId) });
    },
  });
}

export interface UpdateTimeboxArgs {
  timeboxId: string;
  patch: UpdateTimeboxInput;
}

/** PATCH /workspaces/{wsId}/timeboxes/{timeboxId} — update a timebox. */
export function useUpdateTimebox(
  wsId: string,
): UseMutationResult<TimeboxItem, TimeboxApiError, UpdateTimeboxArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ timeboxId, patch }: UpdateTimeboxArgs): Promise<TimeboxItem> => {
      return fetchJson<TimeboxItem>(`${apiBaseUrl}/workspaces/${wsId}/timeboxes/${timeboxId}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(patch),
      });
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: timeboxKeys.list(wsId) });
      void qc.invalidateQueries({ queryKey: timeboxKeys.detail(vars.timeboxId) });
    },
  });
}

export interface UpdateTimeboxStatusArgs {
  timeboxId: string;
  status: TimeboxStatus;
}

/** POST /workspaces/{wsId}/timeboxes/{timeboxId}/status — transition status. */
export function useUpdateTimeboxStatus(
  wsId: string,
): UseMutationResult<void, TimeboxApiError, UpdateTimeboxStatusArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ timeboxId, status }: UpdateTimeboxStatusArgs): Promise<void> => {
      await fetchJson<unknown>(`${apiBaseUrl}/workspaces/${wsId}/timeboxes/${timeboxId}/status`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ status }),
      });
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: timeboxKeys.list(wsId) });
      void qc.invalidateQueries({ queryKey: timeboxKeys.detail(vars.timeboxId) });
    },
  });
}

/** DELETE /workspaces/{wsId}/timeboxes/{timeboxId} — soft delete. */
export function useDeleteTimebox(wsId: string): UseMutationResult<void, TimeboxApiError, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (timeboxId: string): Promise<void> => {
      await fetchVoid(`${apiBaseUrl}/workspaces/${wsId}/timeboxes/${timeboxId}`, {
        method: 'DELETE',
      });
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: timeboxKeys.list(wsId) });
    },
  });
}

export interface AddTaskToTimeboxArgs {
  taskId: string;
}

/** POST /workspaces/{wsId}/timeboxes/{timeboxId}/tasks — add a task. */
export function useAddTaskToTimebox(
  wsId: string,
  timeboxId: string,
): UseMutationResult<void, TimeboxApiError, AddTaskToTimeboxArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId }: AddTaskToTimeboxArgs): Promise<void> => {
      await fetchJson<unknown>(`${apiBaseUrl}/workspaces/${wsId}/timeboxes/${timeboxId}/tasks`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ taskId }),
      });
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: timeboxKeys.tasks(timeboxId) });
    },
  });
}

/** DELETE /workspaces/{wsId}/timeboxes/{timeboxId}/tasks/{taskId} — remove a task. */
export function useRemoveTaskFromTimebox(
  wsId: string,
  timeboxId: string,
): UseMutationResult<void, TimeboxApiError, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (taskId: string): Promise<void> => {
      await fetchVoid(`${apiBaseUrl}/workspaces/${wsId}/timeboxes/${timeboxId}/tasks/${taskId}`, {
        method: 'DELETE',
      });
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: timeboxKeys.tasks(timeboxId) });
    },
  });
}
