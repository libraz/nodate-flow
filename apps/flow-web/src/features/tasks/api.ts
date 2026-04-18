/**
 * Tasks feature — typed queries and mutations backed by the SDK.
 *
 * All hooks are suspense-ready where applicable and participate in the
 * shared QueryClient (throwOnError, route-level ErrorBoundary).
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseQueryResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQuery,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { sdk } from '../../lib/sdk';
import { timelineKeys } from '../timeline/api';

export type Task = components['schemas']['Task'];
export type TaskListItem = components['schemas']['TaskListItem'];
export type TaskComment = components['schemas']['TaskComment'];
export type TaskActor = components['schemas']['TaskActor'];
export type TaskAttachment = components['schemas']['TaskAttachment'];
export type CreateTaskInput = components['schemas']['CreateTaskBody'];
export type PatchTaskInput = components['schemas']['PatchTaskBody'];
export type TransitionInput = components['schemas']['TransitionTaskBody'];
export type TransitionName = TransitionInput['transition'];
export type AddCommentInput = components['schemas']['AddTaskCommentBody'];
export type AddActorInput = components['schemas']['AddTaskActorBody'];
export type AddAttachmentInput = components['schemas']['AddTaskAttachmentBody'];
export type PresignUploadInput = components['schemas']['PresignUploadBody'];
export type PresignUploadResult = components['schemas']['PresignUploadOutputBody'];
export type DownloadAttachmentResult = components['schemas']['DownloadAttachmentOutputBody'];

/** Backend `derived_state` enum (see sql/tables/tasks.sql). */
export type TaskDerivedState = 'open' | 'waiting' | 'review' | 'done' | 'cancelled';

/** Ordered list of all task states for column rendering. */
export const TASK_STATES: readonly TaskDerivedState[] = [
  'open',
  'waiting',
  'review',
  'done',
  'cancelled',
] as const;

/** Backend priority is int32 0..4. */
export type TaskPriority = 0 | 1 | 2 | 3 | 4;

/** Ordered list of all priority levels for filter rendering. */
export const TASK_PRIORITIES: readonly TaskPriority[] = [0, 1, 2, 3, 4] as const;

/** i18n key map for each priority level. */
export const PRIORITY_I18N_KEY: Record<TaskPriority, string> = {
  0: 'tasks.priority.none',
  1: 'tasks.priority.low',
  2: 'tasks.priority.medium',
  3: 'tasks.priority.high',
  4: 'tasks.priority.urgent',
};

export interface TaskFilters {
  search?: string;
  states?: readonly TaskDerivedState[];
  assigneeId?: string;
  priority?: readonly TaskPriority[];
}

/** Query key factory for the tasks feature. */
export const tasksKeys = {
  all: ['tasks'] as const,
  list: (projectId: string, filters?: TaskFilters) =>
    [...tasksKeys.all, 'list', projectId, filters ?? {}] as const,
  detail: (id: string) => [...tasksKeys.all, 'detail', id] as const,
  comments: (id: string) => [...tasksKeys.all, 'detail', id, 'comments'] as const,
  actors: (id: string) => [...tasksKeys.all, 'detail', id, 'actors'] as const,
  duplicates: (id: string) => [...tasksKeys.all, 'detail', id, 'duplicates'] as const,
  inferState: (id: string) => [...tasksKeys.all, 'detail', id, 'infer-state'] as const,
  aiInvocations: (id: string) => [...tasksKeys.all, 'detail', id, 'ai-invocations'] as const,
  dependencies: (id: string) => [...tasksKeys.all, 'detail', id, 'dependencies'] as const,
  attachments: (id: string) => [...tasksKeys.all, 'detail', id, 'attachments'] as const,
};

export type TaskDependencyEdge = components['schemas']['TaskDependencyEdge'];
export type TaskDependencyKind = 'blocks' | 'relates' | 'duplicates' | 'subtask_of';
export interface TaskDependenciesResult {
  outgoing: TaskDependencyEdge[];
  incoming: TaskDependencyEdge[];
}

