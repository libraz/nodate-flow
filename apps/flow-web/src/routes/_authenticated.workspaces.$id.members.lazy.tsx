/**
 * /workspaces/$id/members — workspace member list (lazy).
 *
 * The layout route already renders the workspace h1 and tab row; the table
 * carries its own section heading and help.
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import WorkspaceMembersTable from '../features/workspaces/workspace-members-table';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/members');

function WorkspaceMembersRoute(): ReactElement {
  const { id } = routeApi.useParams();
  return <WorkspaceMembersTable workspaceId={id} />;
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/members')({
  component: WorkspaceMembersRoute,
});
