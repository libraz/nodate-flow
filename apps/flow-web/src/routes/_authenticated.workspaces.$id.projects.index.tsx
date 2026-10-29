/**
 * /workspaces/$id/projects — list view for projects in a workspace.
 */

import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import ProjectList from '../features/projects/project-list';
import ListPageSkeleton from '../features/workspaces/list-page-skeleton';

function WorkspaceProjectsIndexRoute(): ReactElement {
  const { id } = Route.useParams();
  return (
    <Suspense fallback={<ListPageSkeleton nested />}>
      <ProjectList workspaceId={id} />
    </Suspense>
  );
}

export const Route = createFileRoute('/_authenticated/workspaces/$id/projects/')({
  component: WorkspaceProjectsIndexRoute,
});
