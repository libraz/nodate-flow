/**
 * /workspaces/$id/projects/$projectId/timeline — project activity
 * timeline (lazy).
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import { useProjectQuery } from '../features/projects/api';
import TimelineSkeleton from '../features/timeline/timeline-skeleton';
import TimelineView from '../features/timeline/timeline-view';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/projects/$projectId/timeline');

function ProjectTimelineInner({ projectId }: { projectId: string }): ReactElement {
  const { data: project } = useProjectQuery(projectId);
  return (
    <TimelineView scope={{ kind: 'project', id: projectId }} workspaceId={project.workspaceId} />
  );
}

function ProjectTimelineRoute(): ReactElement {
  const { projectId } = routeApi.useParams();
  return (
    <Suspense fallback={<TimelineSkeleton />}>
      <ProjectTimelineInner projectId={projectId} />
    </Suspense>
  );
}

export const Route = createLazyFileRoute(
  '/_authenticated/workspaces/$id/projects/$projectId/timeline',
)({
  component: ProjectTimelineRoute,
});
