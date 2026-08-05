/**
 * /workspaces/$id — workspace detail layout.
 *
 * Renders a horizontal sub-nav (Overview / Projects / Timeline /
 * Settings) so the nested routes are reachable from the UI without
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
import {
  createFileRoute,
  Link,
  Outlet,
  useChildMatches,
  useNavigate,
} from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';

import { useWorkspaceQuery } from '../features/workspaces/api';
import WorkspaceDetail, { type WorkspaceDetailTab } from '../features/workspaces/workspace-detail';
import { ApiError } from '../lib/api-error';
import { authSdk as sdk } from '../lib/sdk';

/**
 * Allowed values for the `?tab=` search param across the workspace
 * subtree. The workspace overview only consumes `overview` / `members`,
 * but the nested project-detail route also reads `?tab=` and adds
 * `settings`, so the parent schema is a superset to keep child routes'
 * own search schemas compatible. The workspace overview itself maps
 * anything outside its own set (`overview` / `members`) to the default.
 */
const TAB_VALUES = ['overview', 'members', 'settings'] as const;

const searchSchema = z.object({
  tab: z.enum(TAB_VALUES).optional().catch('overview'),
});

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
  const { tab } = Route.useSearch();
  const navigate = useNavigate();
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
  // The Tabs primitive inside WorkspaceDetail is driven by the `?tab=`
  // search param so reloads and deep links (e.g. `?tab=members`)
  // restore the right panel instead of snapping back to overview. The
  // schema allows a superset (including `settings` for the nested
  // project route); anything outside the workspace's own tab set
  // resolves to `overview`.
  const activeTab: WorkspaceDetailTab = tab === 'members' ? 'members' : 'overview';

  if (insideProject) {
    return (
      <Suspense
        fallback={
          <div
            style={{
              display: 'flex',
              flexDirection: 'column',
              gap: 'var(--nf-space-4)',
              padding: 'var(--nf-space-6)',
            }}
          >
            {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
            <Skeleton style={{ blockSize: '2rem', inlineSize: '16rem' }} />
            {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
            <Skeleton style={{ blockSize: '12rem', inlineSize: '100%' }} />
          </div>
        }
      >
        <Outlet />
      </Suspense>
    );
  }

  return (
    <section
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-6)',
        padding: 'var(--nf-space-page)',
      }}
    >
      {hasChildRoute ? (
        <p
          style={{
            fontFamily: 'var(--nf-font-display)',
            fontSize: 'var(--nf-text-section-title)',
            margin: 0,
            color: 'var(--nf-color-fg)',
          }}
        >
          {workspace.name}
        </p>
      ) : null}
      <nav
        aria-label={t('workspaces.nav.label')}
        style={{
          display: 'flex',
          gap: 'var(--nf-space-1)',
          borderBlockEnd: '1px solid var(--nf-color-border)',
          paddingBlockEnd: 'var(--nf-space-2)',
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
                background: 'var(--nf-color-accent-subtle)',
                color: 'var(--nf-color-accent)',
                fontWeight: 500,
              },
            }}
            style={{
              display: 'inline-block',
              padding: 'var(--nf-space-2) var(--nf-space-3-5)',
              borderRadius: 'var(--nf-radius-md)',
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
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-4)' }}>
            {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
            <Skeleton style={{ blockSize: '2rem', inlineSize: '16rem' }} />
            {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
            <Skeleton style={{ blockSize: '12rem', inlineSize: '100%' }} />
          </div>
        }
      >
        {hasChildRoute ? (
          <Outlet />
        ) : (
          <WorkspaceDetail
            id={id}
            tab={activeTab}
            onTabChange={(next) => {
              void navigate({
                to: '/workspaces/$id',
                params: { id },
                search: (prev) => ({ ...prev, tab: next === 'overview' ? undefined : next }),
                replace: true,
              });
            }}
          />
        )}
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
      <Link
        to="/workspaces"
        style={{
          display: 'inline-flex',
          alignItems: 'center',
          padding: 'var(--nf-space-2) var(--nf-space-5)',
          borderRadius: 'var(--nf-radius-md)',
          background: 'var(--nf-color-accent)',
          color: 'var(--nf-color-fg-on-accent)',
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
  validateSearch: (raw) => searchSchema.parse(raw),
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
