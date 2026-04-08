/**
 * /workspaces/$id/settings/weekly-digest — workspace weekly digest (lazy).
 *
 * Renders the deterministic weekly digest (2.AI-9): state counts,
 * completed-this-week / overdue-open task lists, and a pre-rendered
 * markdown body. Read-only.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import DigestView from '../features/weekly-digest/digest-view';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/weekly-digest');

function WeeklyDigestRoute(): ReactElement {
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
            <Skeleton style={{ blockSize: '6rem', inlineSize: '100%' }} />
            <Skeleton style={{ blockSize: '10rem', inlineSize: '100%' }} />
          </div>
        }
      >
        <DigestView workspaceId={id} />
      </Suspense>
    </section>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/weekly-digest')({
  component: WeeklyDigestRoute,
});
