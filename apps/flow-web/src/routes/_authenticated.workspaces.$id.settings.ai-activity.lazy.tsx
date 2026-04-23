/**
 * /workspaces/$id/settings/ai-activity — workspace AI invocation audit (lazy).
 *
 * Surfaces recent redacted LLM calls so workspace members can audit
 * what their AI providers have been asked and what came back
 * (2.WEB-2). Read-only.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { ErrorBoundary, type FallbackProps } from 'react-error-boundary';

import InvocationsList from '../features/ai-providers/invocations-list';
import AccessRestricted from '../features/workspaces/access-restricted';
import { ApiError } from '../lib/api-error';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/ai-activity');

/**
 * Renders <AccessRestricted> for 401 / 403, rethrows everything else so
 * the root FatalFallback keeps its normal behaviour on unexpected errors.
 */
function SettingsErrorFallback({ error }: FallbackProps): ReactElement {
  if (error instanceof ApiError && (error.httpStatus === 401 || error.httpStatus === 403)) {
    return <AccessRestricted sectionTitleKey="nav.ai_activity" />;
  }
  throw error;
}

function AiActivityRoute(): ReactElement {
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
              <Skeleton style={{ blockSize: '6rem', inlineSize: '100%' }} />
            </div>
          }
        >
          <InvocationsList workspaceId={id} />
        </Suspense>
      </ErrorBoundary>
    </section>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/ai-activity')({
  component: AiActivityRoute,
});
