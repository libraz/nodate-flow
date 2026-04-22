/**
 * /workspaces/$id/settings/public-shares — manage workspace-owned public
 * share pages (lazy).
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import ShareList from '../features/public-shares/share-list';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/public-shares');

function WorkspacePublicSharesRoute(): ReactElement {
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
      <ShareList workspaceId={id} />
    </Suspense>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/public-shares')({
  component: WorkspacePublicSharesRoute,
});