export type TaskAiInvocation = components['schemas']['TaskAiInvocation'];

export type InferStateProposal = components['schemas']['InferStateProposal'];
export interface InferStateResult {
  taskId: string;
  state: TaskDerivedState;
  proposal?: InferStateProposal;
}

export type DuplicateCandidate = components['schemas']['DuplicateCandidate'];
export interface DuplicatesResult {
  model: string;
  candidates: DuplicateCandidate[];
}

import { ApiError, toApiError } from '../../lib/api-error';

export { ApiError as TaskApiError };

/**
 * Legal transitions from a given derived state, per ADR 0001 (v1 state
 * machine) as specialized for the board drop / transitions panel UX.
 *
 * Keep this in sync with `transitionForDrop` below — the transitions panel
 * simply enumerates these, while the board infers the right transition from
 * the (from, to) column pair.
 */
export const TRANSITIONS_BY_STATE: Record<TaskDerivedState, readonly TransitionName[]> = {
  // Must stay in lockstep with apps/flow-api/internal/constraint/engine/replay.go
  // (ADR 0001 v1 state machine).
  open: ['start', 'complete', 'cancel'],
  waiting: ['submit', 'block', 'cancel'],
  review: ['complete', 'reopen', 'cancel'],
  done: ['reopen'],
  cancelled: ['reopen'],
};

/**
 * Resolution of a board drag-and-drop drop onto a target column.
 *
 * `transition` is the single state-machine verb the API will accept and
 * `landingState` is the state the task will actually be in afterwards. The
 * landing state can differ from the column the user dropped onto when we
 * leniently resolve a "go back" drop (e.g. dragging a `done` card onto the
 * `open` column resolves to `reopen`, which actually lands in `waiting`).
 * Callers should drive the optimistic cache update from `landingState`, not
 * the drop target, and may want to inform the user when the two differ.
 */
export interface DropResolution {
  transition: TransitionName;
  landingState: TaskDerivedState;
}

/**
 * Map a board column drop onto a state machine transition, or `null` if the
 * drop is illegal. See task spec F7.
 *
 * Mirrors apps/flow-api/internal/http/handlers/tasks/transitions.go#nextState as
 * the canonical truth table. Adjacent legal steps resolve exactly. "Go back"
 * drops that don't have a direct verb (done/cancelled/review onto an earlier
 * column) are leniently resolved to the closest legal `reopen` so the user
 * isn't forced into the side panel just to undo a state change.
 */
export function transitionForDrop(
  from: TaskDerivedState,
  to: TaskDerivedState,
): DropResolution | null {
  if (from === to) return null;
  switch (to) {
    case 'waiting':
      if (from === 'open') return { transition: 'start', landingState: 'waiting' };
      if (from === 'review') return { transition: 'reopen', landingState: 'waiting' };
      if (from === 'done') return { transition: 'reopen', landingState: 'waiting' };
      // Lenient: dragging a cancelled card "back" toward waiting reopens it,
      // which the backend lands in `open`.
      if (from === 'cancelled') return { transition: 'reopen', landingState: 'open' };
      return null;
    case 'review':
      if (from === 'waiting') return { transition: 'submit', landingState: 'review' };
      return null;
    case 'done':
      if (from === 'open') return { transition: 'complete', landingState: 'done' };
      if (from === 'review') return { transition: 'complete', landingState: 'done' };
      return null;
    case 'open':
      if (from === 'waiting') return { transition: 'block', landingState: 'open' };
      if (from === 'cancelled') return { transition: 'reopen', landingState: 'open' };
      // Lenient go-back: done/review dropped on open both reopen, landing in
      // waiting (the only legal target for `reopen` from those states).
      if (from === 'done') return { transition: 'reopen', landingState: 'waiting' };
      if (from === 'review') return { transition: 'reopen', landingState: 'waiting' };
      return null;
    case 'cancelled':
      if (from === 'open' || from === 'waiting' || from === 'review') {
        return { transition: 'cancel', landingState: 'cancelled' };
      }
      // done → cancelled is not reachable in one step on the backend; the
      // user must reopen first, so we reject it locally.
      return null;
    default:
      return null;
  }
}

