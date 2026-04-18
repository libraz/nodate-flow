/**
 * /workspaces/$id/settings/auto-actions — auto-action executor settings (lazy).
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import AutoActionSettingsPage from '../features/workspaces/auto-action-settings';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/auto-actions');

function AutoActionsRoute(): ReactElement {
  const { id } = routeApi.useParams();
  return (
    <section
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: '1.5rem',
        inlineSize: '100%',
      }}
    >
      <Suspense
        fallback={
          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
            <Skeleton style={{ blockSize: '2rem', inlineSize: '16rem' }} />
            <Skeleton style={{ blockSize: '12rem', inlineSize: '100%' }} />
          </div>
        }
      >
        <AutoActionSettingsPage workspaceId={id} />
      </Suspense>
    </section>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/auto-actions')({
  component: AutoActionsRoute,
});
