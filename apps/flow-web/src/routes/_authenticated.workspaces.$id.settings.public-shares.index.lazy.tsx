/**
 * /workspaces/$id/settings/public-shares/ — manage workspace-owned public
 * share pages (lazy index). Renders the share list; detail editor mounts at
 * the sibling `$shareId` child.
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import ShareList from '../features/public-shares/share-list';
import SettingsListSkeleton from '../features/settings/settings-list-skeleton';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/public-shares/');

function WorkspacePublicSharesIndex(): ReactElement {
  const { id } = routeApi.useParams();
  return (
    <Suspense fallback={<SettingsListSkeleton />}>
      <ShareList workspaceId={id} />
    </Suspense>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/public-shares/')({
  component: WorkspacePublicSharesIndex,
});
