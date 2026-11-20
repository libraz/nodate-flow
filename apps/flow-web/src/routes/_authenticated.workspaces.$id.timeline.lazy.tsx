/**
 * /workspaces/$id/timeline — workspace activity timeline (lazy).
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import PageHeader from '../components/page-header';
import TimelineSkeleton from '../features/timeline/timeline-skeleton';
import TimelineView from '../features/timeline/timeline-view';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/timeline');

function WorkspaceTimelineRoute(): ReactElement {
  const { id } = routeApi.useParams();
  const { t: tTimeline } = useTranslation('timeline');
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-6)' }}>
      <PageHeader title={tTimeline('view.title')} help="timeline" />
      <Suspense fallback={<TimelineSkeleton />}>
        <TimelineView scope={{ kind: 'workspace', id }} />
      </Suspense>
    </div>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/timeline')({
  component: WorkspaceTimelineRoute,
});
