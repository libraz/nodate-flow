/**
 * useTopBarBreadcrumb — derive the top-bar breadcrumb state from the
 * current TanStack Router match chain.
 *
 * Returns `null` on routes where the top-bar should render no
 * breadcrumb:
 *   - task detail (`/tasks/$taskId/...`) — the task detail panel has
 *     its own in-page breadcrumb, so the top-bar must not duplicate it.
 *   - any route where no workspace id can be resolved (truly unscoped
 *     — e.g. `/login` should never hit this hook, but guard anyway).
 *
 * Otherwise returns the ids needed to render the breadcrumb. The
 * consumer (`top-bar-breadcrumb.tsx`) then calls the suspense-backed
 * workspace / project queries to resolve display names. Keeping the
 * query calls in the renderer rather than this hook means the hook
 * obeys the Rules of Hooks unconditionally — the renderer component
 * mounts or unmounts as a whole when the breadcrumb shape changes.
 */

import { useMatches } from '@tanstack/react-router';

import { useCurrentWorkspaceId } from '../../lib/use-current-workspace';

/** One of the nested project views whose label becomes the tail crumb. */
export type ProjectView = 'overview' | 'tasks' | 'gantt' | 'timeline';

/** i18n key mapping for each project view crumb. */
export const PROJECT_VIEW_KEY: Readonly<Record<ProjectView, string>> = {
  overview: 'projects.nav.overview',
  tasks: 'projects.nav.tasks',
  gantt: 'projects.nav.gantt',
  timeline: 'projects.nav.timeline',
};

/** Return shape of {@link useTopBarBreadcrumb}. */
export interface TopBarBreadcrumbState {
  readonly workspaceId: string;
  readonly projectId: string | null;
  readonly view: ProjectView | null;
}

/**
 * Walk the match chain once and extract the relevant route params and
 * the active project-scoped view. The chain is ordered root → leaf, so
 * later matches override earlier ones for more-specific values.
 */
function readMatchChain(matches: ReturnType<typeof useMatches>): {
  workspaceId: string | null;
  projectId: string | null;
  view: ProjectView | null;
  onTaskRoute: boolean;
} {
  let workspaceId: string | null = null;
  let projectId: string | null = null;
  let view: ProjectView | null = null;
  let onTaskRoute = false;
  for (const m of matches) {
    const params = m.params as Record<string, string> | undefined;
    if (params) {
      if (typeof params.id === 'string' && params.id.length > 0) {
        workspaceId = params.id;
      }
      if (typeof params.projectId === 'string' && params.projectId.length > 0) {
        projectId = params.projectId;
      }
      if (typeof params.taskId === 'string' && params.taskId.length > 0) {
        onTaskRoute = true;
      }
    }
    const routeId = typeof m.routeId === 'string' ? m.routeId : '';
    // The project layout route — overview sits on this id itself.
    if (routeId.endsWith('/projects/$projectId')) {
      view = 'overview';
    } else if (routeId.endsWith('/projects/$projectId/tasks')) {
      view = 'tasks';
    } else if (routeId.endsWith('/projects/$projectId/gantt')) {
      view = 'gantt';
    } else if (routeId.endsWith('/projects/$projectId/timeline')) {
      view = 'timeline';
    }
  }
  return { workspaceId, projectId, view, onTaskRoute };
}

/**
 * Return the breadcrumb inputs the top-bar should use for the current
 * route, or `null` when nothing should be rendered.
 *
 * The hook calls the same (small, cached) hooks on every invocation so
 * the Rules of Hooks are obeyed regardless of the current route shape.
 */
export function useTopBarBreadcrumb(): TopBarBreadcrumbState | null {
  const matches = useMatches();
  const fallbackWsId = useCurrentWorkspaceId();

  const { workspaceId: routeWsId, projectId, view, onTaskRoute } = readMatchChain(matches);
  // Task detail owns its own breadcrumb; avoid duplication in the top-bar.
  if (onTaskRoute) return null;

  // Fall back to the persisted workspace id for cross-workspace pages
  // (/inbox, /today, /calendar, /pages, /settings) so users keep a
  // sense of place. If neither resolves, render nothing.
  const workspaceId = routeWsId ?? fallbackWsId;
  if (!workspaceId) return null;

  return { workspaceId, projectId, view };
}