export function useTasksQuery(
  projectId: string,
  filters?: TaskFilters,
): UseSuspenseQueryResult<TaskListItem[]> {
  return useSuspenseQuery({
    queryKey: tasksKeys.list(projectId, filters),
    queryFn: async (): Promise<TaskListItem[]> => {
      const search = filters?.search?.trim() ?? '';
      const states = filters?.states ?? [];
      const assignee = filters?.assigneeId?.trim() ?? '';
      const priorities = filters?.priority ?? [];
      const query: {
        projectId: string;
        limit: number;
        offset: number;
        q?: string;
        state?: string[];
        assignee?: string;
      } = {
        projectId,
        limit: 200,
        offset: 0,
      };
      if (search.length > 0) query.q = search;
      if (states.length > 0) query.state = [...states];
      if (assignee.length > 0) query.assignee = assignee;
      const { data, error } = await sdk.GET('/tasks', { params: { query } });
      if (error || !data) throw toApiError(error, 'Failed to load tasks');
      const tasks = data.tasks ?? [];
      // Client-side priority filter (API does not support priority param yet)
      if (priorities.length > 0) {
        const allowed = new Set<number>(priorities);
        return tasks.filter((t) => allowed.has(t.priority));
      }
      return tasks;
    },
  });
}

export function useTaskQuery(taskId: string): UseSuspenseQueryResult<Task> {
  return useSuspenseQuery({
    queryKey: tasksKeys.detail(taskId),
    queryFn: async (): Promise<Task> => {
      const { data, error } = await sdk.GET('/tasks/{id}', {
        params: { path: { id: taskId } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load task');
      return data;
    },
  });
}

export function useTaskDuplicatesQuery(taskId: string): UseSuspenseQueryResult<DuplicatesResult> {
  return useSuspenseQuery({
    queryKey: tasksKeys.duplicates(taskId),
    queryFn: async (): Promise<DuplicatesResult> => {
      const { data, error } = await sdk.GET('/tasks/{id}/duplicates', {
        params: { path: { id: taskId } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load duplicates');
      return { model: data.model, candidates: data.candidates ?? [] };
    },
  });
}

export function useTaskInferStateQuery(taskId: string): UseSuspenseQueryResult<InferStateResult> {
  return useSuspenseQuery({
    queryKey: tasksKeys.inferState(taskId),
    queryFn: async (): Promise<InferStateResult> => {
      const { data, error } = await sdk.GET('/tasks/{id}/infer-state', {
        params: { path: { id: taskId } },
      });
      if (error || !data) throw toApiError(error, 'Failed to infer state');
      const result: InferStateResult = {
        taskId: data.taskId,
        state: data.state as TaskDerivedState,
      };
      if (data.proposal) result.proposal = data.proposal;
      return result;
    },
  });
}

export function useTaskAiInvocationsQuery(
  taskId: string,
): UseSuspenseQueryResult<TaskAiInvocation[]> {
  return useSuspenseQuery({
    queryKey: tasksKeys.aiInvocations(taskId),
    queryFn: async (): Promise<TaskAiInvocation[]> => {
      const { data, error } = await sdk.GET('/tasks/{id}/ai/invocations', {
        params: { path: { id: taskId } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load task AI invocations');
      return data.invocations ?? [];
    },
  });
}

export function useTaskActorsQuery(taskId: string): UseSuspenseQueryResult<TaskActor[]> {
  return useSuspenseQuery({
    queryKey: tasksKeys.actors(taskId),
    queryFn: async (): Promise<TaskActor[]> => {
      const { data, error } = await sdk.GET('/tasks/{id}/actors', {
        params: { path: { id: taskId } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load task actors');
      return data.actors ?? [];
    },
  });
}

export interface AddActorArgs {
  taskId: string;
  input: AddActorInput;
}

export function useAddTaskActor(): UseMutationResult<TaskActor, ApiError, AddActorArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, input }: AddActorArgs): Promise<TaskActor> => {
      const { data, error } = await sdk.POST('/tasks/{id}/actors', {
        params: { path: { id: taskId } },
        body: input,
      });
      if (error || !data) throw toApiError(error, 'Failed to add task actor');
      return data;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: tasksKeys.actors(vars.taskId) });
      void qc.invalidateQueries({ queryKey: tasksKeys.detail(vars.taskId) });
      void qc.invalidateQueries({ queryKey: [...timelineKeys.all, 'task', vars.taskId] });
    },
  });
}

export interface RemoveActorArgs {
  taskId: string;
  actorId: string;
}

export function useRemoveTaskActor(): UseMutationResult<void, ApiError, RemoveActorArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, actorId }: RemoveActorArgs): Promise<void> => {
      const { error } = await sdk.DELETE('/tasks/{id}/actors/{actorId}', {
        params: { path: { id: taskId, actorId } },
      });
      if (error) throw toApiError(error, 'Failed to remove task actor');
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: tasksKeys.actors(vars.taskId) });
      void qc.invalidateQueries({ queryKey: tasksKeys.detail(vars.taskId) });
      void qc.invalidateQueries({ queryKey: [...timelineKeys.all, 'task', vars.taskId] });
    },
  });
}

