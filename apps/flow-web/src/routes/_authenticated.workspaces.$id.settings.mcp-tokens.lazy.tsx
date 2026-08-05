/**
 * /workspaces/$id/settings/mcp-tokens — manage MCP personal access tokens (lazy).
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { ErrorBoundary, type FallbackProps } from 'react-error-boundary';

import TokenList from '../features/mcp-tokens/token-list';
import AccessRestricted from '../features/workspaces/access-restricted';
import { ApiError } from '../lib/api-error';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/mcp-tokens');

/**
 * Renders <AccessRestricted> for 401 / 403, rethrows everything else so
 * the root FatalFallback keeps its normal behaviour on unexpected errors.
 */
function SettingsErrorFallback({ error }: FallbackProps): ReactElement {
  if (error instanceof ApiError && (error.httpStatus === 401 || error.httpStatus === 403)) {
    return <AccessRestricted sectionTitleKey="nav.mcp_tokens" />;
  }
  throw error;
}

function WorkspaceMcpTokensRoute(): ReactElement {
  const { id } = routeApi.useParams();
  return (
    <ErrorBoundary FallbackComponent={SettingsErrorFallback}>
      <Suspense
        fallback={
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-3)' }}>
            <Skeleton style={{ blockSize: '2rem', inlineSize: '16rem' }} />
            <Skeleton style={{ blockSize: '8rem', inlineSize: '100%' }} />
          </div>
        }
      >
        <TokenList workspaceId={id} />
      </Suspense>
    </ErrorBoundary>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/mcp-tokens')({
  component: WorkspaceMcpTokensRoute,
});
