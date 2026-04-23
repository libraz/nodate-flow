/**
 * /projects/$projectId — legacy redirect shim.
 *
 * The canonical project URL is now
 * `/workspaces/$id/projects/$projectId[/tasks|/gantt|/timeline]`.
 * This layout resolves the project's workspace and issues a permanent
 * redirect to the canonical path, preserving any trailing segment so
 * `/projects/X/tasks` maps to `/workspaces/W/projects/X/tasks` etc.
 *
 * Kept around for UX during the transition and to protect bookmarks,
 * shared links, email deep-links, and any external reference.
 * Children (`.tasks.tsx`, `.gantt.tsx`, `.timeline.tsx`, …) exist as
 * empty stubs so the route tree still matches child URLs; the parent's
 * `beforeLoad` redirects before any child component renders.
 */

import { createFileRoute, notFound, redirect } from '@tanstack/react-router';

import { sdk } from '../lib/sdk';

type CanonicalTo =
  | '/workspaces/$id/projects/$projectId'
  | '/workspaces/$id/projects/$projectId/tasks'
  | '/workspaces/$id/projects/$projectId/gantt'
  | '/workspaces/$id/projects/$projectId/timeline';

/**
 * Map the legacy URL suffix to the typed canonical route. Normalizes
 * a trailing slash on `/tasks/` to `/tasks` so the TanStack Router
 * route table recognises it.
 */
function canonicalToFor(suffix: string): CanonicalTo {
  const s = suffix.replace(/\/$/, '');
  if (s === '/tasks') return '/workspaces/$id/projects/$projectId/tasks';
  if (s === '/gantt') return '/workspaces/$id/projects/$projectId/gantt';
  if (s === '/timeline') return '/workspaces/$id/projects/$projectId/timeline';
  return '/workspaces/$id/projects/$projectId';
}

export const Route = createFileRoute('/_authenticated/projects/$projectId')({
  beforeLoad: async ({ params, location }) => {
    const { data, response } = await sdk.GET('/projects/{prjId}', {
      params: { path: { prjId: params.projectId } },
    });
    if (response.status === 404 || !data) throw notFound();
    const wsId = data.workspaceId;
    // Preserve any suffix after `/projects/$projectId` (e.g. `/tasks`,
    // `/gantt`, `/timeline`) so deep links map 1:1. Search params are
    // carried through by the router automatically.
    const prefix = `/projects/${params.projectId}`;
    const suffix = location.pathname.startsWith(prefix)
      ? location.pathname.slice(prefix.length)
      : '';
    throw redirect({
      to: canonicalToFor(suffix),
      params: { id: wsId, projectId: params.projectId },
      replace: true,
    });
  },
});
