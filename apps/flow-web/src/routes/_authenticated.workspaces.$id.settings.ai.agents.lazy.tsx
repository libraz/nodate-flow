/**
 * /workspaces/$id/settings/ai/agents — workspace AI agents tab (lazy).
 * Thin wrapper; the page lives in `features/ai-providers/agents-page`.
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import AgentsPage from '../features/ai-providers/agents-page';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/ai/agents');

function AiAgentsRoute(): ReactElement {
  const { id } = routeApi.useParams();
  return <AgentsPage workspaceId={id} />;
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/ai/agents')({
  component: AiAgentsRoute,
});
