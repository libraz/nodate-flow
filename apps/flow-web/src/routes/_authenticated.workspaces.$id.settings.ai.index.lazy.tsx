/**
 * /workspaces/$id/settings/ai/ — sends the caller to the first tab they can
 * use: Connection for owner / admin, Agents for everyone else (an unknown
 * role included). Waits for the role lookup before choosing.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute, getRouteApi, Navigate } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import { isWorkspaceAdmin, useCurrentWorkspaceRoleState } from '../lib/use-current-workspace-role';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/ai/');

function AiSettingsIndex(): ReactElement {
  const { id } = routeApi.useParams();
  const { role, isPending } = useCurrentWorkspaceRoleState();
  if (isPending) return <Skeleton aria-hidden="true" style={{ blockSize: 'var(--nf-space-16)' }} />;
  return isWorkspaceAdmin(role) ? (
    <Navigate to="/workspaces/$id/settings/ai/connection" params={{ id }} replace />
  ) : (
    <Navigate to="/workspaces/$id/settings/ai/agents" params={{ id }} replace />
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/ai/')({
  component: AiSettingsIndex,
});
