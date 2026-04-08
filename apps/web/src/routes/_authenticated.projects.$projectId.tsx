/**
 * /projects/$projectId — project layout. Renders nested child routes
 * (e.g. `/tasks`, `/timeline`) via <Outlet />. The detail view itself
 * lives in the sibling `_authenticated.projects.$projectId.index.tsx`.
 *
 * The loader probes the project so deep-link 404s land on the branded
 * NotFound rendered inside the authenticated AppShell instead of
 * crashing the route into the root ErrorBoundary.
 */

import { Outlet, createFileRoute, notFound } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import { sdk } from '../lib/sdk';

function ProjectLayout(): ReactElement {
  return <Outlet />;
}

export const Route = createFileRoute('/_authenticated/projects/$projectId')({
  component: ProjectLayout,
  loader: async ({ params }) => {
    const { response } = await sdk.GET('/projects/{prjId}', {
      params: { path: { prjId: params.projectId } },
    });
    if (response.status === 404) throw notFound();
    return null;
  },
});
