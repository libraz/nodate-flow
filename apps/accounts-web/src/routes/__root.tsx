import { ApiError } from '@nodate-flow/sdk';
import { Link, Outlet, createRootRouteWithContext, useRouterState } from '@tanstack/react-router';
import { type ReactElement, Suspense, lazy, useEffect, useRef } from 'react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import type { RouterContext } from '../router/router';

function NotFound(): ReactElement {
  const { t } = useTranslation('auth');
  return (
    <main
      style={{
        minBlockSize: '100vh',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: '1rem',
        fontFamily: 'var(--font-body)',
        background: 'var(--nf-color-bg)',
        color: 'var(--nf-color-fg)',
      }}
    >
      <h1
        style={{
          fontFamily: 'var(--font-display)',
          fontSize: 'clamp(3rem, 8vw, 6rem)',
          margin: 0,
        }}
      >
        404
      </h1>
      <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>{t('not_found.description')}</p>
      <Link
        to="/login"
        style={{
          padding: '0.5rem 1rem',
          borderRadius: '0.5rem',
          background: 'var(--nf-color-accent)',
          color: 'var(--nf-color-fg-on-accent)',
          textDecoration: 'none',
        }}
      >
        {t('not_found.back_to_login')}
      </Link>
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

export function FatalFallback({
  error,
  resetErrorBoundary,
}: {
  error: unknown;
  resetErrorBoundary: () => void;
}): ReactElement {
  const { t } = useTranslation(['auth', 'errors']);
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
        gap: '1rem',
        padding: '2rem',
        fontFamily: 'var(--font-body)',
        background: 'var(--nf-color-bg)',
        color: 'var(--nf-color-fg)',
        textAlign: 'center',
      }}
    >
      <h1 style={{ fontFamily: 'var(--font-display)', margin: 0 }}>{t('fatal.title')}</h1>
      <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', maxInlineSize: '36rem' }}>
        {t('fatal.description')}
      </p>
      {message ? (
        <pre
          style={{
            margin: 0,
            padding: '0.75rem 1rem',
            borderRadius: '0.5rem',
            background: 'var(--nf-color-surface)',
            color: 'var(--nf-color-fg-muted)',
            fontSize: '0.8125rem',
            maxInlineSize: '48rem',
            whiteSpace: 'pre-wrap',
            textAlign: 'start',
          }}
        >
          {message}
        </pre>
      ) : null}
      <div style={{ display: 'flex', gap: '0.75rem' }}>
        <button
          type="button"
          onClick={resetErrorBoundary}
          style={{
            padding: '0.5rem 1rem',
            borderRadius: '0.5rem',
            border: '1px solid var(--nf-color-border)',
            background: 'var(--nf-color-surface)',
            color: 'var(--nf-color-fg)',
            cursor: 'pointer',
          }}
        >
          {t('fatal.retry')}
        </button>
        <Link
          to="/login"
          style={{
            padding: '0.5rem 1rem',
            borderRadius: '0.5rem',
            background: 'var(--nf-color-accent)',
            color: 'var(--nf-color-fg-on-accent)',
            textDecoration: 'none',
          }}
        >
          {t('fatal.back_to_login')}
        </Link>
      </div>
    </main>
  );
}

function RootLayout(): ReactElement {
  // The root ErrorBoundary resets when the user navigates. The reset is
  // driven from inside `FatalFallback` via `useRouterState` — the
  // subtree under the boundary is frozen while the fallback is active,
  // so `resetKeys` on this element never propagates the new pathname.
  return (
    <>
      <ErrorBoundary FallbackComponent={FatalFallback}>
        <Suspense fallback={null}>
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
