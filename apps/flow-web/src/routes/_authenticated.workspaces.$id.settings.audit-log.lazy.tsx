/**
 * /workspaces/$id/settings/audit-log — workspace audit log viewer (lazy).
 *
 * Lists recent audit entries with search, filtering, pagination, and
 * CSV export. Read-only.
 *
 * The backing handler `GET /workspaces/{wsId}/audit-logs` is not yet
 * registered in the OpenAPI spec, so the API will respond non-2xx until
 * flow-api ships it. While the endpoint is missing the route renders a
 * "Coming soon" empty state instead of letting the 404 / 5xx bubble up
 * to the root error boundary. The route stays accessible by direct URL
 * so developers can preview the layout.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { ErrorBoundary, type FallbackProps } from 'react-error-boundary';

import AuditLogComingSoon from '../features/audit/audit-log-coming-soon';
import AuditLogView from '../features/audit/audit-log-view';
import AccessRestricted from '../features/workspaces/access-restricted';
import { ApiError } from '../lib/api-error';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/audit-log');

/**
 * Routes 401 / 403 to <AccessRestricted>, treats other API errors
 * (missing endpoint, 5xx, network) as "feature not yet shipped" and
 * renders the coming-soon panel. Anything not recognised as an API
 * error rethrows so the root FatalFallback can handle it.
 */
function SettingsErrorFallback({ error }: FallbackProps): ReactElement {
  if (error instanceof ApiError) {
    if (error.httpStatus === 401 || error.httpStatus === 403) {
      return <AccessRestricted sectionTitleKey="nav.audit_log" />;
    }
    return <AuditLogComingSoon />;
  }
  // The audit-logs query path uses an untyped fetch that throws a plain
  // Error when the endpoint is unavailable; surface that as the same
  // coming-soon state rather than a fatal page.
  if (error instanceof Error) {
    return <AuditLogComingSoon />;
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
