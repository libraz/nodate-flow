/**
 * /workspaces/$id — workspace detail layout.
 *
 * Renders a horizontal sub-nav (Overview / Timeline / Settings) so the
 * nested routes are reachable from the UI without typing URLs. When no
 * child route is matched, the workspace overview is rendered inline.
 *
 * Loader + errorComponent intercept WS.WORKSPACE.NOT_FOUND so
 * fabricated or stale workspace UUIDs land on a branded "Workspace not
 * found" screen instead of the root FatalFallback. Other errors bubble
 * up to the root boundary.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { Link, Outlet, createFileRoute, useChildMatches } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import { useWorkspaceQuery } from '../features/workspaces/api';
import WorkspaceDetail from '../features/workspaces/workspace-detail';
import { ApiError } from '../lib/api-error';
import { authSdk as sdk } from '../lib/sdk';

type SubNavKey = 'overview' | 'projects' | 'timeline' | 'settings';

interface SubNavItem {
  key: SubNavKey;
  to:
    | '/workspaces/$id'
    | '/workspaces/$id/projects'
    | '/workspaces/$id/timeline'
    | '/workspaces/$id/settings';
}

const SUB_NAV: readonly SubNavItem[] = [
  { key: 'overview', to: '/workspaces/$id' },
  { key: 'projects', to: '/workspaces/$id/projects' },
  { key: 'timeline', to: '/workspaces/$id/timeline' },
  { key: 'settings', to: '/workspaces/$id/settings' },
];

function labelKeyFor(
  key: SubNavKey,
):
  | 'workspaces.nav.overview'
  | 'workspaces.nav.projects'
  | 'workspaces.nav.timeline'
  | 'workspaces.nav.settings' {
  switch (key) {
    case 'overview':
      return 'workspaces.nav.overview';
    case 'projects':
      return 'workspaces.nav.projects';
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
  // WorkspaceDetail renders its own <h1> on the overview. On child
  // routes (projects / timeline / settings) nothing carries the
  // workspace name, so show it here for context.
  const { data: workspace } = useWorkspaceQuery(id);

  return (
    <section
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: '1.5rem',
        padding: 'clamp(1.5rem, 4vw, 2.5rem)',
      }}
    >
      {hasChildRoute ? (
        <h1
          style={{
            fontFamily: 'var(--font-display)',
            fontSize: 'clamp(1.5rem, 2.5vw, 2rem)',
            margin: 0,
          }}
        >
          {workspace.name}
        </h1>
      ) : null}
      <nav
        aria-label={t('workspaces.nav.label')}
        style={{
          display: 'flex',
          gap: '0.25rem',
          borderBlockEnd: '1px solid var(--nf-color-border)',
          paddingBlockEnd: '0.5rem',
        }}
      >
        {SUB_NAV.map((item) => (
          <Link
            key={item.key}
            to={item.to}
            params={{ id }}
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
        gap: '1.25rem',
        padding: '3rem 2rem',
        textAlign: 'center',
      }}
    >
      <div
        aria-hidden
        style={{
          fontFamily: 'var(--font-display)',
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
          fontFamily: 'var(--font-display)',
          margin: 0,
          fontSize: '1.5rem',
          color: 'var(--nf-color-fg)',
        }}
      >
        {t('workspaces.not_found.title')}
      </h1>
      <p
        style={{
          margin: 0,
          maxInlineSize: '28rem',
          color: 'var(--nf-color-fg-muted)',
        }}
      >
        {t('workspaces.not_found.body')}
      </p>
      <Link
        to="/workspaces"
        style={{
          display: 'inline-flex',
          alignItems: 'center',
          padding: '0.5rem 1.25rem',
          borderRadius: '0.5rem',
          background: 'var(--nf-color-accent, var(--nf-color-accent))',
          color: 'var(--nf-color-fg-on-accent, white)',
          textDecoration: 'none',
          fontWeight: 500,
        }}
      >
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
    const { response } = await sdk.GET('/workspaces/{wsId}', {
      params: { path: { wsId: params.id } },
    });
    if (response.status === 404) {
      throw new ApiError('WS.WORKSPACE.NOT_FOUND', 'Workspace not found');
    }
    return null;
  },
});
