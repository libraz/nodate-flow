/**
 * /workspaces/$id/settings/audit-log — workspace audit log viewer (lazy).
 *
 * Lists recent audit entries with search, filtering, pagination, and
 * CSV export. Read-only.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { ErrorBoundary, type FallbackProps } from 'react-error-boundary';

import AuditLogView from '../features/audit/audit-log-view';
import AccessRestricted from '../features/workspaces/access-restricted';
import { ApiError } from '../lib/api-error';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/audit-log');

/**
 * Renders <AccessRestricted> for 401 / 403, rethrows everything else so
 * the root FatalFallback keeps its normal behaviour on unexpected errors.
 */
function SettingsErrorFallback({ error }: FallbackProps): ReactElement {
  if (error instanceof ApiError && (error.httpStatus === 401 || error.httpStatus === 403)) {
    return <AccessRestricted sectionTitleKey="nav.audit_log" />;
  }
  throw error;
}

function AuditLogRoute(): ReactElement {
  const { id } = routeApi.useParams();
  return (
    <section
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: '1.5rem',
        inlineSize: '100%',
      }}
    >
      <ErrorBoundary FallbackComponent={SettingsErrorFallback}>
        <Suspense
          fallback={
            <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
              <Skeleton style={{ blockSize: '2rem', inlineSize: '16rem' }} />
              <Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />
              <Skeleton style={{ blockSize: '12rem', inlineSize: '100%' }} />
            </div>
          }
        >
          <AuditLogView workspaceId={id} />
        </Suspense>
      </ErrorBoundary>
    </section>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/audit-log')({
  component: AuditLogRoute,
});
