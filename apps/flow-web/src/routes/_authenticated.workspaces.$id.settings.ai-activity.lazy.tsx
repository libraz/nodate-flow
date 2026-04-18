/**
 * /workspaces/$id/settings/ai-activity — workspace AI invocation audit (lazy).
 *
 * Surfaces recent redacted LLM calls so workspace members can audit
 * what their AI providers have been asked and what came back
 * (2.WEB-2). Read-only.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import InvocationsList from '../features/ai-providers/invocations-list';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/ai-activity');

function AiActivityRoute(): ReactElement {
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
            <Skeleton style={{ blockSize: '6rem', inlineSize: '100%' }} />
          </div>
        }
      >
        <InvocationsList workspaceId={id} />
      </Suspense>
    </section>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/ai-activity')({
  component: AiActivityRoute,
});
