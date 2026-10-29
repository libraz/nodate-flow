/**
 * /workspaces/$id/settings/ai-activity — workspace AI invocation audit (lazy).
 *
 * Surfaces recent redacted LLM calls so workspace members can audit
 * what their AI providers have been asked and what came back.
 * Read-only.
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { ErrorBoundary, type FallbackProps } from 'react-error-boundary';

import InvocationsList from '../features/ai-providers/invocations-list';
import SettingsListSkeleton from '../features/settings/settings-list-skeleton';
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
    <ErrorBoundary FallbackComponent={SettingsErrorFallback}>
      <Suspense fallback={<SettingsListSkeleton />}>
        <InvocationsList workspaceId={id} />
      </Suspense>
    </ErrorBoundary>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/ai-activity')({
  component: AiActivityRoute,
});
