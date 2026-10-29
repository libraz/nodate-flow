/**
 * /workspaces/$id/settings/public-shares/$shareId — editor page for a single
 * public share (lazy). Attached events + add/remove.
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import ShareDetail from '../features/public-shares/share-detail';
import SettingsListSkeleton from '../features/settings/settings-list-skeleton';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/public-shares/$shareId');

function ShareDetailRoute(): ReactElement {
  const { id, shareId } = routeApi.useParams();
  return (
    <Suspense fallback={<SettingsListSkeleton />}>
      <ShareDetail workspaceId={id} shareId={shareId} />
    </Suspense>
  );
}

export const Route = createLazyFileRoute(
  '/_authenticated/workspaces/$id/settings/public-shares/$shareId',
)({
  component: ShareDetailRoute,
});