export function useTaskCommentsQuery(taskId: string): UseSuspenseQueryResult<TaskComment[]> {
  return useSuspenseQuery({
    queryKey: tasksKeys.comments(taskId),
    queryFn: async (): Promise<TaskComment[]> => {
      const { data, error } = await sdk.GET('/tasks/{id}/comments', {
        params: { path: { id: taskId } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load comments');
      return data.comments ?? [];
    },
  });
}

export function useCreateTask(): UseMutationResult<Task, ApiError, CreateTaskInput> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: CreateTaskInput): Promise<Task> => {
      const { data, error } = await sdk.POST('/tasks', { body: input });
      if (error || !data) throw toApiError(error, 'Failed to create task');
      return data;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: [...tasksKeys.all, 'list', vars.projectId] });
    },
  });
}

export interface UpdateTaskArgs {
  id: string;
  patch: PatchTaskInput;
}

export function useUpdateTask(): UseMutationResult<Task, ApiError, UpdateTaskArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, patch }: UpdateTaskArgs): Promise<Task> => {
      const { data, error } = await sdk.PATCH('/tasks/{id}', {
        params: { path: { id } },
        body: patch,
      });
      if (error || !data) throw toApiError(error, 'Failed to update task');
      return data;
    },
    onSuccess: (data, vars) => {
      qc.setQueryData(tasksKeys.detail(vars.id), data);
      void qc.invalidateQueries({ queryKey: tasksKeys.detail(vars.id) });
      void qc.invalidateQueries({ queryKey: [...tasksKeys.all, 'list'] });
      void qc.invalidateQueries({ queryKey: [...timelineKeys.all, 'task', vars.id] });
    },
  });
}

export function useDeleteTask(): UseMutationResult<void, ApiError, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string): Promise<void> => {
      const { error } = await sdk.DELETE('/tasks/{id}', {
        params: { path: { id } },
      });
      if (error) throw toApiError(error, 'Failed to delete task');
    },
    onSuccess: (_data, id) => {
      qc.removeQueries({ queryKey: tasksKeys.detail(id) });
      void qc.invalidateQueries({ queryKey: [...tasksKeys.all, 'list'] });
    },
  });
}

export interface AddCommentArgs {
  taskId: string;
  body: string;
}

