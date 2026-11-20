/**
 * /workspaces/$id/projects/$projectId/timeline — project activity
 * timeline (lazy).
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import { useProjectQuery } from '../features/projects/api';
import ProjectSectionHeader from '../features/projects/project-section-header';
import TimelineSkeleton from '../features/timeline/timeline-skeleton';
import TimelineView from '../features/timeline/timeline-view';
import styles from './_authenticated.workspaces.$id.projects.$projectId.timeline.module.css';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/projects/$projectId/timeline');

function ProjectTimelineInner({ projectId }: { projectId: string }): ReactElement {
  const { data: project } = useProjectQuery(projectId);
  return (
    <TimelineView scope={{ kind: 'project', id: projectId }} workspaceId={project.workspaceId} />
  );
}

function ProjectTimelineRoute(): ReactElement {
  const { projectId } = routeApi.useParams();
  const { t } = useTranslation('timeline');
  return (
    <section className={styles.section}>
      <ProjectSectionHeader
        title={t('view.title')}
        help="timeline"
        description={t('view.description')}
      />
      <Suspense fallback={<TimelineSkeleton />}>
        <ProjectTimelineInner projectId={projectId} />
      </Suspense>
    </section>
  );
}

export const Route = createLazyFileRoute(
  '/_authenticated/workspaces/$id/projects/$projectId/timeline',
)({
  component: ProjectTimelineRoute,
});
