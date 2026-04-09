/**
 * useCurrentWorkspaceId — derives the active workspace id from the
 * current URL. Handles both `/workspaces/{id}/...` (direct) and
 * `/projects/{pid}/...` (indirect, resolved via a non-suspense project
 * fetch). Returns `null` outside workspace- or project-scoped routes,
 * or while the project→workspace lookup is still in flight.
 *
 * Shared by the sidebar (project tree) and the top-bar workspace
 * switcher so both UIs stay in sync with the route.
 */

import { useQuery } from '@tanstack/react-query';
import { useRouterState } from '@tanstack/react-router';
import { useMemo } from 'react';

import { projectsKeys } from '../features/projects/api';
import { sdk } from './sdk';

export function useCurrentWorkspaceId(): string | null {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const wsMatch = useMemo(() => /^\/workspaces\/([^/]+)(?:\/|$)/.exec(pathname), [pathname]);
  const projectMatch = useMemo(() => /^\/projects\/([^/]+)(?:\/|$)/.exec(pathname), [pathname]);
  const taskMatch = useMemo(() => /^\/tasks\/([^/]+)(?:\/|$)/.exec(pathname), [pathname]);
  const projectId = projectMatch ? (projectMatch[1] ?? null) : null;
  const taskId = taskMatch ? (taskMatch[1] ?? null) : null;

  const projectQuery = useQuery({
    queryKey: projectId ? projectsKeys.detail(projectId) : ['projects', 'detail', 'none'],
    enabled: projectId !== null,
    staleTime: 60_000,
    queryFn: async (): Promise<{ workspaceId: string } | null> => {
      if (!projectId) return null;
      const { data, error } = await sdk.GET('/projects/{prjId}', {
        params: { path: { prjId: projectId } },
      });
      if (error || !data) return null;
      return { workspaceId: data.workspaceId };
    },
  });

  const taskQuery = useQuery({
    queryKey: taskId
      ? (['tasks', 'detail', taskId] as const)
      : (['tasks', 'detail', 'none'] as const),
    enabled: taskId !== null,
    staleTime: 60_000,
    queryFn: async (): Promise<{ workspaceId: string } | null> => {
      if (!taskId) return null;
      const { data, error } = await sdk.GET('/tasks/{id}', {
        params: { path: { id: taskId } },
      });
      if (error || !data) return null;
      return { workspaceId: data.workspaceId };
    },
  });

  if (wsMatch) return wsMatch[1] ?? null;
  if (projectQuery.data) return projectQuery.data.workspaceId;
  if (taskQuery.data) return taskQuery.data.workspaceId;
  return null;
}
