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
  const { t: tTimeline } = useTranslation('timeline');
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-4)' }}>
      <header>
        <h1 style={{ margin: 0, fontSize: 'var(--nf-text-xl)', color: 'var(--nf-color-fg)' }}>
          {tTimeline('view.title')}
        </h1>
      </header>
      <Suspense
        fallback={
          <div style={{ padding: 'var(--nf-space-8)', display: 'flex', justifyContent: 'center' }}>
            <Spinner label={t('common.loading')} />
          </div>
        }
      >
        <TimelineView scope={{ kind: 'workspace', id }} />
      </Suspense>
    </div>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/timeline')({
  component: WorkspaceTimelineRoute,
});
