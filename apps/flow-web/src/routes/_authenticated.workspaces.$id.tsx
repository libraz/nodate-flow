/**
 * /workspaces/$id — workspace detail layout.
 *
 * Renders a horizontal sub-nav (Overview / Projects / Timeline /
 * Members / Settings) so the nested routes are reachable from the UI without
 * typing URLs. When no child route is matched, the workspace overview
 * is rendered inline.
 *
 * When the active child route is under `projects/$projectId/*`, the
 * workspace-level header and tab strip are suppressed entirely. The
 * nested project route already renders its own h1 + tab strip, and the
 * top-bar breadcrumb carries the "Workspace › Project" context, so
 * repeating the workspace chrome would stack three nav layers above
 * page content and generate multiple h1 elements on the same document.
 *
 * Loader + errorComponent intercept WS.WORKSPACE.NOT_FOUND so
 * fabricated or stale workspace UUIDs land on a branded "Workspace not
 * found" screen instead of the root FatalFallback. Other errors bubble
 * up to the root boundary.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createFileRoute, Link, Outlet, useChildMatches } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import PageFrame from '../components/page-frame';
import { useWorkspaceQuery } from '../features/workspaces/api';
import WorkspaceDetail from '../features/workspaces/workspace-detail';
import styles from '../features/workspaces/workspaces.module.css';
import { authApiProbe } from '../lib/api';
import { ApiError } from '../lib/api-error';

type SubNavKey = 'overview' | 'projects' | 'timeline' | 'members' | 'settings';

interface SubNavItem {
  key: SubNavKey;
  to:
    | '/workspaces/$id'
    | '/workspaces/$id/projects'
    | '/workspaces/$id/timeline'
    | '/workspaces/$id/members'
    | '/workspaces/$id/settings';
}

const SUB_NAV: readonly SubNavItem[] = [
  { key: 'overview', to: '/workspaces/$id' },
  { key: 'projects', to: '/workspaces/$id/projects' },
  { key: 'timeline', to: '/workspaces/$id/timeline' },
  { key: 'members', to: '/workspaces/$id/members' },
  { key: 'settings', to: '/workspaces/$id/settings' },
];

function labelKeyFor(
  key: SubNavKey,
):
  | 'workspaces.nav.overview'
  | 'workspaces.nav.projects'
  | 'workspaces.nav.timeline'
  | 'workspaces.nav.members'
  | 'workspaces.nav.settings' {
  switch (key) {
    case 'overview':
      return 'workspaces.nav.overview';
    case 'projects':
      return 'workspaces.nav.projects';
    case 'timeline':
      return 'workspaces.nav.timeline';
    case 'members':
      return 'workspaces.nav.members';
    case 'settings':
      return 'workspaces.nav.settings';
  }
}

/**
 * True when any child match is the project-detail subtree
 * (`/workspaces/$id/projects/$projectId/*`). The project layout owns
 * its own h1 + tab strip, so the workspace chrome is hidden there to
 * avoid stacking two header blocks above the same page.
 */
function useInsideProjectRoute(): boolean {
  const childMatches = useChildMatches();
  return childMatches.some((m) => {
    const routeId = typeof m.routeId === 'string' ? m.routeId : '';
    return routeId.includes('/projects/$projectId');
  });
}

function WorkspaceDetailRoute(): ReactElement {
  const { t } = useTranslation('common');
  const { id } = Route.useParams();
  const childMatches = useChildMatches();
  const hasChildRoute = childMatches.length > 0;
  const insideProject = useInsideProjectRoute();
  // WorkspaceDetail renders its own <h1> on the overview. On child
  // routes (projects / timeline / settings) the active child owns the
  // page <h1> (section title), so the workspace name here is rendered
  // as a non-heading <p> styled like a breadcrumb — keeping the
  // document outline at a single top-level heading without inverting
  // heading order (h2 before h1). Project-detail routes render their
  // own chrome, so the layout bails out entirely (see `insideProject`
  // branch below).
  const { data: workspace } = useWorkspaceQuery(id);

  if (insideProject) {
    return (
      <Suspense
        fallback={
          <PageFrame>
            {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
            <Skeleton style={{ blockSize: '2rem', inlineSize: '16rem' }} />
            {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
            <Skeleton style={{ blockSize: '12rem', inlineSize: '100%' }} />
          </PageFrame>
        }
      >
        <Outlet />
      </Suspense>
    );
  }

  return (
    <PageFrame>
      {hasChildRoute ? <p className={styles.workspaceContext}>{workspace.name}</p> : null}
      <nav aria-label={t('workspaces.nav.label')} className={styles.subNav}>
        {SUB_NAV.map((item) => (
          <Link
            key={item.key}
            to={item.to}
            params={{ id }}
            activeOptions={{ exact: item.key === 'overview' }}
            activeProps={{ 'aria-current': 'page', 'data-active': 'true' }}
            className={styles.subNavLink}
          >
            {t(labelKeyFor(item.key))}
          </Link>
        ))}
      </nav>

      <Suspense
        fallback={
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-4)' }}>
            {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
            <Skeleton style={{ blockSize: '2rem', inlineSize: '16rem' }} />
            {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
            <Skeleton style={{ blockSize: '12rem', inlineSize: '100%' }} />
          </div>
        }
      >
        {hasChildRoute ? <Outlet /> : <WorkspaceDetail id={id} />}
      </Suspense>
    </PageFrame>
  );
}

/**
 * Branded fallback for WS.WORKSPACE.NOT_FOUND. Rendered when a
 * fabricated or stale workspace UUID reaches the loader or any
 * suspense query that throws inside this subtree.
 */
function WorkspaceNotFound(): ReactElement {
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
          fontSize: 'var(--nf-text-status-glyph)',
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
        {t('workspaces.not_found.title')}
      </h1>
      <p
        style={{
          margin: 0,
          // nf-token-override: component dimension, not a spacing step
          maxInlineSize: '28rem',
          color: 'var(--nf-color-fg-muted)',
        }}
      >
        {t('workspaces.not_found.body')}
      </p>
      <Link to="/workspaces" className={styles.ctaLink}>
        {t('workspaces.not_found.cta')}
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

export const Route = createFileRoute('/_authenticated/workspaces/$id')({
  component: WorkspaceDetailRoute,
  errorComponent: ({ error }) => {
    if (errorCodeOf(error) === 'WS.WORKSPACE.NOT_FOUND') {
      return <WorkspaceNotFound />;
    }
    throw error;
  },
  loader: async ({ params }) => {
    // The loader only asks whether the workspace is there; the shape of
    // the answer is the component's business. auth-api owns workspaces,
    // so the question goes there — flow-api serves no such path.
    const status = await authApiProbe((client) =>
      client.GET('/workspaces/{wsId}', { params: { path: { wsId: params.id } } }),
    );
    if (status === 404) {
      throw new ApiError('WS.WORKSPACE.NOT_FOUND', 'Workspace not found');
    }
    return null;
  },
});