export function useAddTaskComment(): UseMutationResult<TaskComment, ApiError, AddCommentArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, body }: AddCommentArgs): Promise<TaskComment> => {
      const { data, error } = await sdk.POST('/tasks/{id}/comments', {
        params: { path: { id: taskId } },
        body: { body },
      });
      if (error || !data) throw toApiError(error, 'Failed to add comment');
      return data;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: tasksKeys.comments(vars.taskId) });
      void qc.invalidateQueries({ queryKey: [...timelineKeys.all, 'task', vars.taskId] });
    },
  });
}

export interface TransitionTaskArgs {
  id: string;
  transition: TransitionName;
  /** Optional projectId for cache list invalidation. */
  projectId?: string;
  /** Optional expected target state for optimistic list update. */
  optimisticState?: TaskDerivedState;
}

/**
 * useTransitionTask — POST /tasks/{id}/transitions.
 *
 * Performs optimistic cache updates for `tasks.list` (move the card between
 * columns) and `tasks.detail` (flip derivedState), then reconciles with the
 * authoritative Task returned by the server. Rolls back on error.
 */
export function useTransitionTask(): UseMutationResult<Task, ApiError, TransitionTaskArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, transition }: TransitionTaskArgs): Promise<Task> => {
      const { data, error } = await sdk.POST('/tasks/{id}/transitions', {
        params: { path: { id } },
        body: { transition },
      });
      if (error || !data) throw toApiError(error, 'Failed to apply transition');
      return data;
    },
    onMutate: async (vars) => {
      // Scope cancellation to the affected project list and task detail only.
      // Cancelling `tasksKeys.all` would kill in-flight queries for *other*
      // projects / search results that `onSettled` never re-triggers.
      if (vars.projectId) {
        await qc.cancelQueries({
          queryKey: [...tasksKeys.all, 'list', vars.projectId],
        });
      } else {
        await qc.cancelQueries({ queryKey: [...tasksKeys.all, 'list'] });
      }
      await qc.cancelQueries({ queryKey: tasksKeys.detail(vars.id) });
      const listSnapshots = qc.getQueriesData<TaskListItem[]>({
        queryKey: [...tasksKeys.all, 'list'],
      });
      const detailSnapshot = qc.getQueryData<Task>(tasksKeys.detail(vars.id));
      if (vars.optimisticState) {
        const nextState = vars.optimisticState;
        for (const [key, value] of listSnapshots) {
          if (!value) continue;
          const updated = value.map((task) =>
            task.id === vars.id ? { ...task, derivedState: nextState } : task,
          );
          qc.setQueryData(key, updated);
        }
        if (detailSnapshot) {
          qc.setQueryData(tasksKeys.detail(vars.id), {
            ...detailSnapshot,
            derivedState: nextState,
          });
        }
      }
      return { listSnapshots, detailSnapshot };
    },
    onError: (_err, vars, ctx) => {
      const snap = ctx as
        | {
            listSnapshots: [readonly unknown[], TaskListItem[] | undefined][];
            detailSnapshot: Task | undefined;
          }
        | undefined;
      if (!snap) return;
      for (const [key, value] of snap.listSnapshots) {
        qc.setQueryData(key, value);
      }
      if (snap.detailSnapshot) {
        qc.setQueryData(tasksKeys.detail(vars.id), snap.detailSnapshot);
      }
    },
    onSuccess: (task) => {
      qc.setQueryData(tasksKeys.detail(task.id), task);
    },
    onSettled: (_data, _err, vars) => {
      void qc.invalidateQueries({ queryKey: tasksKeys.detail(vars.id) });
      void qc.invalidateQueries({ queryKey: [...timelineKeys.all, 'task', vars.id] });
      if (vars.projectId) {
        void qc.invalidateQueries({
          queryKey: [...tasksKeys.all, 'list', vars.projectId],
        });
      } else {
        void qc.invalidateQueries({ queryKey: [...tasksKeys.all, 'list'] });
      }
    },
  });
}

