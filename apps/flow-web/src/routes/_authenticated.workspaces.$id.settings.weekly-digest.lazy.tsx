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
import { ErrorBoundary, type FallbackProps } from 'react-error-boundary';

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
    <section
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-6)',
        inlineSize: '100%',
      }}
    >
      <ErrorBoundary FallbackComponent={SettingsErrorFallback}>
        <Suspense
          fallback={
            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-3)' }}>
              {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
              <Skeleton style={{ blockSize: '2rem', inlineSize: '16rem' }} />
              {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
              <Skeleton style={{ blockSize: '6rem', inlineSize: '100%' }} />
              {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
              <Skeleton style={{ blockSize: '10rem', inlineSize: '100%' }} />
            </div>
          }
        >
          <DigestView workspaceId={id} />
        </Suspense>
      </ErrorBoundary>
    </section>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/weekly-digest')({
  component: WeeklyDigestRoute,
});
