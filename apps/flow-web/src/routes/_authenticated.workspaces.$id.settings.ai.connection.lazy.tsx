/**
 * /workspaces/$id/settings/ai/connection — AI provider management tab (lazy).
 *
 * Admin-only. Once the role is known to be below admin the tab shows the
 * restricted panel without issuing the provider requests; an unknown role
 * (lookup failed) falls through to the list, which handles a 403 inline.
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { ErrorBoundary, type FallbackProps } from 'react-error-boundary';

import ProviderList from '../features/ai-providers/provider-list';
import SettingsListSkeleton from '../features/settings/settings-list-skeleton';
import AccessRestricted from '../features/workspaces/access-restricted';
import { ApiError } from '../lib/api-error';
import { isWorkspaceAdmin, useCurrentWorkspaceRoleState } from '../lib/use-current-workspace-role';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/ai/connection');

/**
 * Renders the tab variant of <AccessRestricted> for 401 / 403, rethrows
 * everything else so the root FatalFallback keeps its normal behaviour.
 */
function TabErrorFallback({ error }: FallbackProps): ReactElement {
  if (error instanceof ApiError && (error.httpStatus === 401 || error.httpStatus === 403)) {
    return <AccessRestricted sectionTitleKey="nav.ai" variant="tab" />;
  }
  throw error;
}

function AiConnectionRoute(): ReactElement {
  const { id } = routeApi.useParams();
  const { role, isPending } = useCurrentWorkspaceRoleState();
  if (isPending) return <SettingsListSkeleton />;
  if (role !== null && !isWorkspaceAdmin(role)) {
    return <AccessRestricted sectionTitleKey="nav.ai" variant="tab" />;
  }
  return (
    <ErrorBoundary FallbackComponent={TabErrorFallback}>
      <Suspense fallback={<SettingsListSkeleton />}>
        <ProviderList workspaceId={id} />
      </Suspense>
    </ErrorBoundary>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/ai/connection')({
  component: AiConnectionRoute,
});
