/**
 * Timeline feature — typed suspense queries for task / project / workspace
 * timelines, backed by the SDK.
 */

import type { components } from '@nodate-flow/sdk';
import { type UseSuspenseQueryResult, useSuspenseQuery } from '@tanstack/react-query';

import { sdk } from '../../lib/sdk';

export type TimelineEvent = components['schemas']['Event'];

/**
 * All known timeline event kinds. The UI maps these to i18n keys by
 * replacing `.` with `_` (e.g. `task.created` -> `event.task_created`).
 */
export const TIMELINE_KINDS = [
  'task.created',
  'task.updated',
  'task.disabled',
  'task.comment.added',
  'task.comment.edited',
  'task.comment.removed',
  'task.attachment.added',
  'task.attachment.removed',
  'task.actor.added',
  'task.actor.removed',
  'task.dependency.added',
  'task.dependency.removed',
  'task.constraint.added',
  'task.constraint.removed',
  'task.transition.start',
  'task.transition.block',
  'task.transition.unblock',
  'task.transition.submit',
  'task.transition.complete',
  'task.transition.reopen',
  'task.transition.cancel',
  'signal.attached',
  'comment.added',
] as const;

export type TimelineKind = (typeof TIMELINE_KINDS)[number];

export interface TimelineFilters {
  kind?: readonly string[];
  /**
   * Selected actor user public ids. The backend currently filters by a
   * single actor; the first id wins. The array shape is kept so the UI
   * can offer a multi-select picker without an API churn later.
   */
  actor?: readonly string[];
  limit?: number;
  offset?: number;
}

export type TimelineScope = 'task' | 'project' | 'workspace';

/** Query key factory for the timeline feature. */
export const timelineKeys = {
  all: ['timeline'] as const,
  scoped: (scope: TimelineScope, id: string, filters?: TimelineFilters) =>
    [...timelineKeys.all, scope, id, filters ?? {}] as const,
};

import { ApiError, toApiError } from '../../lib/api-error';

export { ApiError as TimelineApiError };

interface TimelineQuery {
  limit?: number;
  offset?: number;
  kind?: string[];
  actor?: string;
}

function buildQuery(filters?: TimelineFilters): TimelineQuery {
  const query: TimelineQuery = {};
  if (filters?.limit !== undefined) query.limit = filters.limit;
  if (filters?.offset !== undefined) query.offset = filters.offset;
  if (filters?.kind && filters.kind.length > 0) query.kind = [...filters.kind];
  if (filters?.actor && filters.actor.length > 0) {
    const first = filters.actor[0];
    if (first !== undefined) query.actor = first;
  }
  return query;
}

export interface TimelinePage {
  events: TimelineEvent[];
  total: number;
  limit: number;
  offset: number;
}

function normalize(data: {
  events?: TimelineEvent[] | null;
  total?: number;
  limit?: number;
  offset?: number;
}): TimelinePage {
  return {
    events: data.events ?? [],
    total: data.total ?? 0,
    limit: data.limit ?? 0,
    offset: data.offset ?? 0,
  };
}

export function useTaskTimelineQuery(
  taskId: string,
  filters?: TimelineFilters,
): UseSuspenseQueryResult<TimelinePage> {
  return useSuspenseQuery({
    queryKey: timelineKeys.scoped('task', taskId, filters),
    queryFn: async (): Promise<TimelinePage> => {
      const { data, error } = await sdk.GET('/tasks/{id}/timeline', {
        params: { path: { id: taskId }, query: buildQuery(filters) },
      });
      if (error || !data) throw toApiError(error, 'Failed to load task timeline');
      return normalize(data);
    },
  });
}

export function useProjectTimelineQuery(
  projectId: string,
  filters?: TimelineFilters,
): UseSuspenseQueryResult<TimelinePage> {
  return useSuspenseQuery({
    queryKey: timelineKeys.scoped('project', projectId, filters),
    queryFn: async (): Promise<TimelinePage> => {
      const { data, error } = await sdk.GET('/projects/{prjId}/timeline', {
        params: { path: { prjId: projectId }, query: buildQuery(filters) },
      });
      if (error || !data) throw toApiError(error, 'Failed to load project timeline');
      return normalize(data);
    },
  });
}

export function useWorkspaceTimelineQuery(
  workspaceId: string,
  filters?: TimelineFilters,
): UseSuspenseQueryResult<TimelinePage> {
  return useSuspenseQuery({
    queryKey: timelineKeys.scoped('workspace', workspaceId, filters),
    queryFn: async (): Promise<TimelinePage> => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}/timeline', {
        params: { path: { wsId: workspaceId }, query: buildQuery(filters) },
      });
      if (error || !data) throw toApiError(error, 'Failed to load workspace timeline');
      return normalize(data);
    },
  });
}
