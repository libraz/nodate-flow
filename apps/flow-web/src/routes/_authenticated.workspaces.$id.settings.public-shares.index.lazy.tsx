/**
 * /workspaces/$id/settings/public-shares/ — manage workspace-owned public
 * share pages (lazy index). Renders the share list; detail editor mounts at
 * the sibling `$shareId` child.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import ShareList from '../features/public-shares/share-list';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/public-shares/');

function WorkspacePublicSharesIndex(): ReactElement {
  const { id } = routeApi.useParams();
  return (
    <Suspense
      fallback={
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-3)' }}>
          {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
          <Skeleton style={{ blockSize: '2rem', inlineSize: '16rem' }} />
          {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
          <Skeleton style={{ blockSize: '8rem', inlineSize: '100%' }} />
        </div>
      }
    >
      <ShareList workspaceId={id} />
    </Suspense>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/public-shares/')({
  component: WorkspacePublicSharesIndex,
});
