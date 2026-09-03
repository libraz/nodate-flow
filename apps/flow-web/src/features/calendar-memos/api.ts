/**
 * Calendar memos feature — react-query hooks for the per-calendar
 * "Memos" surface.
 *
 * Each memo is a titled todo bound to a single calendar:
 *
 *   - {@link useMemosQuery}            GET    /workspaces/{wsId}/calendars/{calId}/memos
 *   - {@link useCreateMemoMutation}    POST   /workspaces/{wsId}/calendars/{calId}/memos
 *   - {@link useUpdateMemoMutation}    PATCH  /workspaces/{wsId}/calendars/{calId}/memos/{memoId}
 *   - {@link useDeleteMemoMutation}    DELETE /workspaces/{wsId}/calendars/{calId}/memos/{memoId}
 *
 * Cache key shape: `['calendars', wsId, calId, 'memos']`. The
 * `['calendars', wsId]` prefix dovetails with the realtime stream
 * ({@link import('../realtime/event-to-keys').keysForEvent}) which
 * invalidates `['calendars', ws]` on `calendar.changed`. So memo writes
 * landed by other actors flow into this cache automatically — no
 * dedicated SSE wiring is required.
 *
 * Optimistic updates are applied for create / update / delete so the
 * autosave path on the title editor and the checkbox toggle feel
 * instant. On error we roll back to the snapshot captured in
 * `onMutate`. The list is also refetched in `onSettled` so server
 * truth (e.g. the real `id` / `createdAt`) replaces the optimistic
 * placeholder eventually.
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

/** Single memo row as returned by the list / create endpoints. */
export type Memo = components['schemas']['MemoResponse'];

export type CreateMemoBody = components['schemas']['CreateMemoInputBody'];
export type UpdateMemoBody = components['schemas']['UpdateMemoInputBody'];

/**
 * Query key factory for the memos cache. The `['calendars', wsId]`
 * prefix matches the SSE invalidator so realtime fan-out lands here
 * without any extra wiring. The `calId` segment scopes per-calendar.
 */
export const calendarMemosKeys = {
  list: (wsId: string, calId: string) => ['calendars', wsId, calId, 'memos'] as const,
};

/**
 * Stable temporary id for an optimistic memo. Prefixed so it cannot
 * collide with a real backend public id and so the consumer can
 * detect (and disable) optimistic-only rows when needed.
 */
function makeTempId(): string {
  return `optimistic-${crypto.randomUUID()}`;
}

/**
 * GET `/workspaces/{wsId}/calendars/{calId}/memos`. Returns the memo
 * list for a single calendar. Disabled when either id is empty so the
 * hook can be mounted before a calendar is selected. Stale time is 60s
 * because realtime SSE owns timely freshness.
 */
export function useMemosQuery(wsId: string, calId: string): UseQueryResult<Memo[], ApiError> {
  return useQuery<Memo[], ApiError>({
    queryKey: calendarMemosKeys.list(wsId, calId),
    enabled: wsId.length > 0 && calId.length > 0,
    staleTime: 60_000,
    queryFn: async (): Promise<Memo[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/calendars/{calId}/memos', {
            params: { path: { wsId, calId } },
          }),
        'Failed to load memos',
      );
      return data.memos ?? [];
    },
  });
}

export interface CreateMemoArgs {
  wsId: string;
  calId: string;
  title: string;
  sortWeight?: number;
}

interface CreateMemoContext {
  previous: Memo[] | undefined;
  tempId: string;
}

/**
 * POST `/workspaces/{wsId}/calendars/{calId}/memos` with optimistic
 * insertion. The placeholder row uses a `optimistic-*` id which the
 * settle-time refetch replaces with the server-assigned public id.
 */
export function useCreateMemoMutation(): UseMutationResult<
  Memo,
  ApiError,
  CreateMemoArgs,
  CreateMemoContext