export function useTaskDependenciesQuery(
  taskId: string,
): UseSuspenseQueryResult<TaskDependenciesResult> {
  return useSuspenseQuery({
    queryKey: tasksKeys.dependencies(taskId),
    queryFn: async (): Promise<TaskDependenciesResult> => {
      const { data, error } = await sdk.GET('/tasks/{id}/dependencies', {
        params: { path: { id: taskId } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load task dependencies');
      return {
        outgoing: data.outgoing ?? [],
        incoming: data.incoming ?? [],
      };
    },
  });
}

export interface AddDependencyArgs {
  taskId: string;
  toTaskId: string;
  kind: TaskDependencyKind;
}

export function useAddTaskDependency(): UseMutationResult<void, ApiError, AddDependencyArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, toTaskId, kind }: AddDependencyArgs): Promise<void> => {
      const { error } = await sdk.POST('/tasks/{id}/dependencies', {
        params: { path: { id: taskId } },
        body: { toTaskId, kind },
      });
      if (error) throw toApiError(error, 'Failed to add task dependency');
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: tasksKeys.dependencies(vars.taskId) });
      void qc.invalidateQueries({ queryKey: tasksKeys.detail(vars.taskId) });
      void qc.invalidateQueries({ queryKey: [...timelineKeys.all, 'task', vars.taskId] });
    },
  });
}

export interface RemoveDependencyArgs {
  taskId: string;
  depId: string;
}

export function useRemoveTaskDependency(): UseMutationResult<void, ApiError, RemoveDependencyArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, depId }: RemoveDependencyArgs): Promise<void> => {
      const { error } = await sdk.DELETE('/tasks/{id}/dependencies/{depId}', {
        params: { path: { id: taskId, depId } },
      });
      if (error) throw toApiError(error, 'Failed to remove task dependency');
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: tasksKeys.dependencies(vars.taskId) });
      void qc.invalidateQueries({ queryKey: tasksKeys.detail(vars.taskId) });
      void qc.invalidateQueries({ queryKey: [...timelineKeys.all, 'task', vars.taskId] });
    },
  });
}

/* ── Reorder mutation ────────────────────────────────────────── */

export interface ReorderItem {
  id: string;
  sortWeight: number;
}

export interface ReorderTasksArgs {
  projectId: string;
  items: ReorderItem[];
}

/**
 * useReorderTasks — POST /tasks/reorder.
 *
 * Sends a batch of (taskId, sortWeight) pairs to persist list order.
 * Performs optimistic reorder on the `tasks.list` cache so the UI is
 * snappy, rolling back on failure.
 */
export function useReorderTasks(): UseMutationResult<void, ApiError, ReorderTasksArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ projectId, items }: ReorderTasksArgs): Promise<void> => {
      const { error } = await sdk.POST('/tasks/reorder', {
        body: { projectId, items },
      });
      if (error) throw toApiError(error, 'Failed to reorder tasks');
    },
    onMutate: async (vars) => {
      await qc.cancelQueries({ queryKey: [...tasksKeys.all, 'list', vars.projectId] });
      const snapshots = qc.getQueriesData<TaskListItem[]>({
        queryKey: [...tasksKeys.all, 'list', vars.projectId],
      });
      // Optimistic: apply sort weights to cached lists
      const weightMap = new Map(vars.items.map((i) => [i.id, i.sortWeight]));
      for (const [key, value] of snapshots) {
        if (!value) continue;
        const updated = value
          .map((task) => {
            const w = weightMap.get(task.id);
            return w != null ? { ...task, sortWeight: w } : task;
          })
          .sort((a, b) => a.sortWeight - b.sortWeight);
        qc.setQueryData(key, updated);
      }
      return { snapshots };
    },
    onError: (_err, _vars, ctx) => {
      const snap = ctx as
        | { snapshots: [readonly unknown[], TaskListItem[] | undefined][] }
        | undefined;
      if (!snap) return;
      for (const [key, value] of snap.snapshots) {
        qc.setQueryData(key, value);
      }
    },
    onSettled: (_data, _err, vars) => {
      void qc.invalidateQueries({ queryKey: [...tasksKeys.all, 'list', vars.projectId] });
    },
  });
}

