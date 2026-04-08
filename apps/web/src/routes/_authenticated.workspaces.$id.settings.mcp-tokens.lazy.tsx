/**
 * /workspaces/$id/settings/mcp-tokens — manage MCP personal access tokens (lazy).
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import TokenList from '../features/mcp-tokens/token-list';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/mcp-tokens');

function WorkspaceMcpTokensRoute(): ReactElement {
  const { id } = routeApi.useParams();
  return (
    <Suspense
      fallback={
        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
          <Skeleton style={{ blockSize: '2rem', inlineSize: '16rem' }} />
          <Skeleton style={{ blockSize: '8rem', inlineSize: '100%' }} />
        </div>
      }
    >
      <TokenList workspaceId={id} />
    </Suspense>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/mcp-tokens')({
  component: WorkspaceMcpTokensRoute,
});
