/**
 * /workspaces/$id/settings/weekly-digest — workspace weekly digest (lazy).
 *
 * Renders the deterministic weekly digest (2.AI-9): state counts,
 * completed-this-week / overdue-open task lists, and a pre-rendered
 * markdown body. Read-only.
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { ErrorBoundary, type FallbackProps } from 'react-error-boundary';
import SettingsListSkeleton from '../features/settings/settings-list-skeleton';
import DigestView from '../features/weekly-digest/digest-view';
import AccessRestricted from '../features/workspaces/access-restricted';
import { ApiError } from '../lib/api-error';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/weekly-digest');

/**
 * Renders <AccessRestricted> for 401 / 403, rethrows everything else so
 * the root FatalFallback keeps its normal behaviour on unexpected errors.
 */
function SettingsErrorFallback({ error }: FallbackProps): ReactElement {
  if (error instanceof ApiError && (error.httpStatus === 401 || error.httpStatus === 403)) {
    return <AccessRestricted sectionTitleKey="nav.weekly_digest" />;
  }
  throw error;
}

function WeeklyDigestRoute(): ReactElement {
  const { id } = routeApi.useParams();
  return (
    <ErrorBoundary FallbackComponent={SettingsErrorFallback}>
      <Suspense fallback={<SettingsListSkeleton />}>
        <DigestView workspaceId={id} />
      </Suspense>
    </ErrorBoundary>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/weekly-digest')({
  component: WeeklyDigestRoute,
});
