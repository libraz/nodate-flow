import { ApiError } from '@nodate-flow/sdk';
import PageSkeleton from '@nodate-flow/ui/primitives/page-skeleton';
import { createRootRouteWithContext, Link, Outlet, useRouterState } from '@tanstack/react-router';
import { lazy, type ReactElement, Suspense, useEffect, useRef } from 'react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';
import NotFoundContent from '../components/not-found';
import { useAuthBootstrap } from '../features/auth/use-auth-bootstrap';
import { shouldProbeSession } from '../lib/session-probe';
import type { RouterContext } from '../router/router';

/**
 * SessionProbe — re-establishes the session from the refresh cookie for
 * routes outside `_authenticated` that change what they render based on
 * auth state (`/invite/$token` above all: invite links are opened from
 * mail and chat, i.e. in a browser context that has the cookie but no
 * in-memory session yet).
 *
 * Renders nothing. The bootstrap helper memoizes its in-flight promise
 * module-side, so mounting this alongside the `_authenticated` layout's
 * own call still results in exactly one refresh round-trip.
 */
function SessionProbe(): null {
  useAuthBootstrap();
  return null;
}

function NotFound(): ReactElement {
  return (
    <main
      style={{
        minBlockSize: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        fontFamily: 'var(--nf-font-sans)',
        background: 'var(--nf-color-bg)',
      }}
    >
      <NotFoundContent />
    </main>
  );
}

const TanStackRouterDevtools = import.meta.env.DEV
  ? lazy(() =>
      import('@tanstack/react-router-devtools').then((m) => ({
        default: m.TanStackRouterDevtools,
      })),
    )
  : null;

function FatalFallback({
  error,
  resetErrorBoundary,
}: {
  error: unknown;
  resetErrorBoundary: () => void;
}): ReactElement {
  const { t } = useTranslation(['common', 'errors']);
  let message: string;
  if (error instanceof ApiError && error.code) {
    message = t(error.code, { ns: 'errors', defaultValue: error.message });
  } else if (error instanceof Error) {
    message = error.message;
  } else {
    message = String(error);
  }
  // Subscribe to pathname inside the fallback so the router store forces
  // this component to re-render on navigation. `resetKeys` on the parent
  // ErrorBoundary is unreliable because the parent subtree is frozen
  // while the fallback is active, so the new keys never reach the
  // boundary. Resetting from a navigation-scoped effect runs outside the
  // frozen subtree and recovers the app on Link clicks.
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const errorPathnameRef = useRef(pathname);
  useEffect(() => {
    if (pathname !== errorPathnameRef.current) {
      resetErrorBoundary();
    }
  }, [pathname, resetErrorBoundary]);
  return (
    <main
      style={{
        minBlockSize: '100vh',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 'var(--nf-space-4)',
        padding: 'var(--nf-space-8)',
        fontFamily: 'var(--nf-font-sans)',
        background: 'var(--nf-color-bg)',
        color: 'var(--nf-color-fg)',
        textAlign: 'center',
      }}
    >
      <h1 style={{ fontFamily: 'var(--nf-font-display)', margin: 0 }}>{t('fatal.title')}</h1>
      {/* nf-token-override: component dimension, not a spacing step */}
      <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', maxInlineSize: '36rem' }}>
        {t('fatal.description')}
      </p>
      {message ? (
        <pre
          style={{
            margin: 0,
            padding: 'var(--nf-space-3) var(--nf-space-4)',
            borderRadius: 'var(--nf-radius-md)',
            background: 'var(--nf-color-surface)',
            color: 'var(--nf-color-fg-muted)',
            fontSize: 'var(--nf-text-supporting)',
            maxInlineSize: 'var(--nf-measure-content)',
            whiteSpace: 'pre-wrap',
            textAlign: 'start',
          }}
        >
          {message}
        </pre>
      ) : null}
      <div style={{ display: 'flex', gap: 'var(--nf-space-3)' }}>
        <button
          type="button"
          onClick={resetErrorBoundary}
          style={{
            padding: 'var(--nf-space-2) var(--nf-space-4)',
            borderRadius: 'var(--nf-radius-md)',
            border: '1px solid var(--nf-color-border)',
            background: 'var(--nf-color-surface)',
            color: 'var(--nf-color-fg)',
            cursor: 'pointer',
          }}
        >
          {t('fatal.retry')}
        </button>
        <Link
          to="/"
          style={{
            padding: 'var(--nf-space-2) var(--nf-space-4)',
            borderRadius: 'var(--nf-radius-md)',
            background: 'var(--nf-color-accent)',
            color: 'var(--nf-color-fg-on-accent)',
            textDecoration: 'none',
          }}
        >
          {t('fatal.back_home')}
        </Link>
      </div>
    </main>
  );
}

function RootLayout(): ReactElement {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  // The root ErrorBoundary resets when the user navigates. The reset is
  // driven from inside `FatalFallback` via `useRouterState` — the
  // subtree under the boundary is frozen while the fallback is active,
  // so `resetKeys` on this element never propagates the new pathname.
  return (
    <>
      {shouldProbeSession(pathname) ? <SessionProbe /> : null}
      <ErrorBoundary FallbackComponent={FatalFallback}>
        <Suspense fallback={<PageSkeleton sidebar />}>
          <Outlet />
        </Suspense>
      </ErrorBoundary>
      {TanStackRouterDevtools ? (
        <Suspense fallback={null}>
          <TanStackRouterDevtools />
        </Suspense>
      ) : null}
    </>
  );
}

export const Route = createRootRouteWithContext<RouterContext>()({
  component: RootLayout,
  notFoundComponent: NotFound,
});
