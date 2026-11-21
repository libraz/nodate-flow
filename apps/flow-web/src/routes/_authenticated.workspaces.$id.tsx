/**
 * /workspaces/$id — workspace detail layout.
 *
 * Navigation between workspace sections lives in the sidebar; this layout
 * only hosts the active child. With no child route matched it redirects
 * to the projects list.
 *
 * When the active child route is under `projects/$projectId/*`, the
 * workspace-level header is suppressed entirely: the nested project route
 * renders its own h1 + tab strip, and the top-bar breadcrumb carries the
 * "Workspace › Project" context.
 *
 * Loader + errorComponent intercept WS.WORKSPACE.NOT_FOUND so
 * fabricated or stale workspace UUIDs land on a branded "Workspace not
 * found" screen instead of the root FatalFallback. Other errors bubble
 * up to the root boundary. A 401/403 (the caller is not a member) renders
 * the Forbidden page inside the shell.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createFileRoute, Link, Navigate, Outlet, useChildMatches } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import Forbidden from '../components/forbidden';
import PageFrame from '../components/page-frame';
import styles from '../features/workspaces/workspaces.module.css';
import { authApiProbe } from '../lib/api';
import { ApiError } from '../lib/api-error';

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
  const { id } = Route.useParams();
  const childMatches = useChildMatches();
  const hasChildRoute = childMatches.length > 0;
  const insideProject = useInsideProjectRoute();

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
          <Navigate to="/workspaces/$id/projects" params={{ id }} replace />
        )}
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
    if (error instanceof ApiError && (error.httpStatus === 401 || error.httpStatus === 403)) {
      return <Forbidden />;
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
    if (status === 403) {
      throw new ApiError(
        'WS.WORKSPACE.ACCESS_DENIED',
        'You do not have access to this workspace',
        403,
      );
    }
    return null;
  },
});
