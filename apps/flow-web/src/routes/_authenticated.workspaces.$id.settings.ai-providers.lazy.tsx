/**
 * /workspaces/$id/settings/ai-providers — workspace AI provider management (lazy).
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import ProviderList from '../features/ai-providers/provider-list';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/ai-providers');

function AiProvidersRoute(): ReactElement {
  const { id } = routeApi.useParams();
  return (
    <section
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-6)',
        inlineSize: '100%',
      }}
    >
      <Suspense
        fallback={
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-3)' }}>
            {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
            <Skeleton style={{ blockSize: '2rem', inlineSize: '16rem' }} />
            {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
            <Skeleton style={{ blockSize: '6rem', inlineSize: '100%' }} />
          </div>
        }
      >
        <ProviderList workspaceId={id} />
      </Suspense>
    </section>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/ai-providers')({
  component: AiProvidersRoute,
});
