/**
 * /workspaces/$id/settings/auto-actions — auto-action executor settings (lazy).
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { ErrorBoundary, type FallbackProps } from 'react-error-boundary';

import SettingsListSkeleton from '../features/settings/settings-list-skeleton';
import AccessRestricted from '../features/workspaces/access-restricted';
import AutoActionSettingsPage from '../features/workspaces/auto-action-settings';
import { ApiError } from '../lib/api-error';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/auto-actions');

/**
 * Renders <AccessRestricted> for 401 / 403, rethrows everything else so
 * the root FatalFallback keeps its normal behaviour on unexpected errors.
 */
function SettingsErrorFallback({ error }: FallbackProps): ReactElement {
  if (error instanceof ApiError && (error.httpStatus === 401 || error.httpStatus === 403)) {
    return <AccessRestricted sectionTitleKey="nav.auto_actions" />;
  }
  throw error;
}

function AutoActionsRoute(): ReactElement {
  const { id } = routeApi.useParams();
  return (
    <ErrorBoundary FallbackComponent={SettingsErrorFallback}>
      <Suspense fallback={<SettingsListSkeleton />}>
        <AutoActionSettingsPage workspaceId={id} />
      </Suspense>
    </ErrorBoundary>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/auto-actions')({
  component: AutoActionsRoute,
});
