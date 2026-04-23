/**
 * /workspaces/$id/projects/$projectId/ — project detail
 * (Overview / Members / Settings).
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import ProjectDetail from '../features/projects/project-detail';

function ProjectDetailRoute(): ReactElement {
  const { projectId } = Route.useParams();
  return (
    <Suspense
      fallback={
        <div style={{ padding: '2rem', display: 'flex', flexDirection: 'column', gap: '1rem' }}>
          <Skeleton style={{ blockSize: '2rem', inlineSize: '16rem' }} />
          <Skeleton style={{ blockSize: '12rem', inlineSize: '100%' }} />
        </div>
      }
    >
      <ProjectDetail id={projectId} />
    </Suspense>
  );
}

export const Route = createFileRoute('/_authenticated/workspaces/$id/projects/$projectId/')({
  component: ProjectDetailRoute,
});
