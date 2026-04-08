/**
 * /workspaces/$id — workspace detail layout.
 *
 * Renders a horizontal sub-nav (Overview / Timeline / Settings) so the
 * nested routes are reachable from the UI without typing URLs. When no
 * child route is matched, the workspace overview is rendered inline.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { Link, Outlet, createFileRoute, useChildMatches } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import WorkspaceDetail from '../features/workspaces/workspace-detail';

type SubNavKey = 'overview' | 'timeline' | 'settings';

interface SubNavItem {
  key: SubNavKey;
  to: '/workspaces/$id' | '/workspaces/$id/timeline' | '/workspaces/$id/settings';
}

const SUB_NAV: readonly SubNavItem[] = [
  { key: 'overview', to: '/workspaces/$id' },
  { key: 'timeline', to: '/workspaces/$id/timeline' },
  { key: 'settings', to: '/workspaces/$id/settings' },
];

function labelKeyFor(
  key: SubNavKey,
): 'workspaces.nav.overview' | 'workspaces.nav.timeline' | 'workspaces.nav.settings' {
  switch (key) {
    case 'overview':
      return 'workspaces.nav.overview';
    case 'timeline':
      return 'workspaces.nav.timeline';
    case 'settings':
      return 'workspaces.nav.settings';
  }
}

function WorkspaceDetailRoute(): ReactElement {
  const { t } = useTranslation('common');
  const { id } = Route.useParams();
  const childMatches = useChildMatches();
  const hasChildRoute = childMatches.length > 0;

  return (
    <section
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: '1.5rem',
        padding: 'clamp(1.5rem, 4vw, 2.5rem)',
      }}
    >
      <nav
        aria-label={t('workspaces.nav.label')}
        style={{
          display: 'flex',
          gap: '0.25rem',
          borderBlockEnd: '1px solid var(--color-border)',
          paddingBlockEnd: '0.5rem',
        }}
      >
        {SUB_NAV.map((item) => (
          <Link
            key={item.key}
            to={item.to}
            params={{ id }}
            activeOptions={{ exact: item.key === 'overview' }}
            activeProps={{ 'aria-current': 'page', 'data-active': 'true' }}
            style={{
              display: 'inline-block',
              padding: '0.5rem 0.875rem',
              borderRadius: '0.5rem',
              color: 'var(--color-fg)',
              textDecoration: 'none',
            }}
          >
            {t(labelKeyFor(item.key))}
          </Link>
        ))}
      </nav>

      <Suspense
        fallback={
          <div style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
            <Skeleton style={{ blockSize: '2rem', inlineSize: '16rem' }} />
            <Skeleton style={{ blockSize: '12rem', inlineSize: '100%' }} />
          </div>
        }
      >
        {hasChildRoute ? <Outlet /> : <WorkspaceDetail id={id} />}
      </Suspense>
    </section>
  );
}

export const Route = createFileRoute('/_authenticated/workspaces/$id')({
  component: WorkspaceDetailRoute,
});
