/**
 * Retro drafts queue — typed query + Accept / Discard mutations.
 *
 * Backend contract (Phase 6 / L2 of
 * docs/plan/release-8-signals-and-judge-loop.md):
 *
 *   GET /workspaces/{wsId}/tasks/drafts?reason=retro&offset=0&limit=20
 *
 * Returns retrospective draft tasks each linked back to a source task
 * via a `task_dependencies` row of kind `retro_of`. The task row itself
 * is created at `derived_state='open'` — the "draft" semantics live on
 * the dependency edge, not on the task state. Promoting a draft to a
 * regular task therefore means removing the `retro_of` edge; archiving
 * the task is the destructive discard path.
 *
 * Accept flow (two-step, atomic from the user's perspective):
 *
 *   1. GET /tasks/{taskId}/dependencies — locate the `retro_of` edge
 *      on the outgoing list.
 *   2. DELETE /tasks/{taskId}/dependencies/{depId}.
 *
 * The optimistic update removes the draft from the cached list on
 * mutate, and rolls it back on error. The drafts query is invalidated
 * on settle so a server-side reconciliation refreshes the row count.
 *
 * Discard flow: POST /tasks/{taskId}/archive — the existing task
 * archive endpoint. Same optimistic + rollback shape.
 */

import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { type ApiError, toApiError } from '../../../lib/api-error';
import { sdk } from '../../../lib/sdk';

/** A single retrospective draft as returned by the API. */
export interface RetroDraft {
  taskPublicId: string;
  title: string;
  description?: string;
  /** Unix seconds. */
  createdAt: number;
  createdByAgentId?: string;
  createdByAgentName?: string;
  sourceTask: {
    publicId: string;
    title: string;
  };
}

/** Response shape for the drafts list endpoint. */
export interface RetroDraftsPage {
  drafts: RetroDraft[];
  total: number;
  /** Echoed back so paginated query keys stay stable. */
  offset: number;
  limit: number;
}

/** Default page size — matches the backend's `default:"20"` Huma tag. */
export const RETRO_DRAFTS_PAGE_SIZE = 20;

/** Query key factory for the retro drafts feed. */
export const retroDraftsKeys = {
  all: ['workspaces'] as const,
  list: (wsId: string, offset: number, limit: number) =>
    ['workspaces', wsId, 'tasks', 'drafts', 'retro', { offset, limit }] as const,
  /** Broad prefix used by mutations to invalidate every paginated slice. */
  listPrefix: (wsId: string) => ['workspaces', wsId, 'tasks', 'drafts', 'retro'] as const,
};

/**
 * Suspense query for one page of retro drafts in the given workspace.
 *
 * The query is keyed on (workspaceId, offset, limit) so the page
 * component can drive pagination without colliding with other slices.
 */
export function useRetroDraftsQuery(
  workspaceId: string,
  offset = 0,
  limit: number = RETRO_DRAFTS_PAGE_SIZE,
): UseSuspenseQueryResult<RetroDraftsPage> {
  return useSuspenseQuery({
    queryKey: retroDraftsKeys.list(workspaceId, offset, limit),
    queryFn: async (): Promise<RetroDraftsPage> => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}/tasks/drafts', {
        params: {
          path: { wsId: workspaceId },
          query: { reason: 'retro', offset, limit },
        },
      });
      if (error || !data) throw toApiError(error, 'Failed to load retrospective drafts');
      // The SDK types the array as nullable because Huma can omit empty
      // slices; normalise to an empty array so the UI never branches on null.
      const drafts: RetroDraft[] = (data.drafts ?? []).map((d) => ({
        taskPublicId: d.taskPublicId,
        title: d.title,
        ...(d.description !== undefined ? { description: d.description } : {}),
        createdAt: d.createdAt,
        ...(d.createdByAgentId !== undefined ? { createdByAgentId: d.createdByAgentId } : {}),
        ...(d.createdByAgentName !== undefined ? { createdByAgentName: d.createdByAgentName } : {}),
        sourceTask: {
          publicId: d.sourceTask.publicId,
          title: d.sourceTask.title,
        },
      }));
      return { drafts, total: data.total, offset, limit };
    },
  });
}

/**
 * Mutation context for the optimistic Accept / Discard flow.
 *
 * `previous` is the snapshot of every cached page for this workspace
 * prefix at the moment the mutation started; on error we replay it
 * verbatim so concurrent paginated views all roll back together.
 */
interface MutationContext {
  previous: Array<[readonly unknown[], RetroDraftsPage | undefined]>;
}

/** Snapshot every cached drafts page for the workspace so error can replay. */
function snapshotDraftsPages(
  qc: ReturnType<typeof useQueryClient>,
  workspaceId: string,
): Array<[readonly unknown[], RetroDraftsPage | undefined]> {
  const entries = qc.getQueriesData<RetroDraftsPage>({
    queryKey: retroDraftsKeys.listPrefix(workspaceId),
  });
  return entries.map(([key, value]) => [key, value]);
}

