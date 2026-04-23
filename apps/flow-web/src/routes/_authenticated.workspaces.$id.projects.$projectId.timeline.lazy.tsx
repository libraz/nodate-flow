/**
 * /workspaces/$id/projects/$projectId/timeline — project activity
 * timeline (lazy).
 */

import Spinner from '@nodate-flow/ui/primitives/spinner';
import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import { useProjectQuery } from '../features/projects/api';
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
  const { t } = useTranslation('common');
  return (
    <Suspense
      fallback={
        <div style={{ padding: '2rem', display: 'flex', justifyContent: 'center' }}>
          <Spinner label={t('common.loading')} />
        </div>
      }
    >
      <ProjectTimelineInner projectId={projectId} />
    </Suspense>
  );
}

export const Route = createLazyFileRoute(
  '/_authenticated/workspaces/$id/projects/$projectId/timeline',
)({
  component: ProjectTimelineRoute,
});
