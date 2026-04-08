import { Link, Outlet, createRootRouteWithContext } from '@tanstack/react-router';
import { type ReactElement, Suspense, lazy } from 'react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import type { RouterContext } from '../router/router';

function NotFound(): ReactElement {
  const { t } = useTranslation('common');
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
      }}
    >
      <h1 style={{ fontFamily: 'var(--font-display)', margin: 0 }}>{t('not_found.title')}</h1>
      <p style={{ margin: 0, color: 'var(--color-muted)' }}>{t('not_found.description')}</p>
      <Link to="/" style={{ color: 'var(--color-fg)' }}>
        {t('not_found.back_home')}
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

function FatalFallback(): ReactElement {
  return (
    <main style={{ padding: '2rem', fontFamily: 'var(--font-body)' }}>
      <p>Something went wrong.</p>
    </main>
  );
}

function RootLayout(): ReactElement {
  return (
    <>
      <ErrorBoundary fallback={<FatalFallback />}>
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
