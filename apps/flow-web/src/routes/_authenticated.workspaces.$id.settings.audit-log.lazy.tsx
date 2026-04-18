/**
 * /workspaces/$id/settings/audit-log — workspace audit log viewer (lazy).
 *
 * Lists recent audit entries with search, filtering, pagination, and
 * CSV export. Read-only.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import AuditLogView from '../features/audit/audit-log-view';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/audit-log');

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
    </section>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/audit-log')({
  component: AuditLogRoute,
});
