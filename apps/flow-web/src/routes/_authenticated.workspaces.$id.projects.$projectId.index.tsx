/**
 * /workspaces/$id/projects/$projectId/ — project overview.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createFileRoute, redirect } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import { OverviewPanel } from '../features/projects/project-detail';

function ProjectOverviewRoute(): ReactElement {
  const { id, projectId } = Route.useParams();
  return (
    <Suspense
      fallback={
        <div
          aria-busy="true"
          style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-6)' }}
        >
          {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
          <Skeleton style={{ blockSize: 'var(--nf-control-height)', inlineSize: '16rem' }} />
          {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
          <Skeleton style={{ blockSize: '12rem', inlineSize: '100%' }} />
        </div>
      }
    >
      <OverviewPanel id={projectId} workspaceId={id} />
    </Suspense>
  );
}

/** Legacy `?tab=` links: members and settings became their own routes. */
export function legacyTabTarget(
  tab: string,
):
  | '/workspaces/$id/projects/$projectId/members'
  | '/workspaces/$id/projects/$projectId/settings'
  | '/workspaces/$id/projects/$projectId' {
  if (tab === 'members') return '/workspaces/$id/projects/$projectId/members';
  if (tab === 'settings') return '/workspaces/$id/projects/$projectId/settings';
  return '/workspaces/$id/projects/$projectId';
}

export const Route = createFileRoute('/_authenticated/workspaces/$id/projects/$projectId/')({
  validateSearch: (search: Record<string, unknown>): { tab?: string } =>
    typeof search.tab === 'string' ? { tab: search.tab } : {},
  beforeLoad: ({ search, params }) => {
    if (search.tab === undefined) return;
    throw redirect({
      to: legacyTabTarget(search.tab),
      params: { id: params.id, projectId: params.projectId },
      replace: true,
    });
  },
  component: ProjectOverviewRoute,
});
