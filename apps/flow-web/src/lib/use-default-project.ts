/**
 * useDefaultProjectId — resolves a "default project for task creation".
 *
 * The glass-dock FAB buttons (`new_task`, `quick_capture`) need a
 * project context that is not constrained to the current URL — the
 * user should be able to capture a task from `/today`, `/inbox`, or
 * the home page without first navigating to a project.
 *
 * Resolution order:
 *   1. Project from the current URL — `/projects/{id}` or
 *      `/workspaces/{wsId}/projects/{projectId}/...`.
 *   2. First project in the currently-active workspace (as exposed by
 *      {@link useActiveWorkspaceId}).
 *   3. `null` when the user has no reachable project — callers fall
 *      back to opening the command palette so the user sees the
 *      "create project first" UX.
 *
 * The hook uses the existing projects list endpoint
 * (`GET /workspaces/{wsId}/projects`) via a non-suspense `useQuery`
 * so it can live at the authenticated-layout level without gating
 * the whole shell on a network round-trip.
 */

import { useQuery } from '@tanstack/react-query';
import { useRouterState } from '@tanstack/react-router';
import type { Project } from '../features/projects/api';
import { projectsKeys } from '../features/projects/api';
import { apiRequest } from './api';
import { useActiveWorkspaceId } from './use-current-workspace';

export interface DefaultProjectResolution {
  /** Resolved project id, or null when none is reachable. */
  projectId: string | null;
  /**
   * Workspace id associated with the resolved project. Null when we
   * have neither a URL nor a cached project list to derive it from.
   */
  workspaceId: string | null;
}

/**
 * Resolve the best-effort default project id for FAB-triggered task
 * creation flows. See the file-level doc comment for the precedence
 * rules.
 */
export function useDefaultProjectId(): DefaultProjectResolution {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const wsProjectMatch = /^\/workspaces\/([^/]+)\/projects\/([^/]+)/.exec(pathname);
  const projectMatch = /^\/projects\/([^/]+)/.exec(pathname);

  const urlWorkspaceId = wsProjectMatch?.[1] ?? null;
  const urlProjectId = wsProjectMatch?.[2] ?? projectMatch?.[1] ?? null;

  const activeWorkspaceId = useActiveWorkspaceId();

  // Only fetch the projects list when we need to fall back (no URL
  // project) and we have a workspace context. The query key matches
  // `projectsKeys.list(wsId)` so the sidebar's suspense-variant query
  // and this non-suspense variant share the same cache entry.
  const fallbackWsId = urlProjectId ? null : activeWorkspaceId;
  const projectsQuery = useQuery({
    queryKey: fallbackWsId ? projectsKeys.list(fallbackWsId) : ['projects', 'list', 'none'],
    enabled: fallbackWsId !== null,
    staleTime: 60_000,
    queryFn: async (): Promise<Project[]> => {
      if (!fallbackWsId) return [];
      // This only picks a default destination; with no list the caller
      // falls back to "no project", which is a valid state.
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/projects', {
            params: { path: { wsId: fallbackWsId } },
          }),
        'Failed to load projects',
        { onError: 'empty', empty: null },
      );
      return data?.projects ?? [];
    },
  });

  if (urlProjectId) {
    return { projectId: urlProjectId, workspaceId: urlWorkspaceId };
  }

  const first = projectsQuery.data?.[0];
  if (first && fallbackWsId) {
    return { projectId: first.id, workspaceId: fallbackWsId };
  }

  return { projectId: null, workspaceId: activeWorkspaceId };
}
