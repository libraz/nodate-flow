/**
 * /workspaces/$id/projects/$projectId — project layout. Renders a
 * horizontal sub-nav (Overview / Tasks / Gantt / Timeline) + nested
 * child routes via <Outlet />. The detail view itself lives in the
 * sibling `*.index.tsx`.
 *
 * The loader probes the project so deep-link 404s land on the branded
 * "Project not found" screen rendered by `errorComponent`, and 403s
 * land on the branded Forbidden state, instead of crashing the route
 * into the root ErrorBoundary. It also verifies that the project's
 * workspace matches the `$id` path segment; visiting
 * `/workspaces/WRONG/projects/X` must not silently render project X
 * under the wrong workspace.
 */

import { createFileRoute, Link, Outlet } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import Forbidden from '../components/forbidden';
import { useFavoritesQuery } from '../features/favorites/api';
import FavoriteButton from '../features/favorites/favorite-button';
import { useProjectQuery } from '../features/projects/api';
import { ApiError } from '../lib/api-error';
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

/**
 * ProjectFavoriteStar resolves the existing favorite entry (if any)
 * for the current project and renders the FavoriteButton in the
 * correct toggle state. Uses `useFavoritesQuery` (suspense) so the
 * caller must wrap it in a `<Suspense fallback={null}>` boundary.
 */
function ProjectFavoriteStar({
  projectId,
  workspaceId,
}: {
  projectId: string;
  workspaceId: string;
}): ReactElement {
  const { data: favorites } = useFavoritesQuery(workspaceId);
  const existing = favorites.find((f) => f.targetType === 'project' && f.targetId === projectId);
  return (
    <FavoriteButton
      workspaceId={workspaceId}
      targetType="project"
      targetId={projectId}
      {...(existing ? { favoriteId: existing.id } : {})}
    />
  );
}

function ProjectLayout(): ReactElement {
  const { t } = useTranslation('common');
  const { id, projectId } = Route.useParams();
  const { data: project } = useProjectQuery(projectId);
  // ProjectLayout always renders the project name as the page-level h1;
  // child routes (overview / tasks / gantt / timeline) do not re-emit
  // their own name heading.
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
        gap: 'var(--nf-space-4)',
        padding:
          'clamp(var(--nf-space-4), 3vw, var(--nf-space-8)) clamp(var(--nf-space-4), 3vw, var(--nf-space-8)) 0',
      }}
    >
      <div
        style={{
          display: 'flex',
          gap: 'var(--nf-space-2)',
          alignItems: 'center',
          flexWrap: 'wrap',
        }}
      >
        <h1
          style={{
            fontFamily: 'var(--nf-font-display)',
            fontSize: 'clamp(var(--nf-text-2xl), 2.5vw, 2rem)',
            margin: 0,
            flex: 1,
            minInlineSize: 0,
          }}
        >
          {project.name}
        </h1>
        <Suspense fallback={null}>
          <ProjectFavoriteStar projectId={projectId} workspaceId={id} />
        </Suspense>
      </div>
      {project.description ? (
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>{project.description}</p>
      ) : null}
      <nav
        aria-label={t('projects.nav.label')}
        style={{
          display: 'flex',
          gap: 'var(--nf-space-1)',
          borderBlockEnd: '1px solid var(--nf-color-border)',
          paddingBlockEnd: 'var(--nf-space-2)',
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
                background: 'var(--nf-color-accent-subtle)',
                color: 'var(--nf-color-accent)',
                fontWeight: 500,
              },
            }}
            style={{
              display: 'inline-block',
              padding: 'var(--nf-space-2) 0.875rem',
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

/**
 * Branded fallback for WS.PROJECT.NOT_FOUND. CTA takes the user back
 * to the parent workspace rather than all the way to /workspaces, so
 * the natural "oops, wrong project" recovery path is one click.
 */
function ProjectNotFound({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t } = useTranslation('common');
  return (
    <section
      style={{
        minBlockSize: '60vh',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 'var(--nf-space-5)',
        padding: 'var(--nf-space-12) var(--nf-space-8)',
        textAlign: 'center',
      }}
    >
      <div
        aria-hidden
        style={{
          fontFamily: 'var(--nf-font-display)',
          fontSize: 'clamp(5rem, 14vw, 9rem)',
          lineHeight: 1,
          fontWeight: 700,
          backgroundImage: 'var(--nf-gradient-wordmark)',
          backgroundClip: 'text',
          // biome-ignore lint/style/useNamingConvention: vendor prefix
          WebkitBackgroundClip: 'text',
          color: 'transparent',
          // biome-ignore lint/style/useNamingConvention: vendor prefix
          WebkitTextFillColor: 'transparent',
        }}
      >
        {t('not_found.code', { defaultValue: '404' })}
      </div>
      <h1
        style={{
          fontFamily: 'var(--nf-font-display)',
          margin: 0,
          fontSize: 'var(--nf-text-2xl)',
          color: 'var(--nf-color-fg)',
        }}
      >
        {t('projects.not_found.title')}
      </h1>
      <p
        style={{
          margin: 0,
          maxInlineSize: '28rem',
          color: 'var(--nf-color-fg-muted)',
        }}
      >
        {t('projects.not_found.body')}
      </p>
      <Link
        to="/workspaces/$id"
        params={{ id: workspaceId }}
        style={{
          display: 'inline-flex',
          alignItems: 'center',
          padding: 'var(--nf-space-2) var(--nf-space-5)',
          borderRadius: '0.5rem',
          background: 'var(--nf-color-accent)',
          color: 'var(--nf-color-fg-on-accent)',
          textDecoration: 'none',
          fontWeight: 500,
        }}
      >
        {t('projects.not_found.cta')}
      </Link>
    </section>
  );
}

/**
 * Extract the API error code from an unknown thrown value. Accepts
 * both the newer `{type, detail, title}` RFC 7807 shape and the older
 * `{code, message}` envelope still emitted by the ACL middleware for
 * WS.WORKSPACE.NOT_FOUND / WS.PROJECT.NOT_FOUND.
 */
function errorCodeOf(err: unknown): string | undefined {
  if (err instanceof ApiError) return err.code;
  if (err && typeof err === 'object') {
    const obj = err as { code?: unknown; type?: unknown };
    if (typeof obj.code === 'string') return obj.code;
    if (typeof obj.type === 'string') return obj.type;
  }
  return undefined;
}

function ProjectErrorComponent({ error }: { error: unknown }): ReactElement {
  const { id } = Route.useParams();
  if (error instanceof ForbiddenError) {
    return <Forbidden />;
  }
  if (errorCodeOf(error) === 'WS.PROJECT.NOT_FOUND') {
    return <ProjectNotFound workspaceId={id} />;
  }
  throw error;
}

export const Route = createFileRoute('/_authenticated/workspaces/$id/projects/$projectId')({
  component: ProjectLayout,
  errorComponent: ProjectErrorComponent,
  loader: async ({ params }) => {
    const { data, response } = await sdk.GET('/projects/{prjId}', {
      params: { path: { prjId: params.projectId } },
    });
    if (response.status === 404) {
      throw new ApiError('WS.PROJECT.NOT_FOUND', 'Project not found');
    }
    if (response.status === 403) throw new ForbiddenError();
    // Cross-workspace protection: the project exists and the caller has
    // access, but the URL claims a different workspace. Treat it as a
    // 404 so we never render project data under the wrong workspace
    // breadcrumb.
    if (data && data.workspaceId !== params.id) {
      throw new ApiError('WS.PROJECT.NOT_FOUND', 'Project not found');
    }
    return null;
  },
});
