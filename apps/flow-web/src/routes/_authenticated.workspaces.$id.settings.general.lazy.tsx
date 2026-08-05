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
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-3)' }}>
          {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
          <Skeleton style={{ blockSize: '2rem', inlineSize: '16rem' }} />
          {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
          <Skeleton style={{ blockSize: '2.5rem', inlineSize: '100%' }} />
          {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
          <Skeleton style={{ blockSize: '2.5rem', inlineSize: '100%' }} />
          {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
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
