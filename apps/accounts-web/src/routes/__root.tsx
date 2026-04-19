import { Link, Outlet, createRootRouteWithContext } from '@tanstack/react-router';
import { type ReactElement, Suspense, lazy } from 'react';
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
        background: 'var(--color-bg)',
        color: 'var(--color-fg)',
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
      <p style={{ margin: 0, color: 'var(--color-muted)' }}>{t('notFound.description')}</p>
      <Link
        to="/login"
        style={{
          padding: '0.5rem 1rem',
          borderRadius: '0.5rem',
          background: 'var(--color-accent, #9b59b6)',
          color: 'var(--color-on-accent, #fff)',
          textDecoration: 'none',
        }}
      >
        {t('notFound.backToLogin')}
      </Link>
    </main>
  );
}

const TanStackRouterDevtools = import.meta.env.DEV
  ? lazy(() =>
      import('@tanstack/router-devtools').then((m) => ({
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
  const { t } = useTranslation('auth');
  const message = error instanceof Error ? error.message : String(error);
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
        background: 'var(--color-bg)',
        color: 'var(--color-fg)',
        textAlign: 'center',
      }}
    >
      <h1 style={{ fontFamily: 'var(--font-display)', margin: 0 }}>{t('fatal.title')}</h1>
      <p style={{ margin: 0, color: 'var(--color-muted)', maxInlineSize: '36rem' }}>
        {t('fatal.description')}
      </p>
      {message ? (
        <pre
          style={{
            margin: 0,
            padding: '0.75rem 1rem',
            borderRadius: '0.5rem',
            background: 'var(--color-surface, rgba(127,127,127,0.08))',
            color: 'var(--color-muted)',
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
            border: '1px solid var(--color-border)',
            background: 'var(--color-surface)',
            color: 'var(--color-fg)',
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
            background: 'var(--color-accent, #9b59b6)',
            color: 'var(--color-on-accent, #fff)',
            textDecoration: 'none',
          }}
        >
          {t('fatal.backToLogin')}
        </Link>
      </div>
    </main>
  );
}

function RootLayout(): ReactElement {
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