> {
  const qc = useQueryClient();
  return useMutation<Memo, ApiError, CreateMemoArgs, CreateMemoContext>({
    mutationFn: async ({ wsId, calId, title, sortWeight }): Promise<Memo> => {
      const body: CreateMemoBody = sortWeight === undefined ? { title } : { title, sortWeight };
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/calendars/{calId}/memos', {
            params: { path: { wsId, calId } },
            body,
          }),
        'Failed to create memo',
      );
      return data;
    },
    onMutate: async (vars): Promise<CreateMemoContext> => {
      const key = calendarMemosKeys.list(vars.wsId, vars.calId);
      await qc.cancelQueries({ queryKey: key });
      const previous = qc.getQueryData<Memo[]>(key);
      const tempId = makeTempId();
      const now = Math.floor(Date.now() / 1000);
      const placeholder: Memo = {
        id: tempId,
        title: vars.title,
        done: false,
        sortWeight: vars.sortWeight ?? (previous?.length ?? 0) + 1,
        userDisplayName: '',
        userPublicId: '',
        createdAt: now,
      };
      qc.setQueryData<Memo[]>(key, [...(previous ?? []), placeholder]);
      return { previous, tempId };
    },
    onError: (_err, vars, ctx) => {
      if (ctx?.previous) {
        qc.setQueryData<Memo[]>(calendarMemosKeys.list(vars.wsId, vars.calId), ctx.previous);
      }
    },
    onSettled: (_data, _err, vars) => {
      void qc.invalidateQueries({
        queryKey: calendarMemosKeys.list(vars.wsId, vars.calId),
      });
    },
  });
}

export interface UpdateMemoArgs {
  wsId: string;
  calId: string;
  memoId: string;
  body: UpdateMemoBody;
}

interface UpdateMemoContext {
  previous: Memo[] | undefined;
}

/**
 * PATCH `/workspaces/{wsId}/calendars/{calId}/memos/{memoId}` with
 * optimistic merge. Powers the title autosave (debounced) and the done
 * checkbox toggle, both of which need to feel instant.
 */
export function useUpdateMemoMutation(): UseMutationResult<
  void,
  ApiError,
  UpdateMemoArgs,
  UpdateMemoContext
> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, UpdateMemoArgs, UpdateMemoContext>({
    mutationFn: async ({ wsId, calId, memoId, body }): Promise<void> => {
      await apiRequest(
        (client) =>
          client.PATCH('/workspaces/{wsId}/calendars/{calId}/memos/{memoId}', {
            params: { path: { wsId, calId, memoId } },
            body,
          }),
        'Failed to update memo',
      );
    },
    onMutate: async (vars): Promise<UpdateMemoContext> => {
      const key = calendarMemosKeys.list(vars.wsId, vars.calId);
      await qc.cancelQueries({ queryKey: key });
      const previous = qc.getQueryData<Memo[]>(key);
      if (previous) {
        const next = previous.map((m) => (m.id === vars.memoId ? { ...m, ...vars.body } : m));
        qc.setQueryData<Memo[]>(key, next);
      }
      return { previous };
    },
    onError: (_err, vars, ctx) => {
      if (ctx?.previous) {
        qc.setQueryData<Memo[]>(calendarMemosKeys.list(vars.wsId, vars.calId), ctx.previous);
      }
    },
    onSettled: (_data, _err, vars) => {
      void qc.invalidateQueries({
        queryKey: calendarMemosKeys.list(vars.wsId, vars.calId),
      });
    },
  });
}

export interface DeleteMemoArgs {
  wsId: string;
  calId: string;
  memoId: string;
}

interface DeleteMemoContext {
  previous: Memo[] | undefined;
}

/**
 * DELETE `/workspaces/{wsId}/calendars/{calId}/memos/{memoId}` with
 * optimistic removal. Rolls back if the server rejects the delete.
 */
export function useDeleteMemoMutation(): UseMutationResult<
  void,
  ApiError,
  DeleteMemoArgs,
  DeleteMemoContext
> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, DeleteMemoArgs, DeleteMemoContext>({
    mutationFn: async ({ wsId, calId, memoId }): Promise<void> => {
      await apiRequest(
        (client) =>
          client.DELETE('/workspaces/{wsId}/calendars/{calId}/memos/{memoId}', {
            params: { path: { wsId, calId, memoId } },
          }),
        'Failed to delete memo',
      );
    },
    onMutate: async (vars): Promise<DeleteMemoContext> => {
      const key = calendarMemosKeys.list(vars.wsId, vars.calId);
      await qc.cancelQueries({ queryKey: key });
      const previous = qc.getQueryData<Memo[]>(key);
      if (previous) {
        qc.setQueryData<Memo[]>(
          key,
          previous.filter((m) => m.id !== vars.memoId),
        );
      }
      return { previous };
    },
    onError: (_err, vars, ctx) => {
      if (ctx?.previous) {
        qc.setQueryData<Memo[]>(calendarMemosKeys.list(vars.wsId, vars.calId), ctx.previous);
      }
    },
    onSettled: (_data, _err, vars) => {
      void qc.invalidateQueries({
        queryKey: calendarMemosKeys.list(vars.wsId, vars.calId),
      });
    },
  });
}
