/**
 * /workspaces/$id/activity — unified workspace activity feed (lazy).
 *
 * Surfaces the audit + ai + mcp activity union for the workspace. Mirrors
 * the sibling `timeline` route: a titled header over a Suspense-wrapped
 * feature view scoped to the route's workspace id.
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import PageHeader from '../components/page-header';
import ActivityFeed from '../features/activity/activity-feed';
import type { ActivitySourceFilter } from '../features/activity/api';
import HistoryTabs from '../features/history/history-tabs';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/activity');

function WorkspaceActivityRoute(): ReactElement {
  const { id } = routeApi.useParams();
  const { t } = useTranslation('activity');
  const [source, setSource] = useState<ActivitySourceFilter>('all');

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-6)' }}>
      <PageHeader title={t('view.title')} description={t('view.description')} help="activity" />
      <HistoryTabs workspaceId={id} />
      <ActivityFeed workspaceId={id} source={source} onSourceChange={setSource} />
    </div>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/activity')({
  component: WorkspaceActivityRoute,
});