export function useTaskSearch(
  workspaceId: string,
  query: string,
  enabled: boolean,
): UseQueryResult<TaskListItem[]> {
  return useQuery({
    queryKey: [...tasksKeys.all, 'search', workspaceId, query],
    enabled: enabled && workspaceId.length > 0 && query.trim().length > 0,
    staleTime: 10_000,
    queryFn: async (): Promise<TaskListItem[]> => {
      const { data, error } = await sdk.GET('/tasks', {
        params: { query: { workspaceId, q: query.trim(), limit: 20, offset: 0 } },
      });
      if (error || !data) return [];
      return data.tasks ?? [];
    },
  });
}

/* ── Attachment hooks ───────────────────────────────────────── */

/** Fetches all attachments for a task. */
export function useListAttachments(taskPublicId: string): UseSuspenseQueryResult<TaskAttachment[]> {
  return useSuspenseQuery({
    queryKey: tasksKeys.attachments(taskPublicId),
    queryFn: async (): Promise<TaskAttachment[]> => {
      const { data, error } = await sdk.GET('/tasks/{id}/attachments', {
        params: { path: { id: taskPublicId } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load attachments');
      return data.attachments ?? [];
    },
  });
}

export interface AddAttachmentArgs {
  taskId: string;
  input: AddAttachmentInput;
}

/** Registers attachment metadata on a task. */
export function useAddAttachment(): UseMutationResult<TaskAttachment, ApiError, AddAttachmentArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, input }: AddAttachmentArgs): Promise<TaskAttachment> => {
      const { data, error } = await sdk.POST('/tasks/{id}/attachments', {
        params: { path: { id: taskId } },
        body: input,
      });
      if (error || !data) throw toApiError(error, 'Failed to add attachment');
      return data;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: tasksKeys.attachments(vars.taskId) });
    },
  });
}

export interface PresignUploadArgs {
  taskId: string;
  file: File;
}

/** Requests a presigned PUT URL, uploads the file, and returns the result. */
export function usePresignUpload(): UseMutationResult<
  PresignUploadResult,
  ApiError,
  PresignUploadArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, file }: PresignUploadArgs): Promise<PresignUploadResult> => {
      const { data, error } = await sdk.POST('/tasks/{id}/attachments/presign', {
        params: { path: { id: taskId } },
        body: {
          filename: file.name,
          contentType: file.type || 'application/octet-stream',
          byteSize: file.size,
        },
      });
      if (error || !data) throw toApiError(error, 'Failed to presign upload');

      const res = await fetch(data.uploadUrl, {
        method: 'PUT',
        body: file,
        headers: { 'Content-Type': file.type || 'application/octet-stream' },
      });
      if (!res.ok) throw new ApiError('UPLOAD_FAILED', 'File upload failed');

      return data;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: tasksKeys.attachments(vars.taskId) });
    },
  });
}

export interface DownloadAttachmentArgs {
  taskId: string;
  attachmentId: string;
}

/** Gets a presigned download URL for an attachment. */
export async function fetchDownloadUrl(taskId: string, attachmentId: string): Promise<string> {
  const { data, error } = await sdk.GET('/tasks/{id}/attachments/{aid}/download', {
    params: { path: { id: taskId, aid: attachmentId } },
  });
  if (error || !data) throw toApiError(error, 'Failed to get download URL');
  return data.downloadUrl;
}

export interface DeleteAttachmentArgs {
  taskId: string;
  attachmentId: string;
}

/** Soft-deletes an attachment from a task. */
export function useDeleteAttachment(): UseMutationResult<void, ApiError, DeleteAttachmentArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, attachmentId }: DeleteAttachmentArgs): Promise<void> => {
      const { error } = await sdk.DELETE('/tasks/{id}/attachments/{aid}', {
        params: { path: { id: taskId, aid: attachmentId } },
      });
      if (error) throw toApiError(error, 'Failed to delete attachment');
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: tasksKeys.attachments(vars.taskId) });
    },
  });
}
