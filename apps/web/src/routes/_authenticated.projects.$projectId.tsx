/**
 * /projects/$projectId — project layout. Renders a horizontal sub-nav
 * (Overview / Tasks / Timeline) + nested child routes via <Outlet />.
 * The detail view itself lives in the sibling
 * `_authenticated.projects.$projectId.index.tsx`.
 *
 * The loader probes the project so deep-link 404s land on the branded
 * NotFound rendered inside the authenticated AppShell instead of
 * crashing the route into the root ErrorBoundary.
 */

import { Link, Outlet, createFileRoute, notFound } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { sdk } from '../lib/sdk';

type ProjectSubNavKey = 'overview' | 'tasks' | 'gantt' | 'timeline';

interface ProjectSubNavItem {
  key: ProjectSubNavKey;
  to:
    | '/projects/$projectId'
    | '/projects/$projectId/tasks'
    | '/projects/$projectId/gantt'
    | '/projects/$projectId/timeline';
}

const PROJECT_SUB_NAV: readonly ProjectSubNavItem[] = [
  { key: 'overview', to: '/projects/$projectId' },
  { key: 'tasks', to: '/projects/$projectId/tasks' },
  { key: 'gantt', to: '/projects/$projectId/gantt' },
  { key: 'timeline', to: '/projects/$projectId/timeline' },
];

function labelKeyFor(
  key: ProjectSubNavKey,
): 'projects.nav.overview' | 'projects.nav.tasks' | 'projects.nav.gantt' | 'projects.nav.timeline' {
  switch (key) {
    case 'overview':
      return 'projects.nav.overview';
    case 'tasks':
      return 'projects.nav.tasks';
    case 'gantt':
      return 'projects.nav.gantt';
    case 'timeline':
      return 'projects.nav.timeline';
  }
}

function ProjectLayout(): ReactElement {
  const { t } = useTranslation('common');
  const { projectId } = Route.useParams();
  return (
    <section
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: '1rem',
        padding: 'clamp(1rem, 3vw, 2rem) clamp(1rem, 3vw, 2rem) 0',
      }}
    >
      <nav
        aria-label={t('projects.nav.label')}
        style={{
          display: 'flex',
          gap: '0.25rem',
          borderBlockEnd: '1px solid var(--color-border)',
          paddingBlockEnd: '0.5rem',
        }}
      >
        {PROJECT_SUB_NAV.map((item) => (
          <Link
            key={item.key}
            to={item.to}
            params={{ projectId }}
            activeOptions={{ exact: item.key === 'overview' }}
            activeProps={{
              'aria-current': 'page',
              'data-active': 'true',
              style: {
                background: 'var(--color-surface)',
                color: 'var(--color-fg)',
                fontWeight: 600,
              },
            }}
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
      <Outlet />
    </section>
  );
}

export const Route = createFileRoute('/_authenticated/projects/$projectId')({
  component: ProjectLayout,
  loader: async ({ params }) => {
    const { response } = await sdk.GET('/projects/{prjId}', {
      params: { path: { prjId: params.projectId } },
    });
    if (response.status === 404) throw notFound();
    return null;
  },
});
