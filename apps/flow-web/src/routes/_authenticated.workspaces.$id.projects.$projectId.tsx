/**
 * /workspaces/$id/projects/$projectId — project layout. Renders a
 * horizontal sub-nav (Overview / Tasks / Gantt / Timeline) + nested
 * child routes via <Outlet />. The detail view itself lives in the
 * sibling `*.index.tsx`.
 *
 * The loader probes the project so deep-link 404s land on the branded
 * NotFound rendered inside the authenticated AppShell, and 403s land on
 * the branded Forbidden state, instead of crashing the route into the
 * root ErrorBoundary. It also verifies that the project's workspace
 * matches the `$id` path segment; visiting
 * `/workspaces/WRONG/projects/X` must not silently render project X
 * under the wrong workspace.
 */

import { Link, Outlet, createFileRoute, notFound, useChildMatches } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import Forbidden from '../components/forbidden';
import { useProjectQuery } from '../features/projects/api';
import { sdk } from '../lib/sdk';

/** Sentinel thrown by the route loader to distinguish 403 from generic errors. */
class ForbiddenError extends Error {
  readonly status = 403;
  constructor() {
    super('forbidden');
    this.name = 'ForbiddenError';
  }
}

type ProjectSubNavKey = 'overview' | 'tasks' | 'gantt' | 'timeline';

interface ProjectSubNavItem {
  key: ProjectSubNavKey;
  to:
    | '/workspaces/$id/projects/$projectId'
    | '/workspaces/$id/projects/$projectId/tasks'
    | '/workspaces/$id/projects/$projectId/gantt'
    | '/workspaces/$id/projects/$projectId/timeline';
}

const PROJECT_SUB_NAV: readonly ProjectSubNavItem[] = [
  { key: 'overview', to: '/workspaces/$id/projects/$projectId' },
  { key: 'tasks', to: '/workspaces/$id/projects/$projectId/tasks' },
  { key: 'gantt', to: '/workspaces/$id/projects/$projectId/gantt' },
  { key: 'timeline', to: '/workspaces/$id/projects/$projectId/timeline' },
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
  const { id, projectId } = Route.useParams();
  const { data: project } = useProjectQuery(projectId);
  // On /workspaces/$id/projects/$projectId/ (overview) the detail
  // component renders its own project-name heading. On the nested child
  // routes (tasks/gantt/timeline) nothing else carries the project
  // name, so show it here to keep context.
  const childMatches = useChildMatches();
  const onOverview = childMatches.length === 0;
  // Defence in depth: the loader already converts 404/403 into branded
  // states, but if the query somehow resolves to a nullish value (stale
  // cache, race condition), render the access-denied state rather than
  // dereferencing null.
  if (!project) {
    return <Forbidden />;
  }
  return (
    <section
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: '1rem',
        padding: 'clamp(1rem, 3vw, 2rem) clamp(1rem, 3vw, 2rem) 0',
      }}
    >
      {onOverview ? null : (
        <h1
          style={{
            fontFamily: 'var(--font-display)',
            fontSize: 'clamp(1.5rem, 2.5vw, 2rem)',
            margin: 0,
          }}
        >
          {project.name}
        </h1>
      )}
      <nav
        aria-label={t('projects.nav.label')}
        style={{
          display: 'flex',
          gap: '0.25rem',
          borderBlockEnd: '1px solid var(--nf-color-border)',
          paddingBlockEnd: '0.5rem',
        }}
      >
        {PROJECT_SUB_NAV.map((item) => (
          <Link
            key={item.key}
            to={item.to}
            params={{ id, projectId }}
            activeOptions={{ exact: item.key === 'overview' }}
            activeProps={{
              'aria-current': 'page',
              'data-active': 'true',
              style: {
                background: 'var(--nf-color-accent-subtle, rgba(155,89,182,0.12))',
                color: 'var(--nf-color-accent, var(--nf-color-accent))',
                fontWeight: 500,
              },
            }}
            style={{
              display: 'inline-block',
              padding: '0.5rem 0.875rem',
              borderRadius: '0.5rem',
              color: 'var(--nf-color-fg)',
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

export const Route = createFileRoute('/_authenticated/workspaces/$id/projects/$projectId')({
  component: ProjectLayout,
  errorComponent: ({ error }) => {
    if (error instanceof ForbiddenError) {
      return <Forbidden />;
    }
    throw error;
  },
  loader: async ({ params }) => {
    const { data, response } = await sdk.GET('/projects/{prjId}', {
      params: { path: { prjId: params.projectId } },
    });
    if (response.status === 404) throw notFound();
    if (response.status === 403) throw new ForbiddenError();
    // Cross-workspace protection: the project exists and the caller has
    // access, but the URL claims a different workspace. Treat it as a
    // 404 so we never render project data under the wrong workspace
    // breadcrumb.
    if (data && data.workspaceId !== params.id) throw notFound();
    return null;
  },
});
