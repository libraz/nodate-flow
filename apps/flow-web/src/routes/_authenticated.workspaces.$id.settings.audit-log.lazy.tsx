/**
 * /workspaces/$id/settings/audit-log — workspace audit log viewer (lazy).
 *
 * Lists recent audit entries with search, filtering, pagination, and
 * CSV export. Read-only.
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { ErrorBoundary, type FallbackProps } from 'react-error-boundary';

import AuditLogView from '../features/audit/audit-log-view';
import SettingsListSkeleton from '../features/settings/settings-list-skeleton';
import AccessRestricted from '../features/workspaces/access-restricted';
import { ApiError } from '../lib/api-error';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/audit-log');

/**
 * Routes 401 / 403 to <AccessRestricted>. Other errors rethrow so the
 * root FatalFallback can handle them.
 */
function SettingsErrorFallback({ error }: FallbackProps): ReactElement {
  if (error instanceof ApiError) {
    if (error.httpStatus === 401 || error.httpStatus === 403) {
      return <AccessRestricted sectionTitleKey="nav.audit_log" />;
    }
  }
  throw error;
}

function AuditLogRoute(): ReactElement {
  const { id } = routeApi.useParams();
  return (
    <ErrorBoundary FallbackComponent={SettingsErrorFallback}>
      <Suspense fallback={<SettingsListSkeleton />}>
        <AuditLogView workspaceId={id} />
      </Suspense>
    </ErrorBoundary>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/audit-log')({
  component: AuditLogRoute,
});
