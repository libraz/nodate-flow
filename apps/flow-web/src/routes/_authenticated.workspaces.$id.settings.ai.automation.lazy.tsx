/**
 * /workspaces/$id/settings/ai/automation — auto-action rules and autonomy
 * matrix tab (lazy). Admin-only: a 401 / 403 swaps the tab for the
 * restricted panel.
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { ErrorBoundary, type FallbackProps } from 'react-error-boundary';

import SettingsListSkeleton from '../features/settings/settings-list-skeleton';
import AccessRestricted from '../features/workspaces/access-restricted';
import AutoActionSettingsPage from '../features/workspaces/auto-action-settings';
import { ApiError } from '../lib/api-error';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/ai/automation');

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

function AiAutomationRoute(): ReactElement {
  const { id } = routeApi.useParams();
  return (
    <ErrorBoundary FallbackComponent={TabErrorFallback}>
      <Suspense fallback={<SettingsListSkeleton />}>
        <AutoActionSettingsPage workspaceId={id} />
      </Suspense>
    </ErrorBoundary>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/ai/automation')({
  component: AiAutomationRoute,
});
