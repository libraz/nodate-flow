/**
 * /workspaces/$id/settings/public-shares/$shareId — editor page for a single
 * public share (lazy). Attached events + add/remove.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import ShareDetail from '../features/public-shares/share-detail';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/public-shares/$shareId');

function ShareDetailRoute(): ReactElement {
  const { id, shareId } = routeApi.useParams();
  return (
    <Suspense
      fallback={
        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
          <Skeleton style={{ blockSize: '2rem', inlineSize: '16rem' }} />
          <Skeleton style={{ blockSize: '8rem', inlineSize: '100%' }} />
        </div>
      }
    >
      <ShareDetail workspaceId={id} shareId={shareId} />
    </Suspense>
  );
}

export const Route = createLazyFileRoute(
  '/_authenticated/workspaces/$id/settings/public-shares/$shareId',
)({
  component: ShareDetailRoute,
});