/** Drop the named draft from every cached page; total adjusts accordingly. */
function removeDraftFromCache(
  qc: ReturnType<typeof useQueryClient>,
  workspaceId: string,
  taskPublicId: string,
): void {
  qc.setQueriesData<RetroDraftsPage>(
    { queryKey: retroDraftsKeys.listPrefix(workspaceId) },
    (old): RetroDraftsPage | undefined => {
      if (!old) return old;
      const next = old.drafts.filter((d) => d.taskPublicId !== taskPublicId);
      if (next.length === old.drafts.length) return old;
      return {
        ...old,
        drafts: next,
        total: Math.max(0, old.total - 1),
      };
    },
  );
}

/** Restore a snapshot taken via {@link snapshotDraftsPages}. */
function restoreDraftsSnapshot(
  qc: ReturnType<typeof useQueryClient>,
  snapshot: Array<[readonly unknown[], RetroDraftsPage | undefined]>,
): void {
  for (const [key, value] of snapshot) {
    qc.setQueryData(key, value);
  }
}

export interface AcceptRetroDraftArgs {
  workspaceId: string;
  taskPublicId: string;
}

/**
 * Accept a retrospective draft — removes the `retro_of` dependency
 * edge so the task is no longer surfaced as a draft.
 *
 * The mutation chains two SDK calls in series because the dependency
 * id is not embedded in the drafts response (the L2 brief defers
 * enrichment fields to a follow-up phase). On the rare case where the
 * edge is absent (e.g. another tab already accepted it) the second
 * call is skipped and the mutation succeeds silently — the row was
 * already off the queue.
 */
export function useAcceptRetroDraft(): UseMutationResult<
  void,
  ApiError,
  AcceptRetroDraftArgs,
  MutationContext
> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, AcceptRetroDraftArgs, MutationContext>({
    mutationFn: async ({ taskPublicId }: AcceptRetroDraftArgs): Promise<void> => {
      const { data, error } = await sdk.GET('/tasks/{id}/dependencies', {
        params: { path: { id: taskPublicId } },
      });
      if (error || !data) {
        throw toApiError(error, 'Failed to accept retrospective draft');
      }
      const outgoing = data.outgoing ?? [];
      const retroEdge = outgoing.find((edge) => edge.kind === 'retro_of');
      // No edge → another client already accepted it; treat as success.
      if (!retroEdge) return;
      const { error: deleteError } = await sdk.DELETE('/tasks/{id}/dependencies/{depId}', {
        params: { path: { id: taskPublicId, depId: retroEdge.id } },
      });
      if (deleteError) {
        throw toApiError(deleteError, 'Failed to accept retrospective draft');
      }
    },
    onMutate: async ({ workspaceId, taskPublicId }): Promise<MutationContext> => {
      await qc.cancelQueries({ queryKey: retroDraftsKeys.listPrefix(workspaceId) });
      const previous = snapshotDraftsPages(qc, workspaceId);
      removeDraftFromCache(qc, workspaceId, taskPublicId);
      return { previous };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx) restoreDraftsSnapshot(qc, ctx.previous);
    },
    onSettled: (_data, _err, vars) => {
      void qc.invalidateQueries({ queryKey: retroDraftsKeys.listPrefix(vars.workspaceId) });
    },
  });
}

export interface DiscardRetroDraftArgs {
  workspaceId: string;
  taskPublicId: string;
}

/**
 * Discard a retrospective draft — archives the task via the existing
 * `POST /tasks/{id}/archive` endpoint. The task remains queryable
 * through the archive surface so this is reversible.
 */
export function useDiscardRetroDraft(): UseMutationResult<
  void,
  ApiError,
  DiscardRetroDraftArgs,
  MutationContext
> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, DiscardRetroDraftArgs, MutationContext>({
    mutationFn: async ({ taskPublicId }: DiscardRetroDraftArgs): Promise<void> => {
      const { error } = await sdk.POST('/tasks/{id}/archive', {
        params: { path: { id: taskPublicId } },
      });
      if (error) throw toApiError(error, 'Failed to discard retrospective draft');
    },
    onMutate: async ({ workspaceId, taskPublicId }): Promise<MutationContext> => {
      await qc.cancelQueries({ queryKey: retroDraftsKeys.listPrefix(workspaceId) });
      const previous = snapshotDraftsPages(qc, workspaceId);
      removeDraftFromCache(qc, workspaceId, taskPublicId);
      return { previous };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx) restoreDraftsSnapshot(qc, ctx.previous);
    },
    onSettled: (_data, _err, vars) => {
      void qc.invalidateQueries({ queryKey: retroDraftsKeys.listPrefix(vars.workspaceId) });
    },
  });
}
