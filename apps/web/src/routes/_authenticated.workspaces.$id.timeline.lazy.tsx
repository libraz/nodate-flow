/**
 * /workspaces/$id/timeline — workspace activity timeline (lazy).
 */

import Spinner from '@nodate-flow/ui/primitives/spinner';
import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import TimelineView from '../features/timeline/timeline-view';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/timeline');

function WorkspaceTimelineRoute(): ReactElement {
  const { id } = routeApi.useParams();
  const { t } = useTranslation('common');
  return (
    <Suspense
      fallback={
        <div style={{ padding: '2rem', display: 'flex', justifyContent: 'center' }}>
          <Spinner label={t('common.loading')} />
        </div>
      }
    >
      <TimelineView scope={{ kind: 'workspace', id }} />
    </Suspense>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/timeline')({
  component: WorkspaceTimelineRoute,
});
