/**
 * /workspaces/$id/settings/general — edit basic workspace metadata (lazy).
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import WorkspaceSettingsForm from '../features/workspaces/workspace-settings-form';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/general');

function WorkspaceGeneralRoute(): ReactElement {
  const { id } = routeApi.useParams();
  return (
    <Suspense
      fallback={
        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
          <Skeleton style={{ blockSize: '2rem', inlineSize: '16rem' }} />
          <Skeleton style={{ blockSize: '2.5rem', inlineSize: '100%' }} />
          <Skeleton style={{ blockSize: '2.5rem', inlineSize: '100%' }} />
          <Skeleton style={{ blockSize: '6rem', inlineSize: '100%' }} />
        </div>
      }
    >
      <WorkspaceSettingsForm workspaceId={id} />
    </Suspense>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/general')({
  component: WorkspaceGeneralRoute,
});
