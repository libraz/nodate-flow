/**
 * /workspaces/$id/projects — list view for projects in a workspace.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import ProjectList from '../features/projects/project-list';

function WorkspaceProjectsIndexRoute(): ReactElement {
  const { id } = Route.useParams();
  return (
    <Suspense
      fallback={
        <div style={{ padding: '2rem', display: 'flex', flexDirection: 'column', gap: '1rem' }}>
          <Skeleton style={{ blockSize: '2rem', inlineSize: '12rem' }} />
          <Skeleton style={{ blockSize: '16rem', inlineSize: '100%' }} />
        </div>
      }
    >
      <ProjectList workspaceId={id} />
    </Suspense>
  );
}

export const Route = createFileRoute('/_authenticated/workspaces/$id/projects/')({
  component: WorkspaceProjectsIndexRoute,
});
