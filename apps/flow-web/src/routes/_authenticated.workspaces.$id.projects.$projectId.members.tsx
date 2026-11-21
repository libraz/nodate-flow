/**
 * /workspaces/$id/projects/$projectId/members — project members.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import ProjectMembersTable from '../features/projects/project-members-table';

function ProjectMembersRoute(): ReactElement {
  const { projectId } = Route.useParams();
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
      <ProjectMembersTable projectId={projectId} />
    </Suspense>
  );
}

export const Route = createFileRoute('/_authenticated/workspaces/$id/projects/$projectId/members')({
  component: ProjectMembersRoute,
});
