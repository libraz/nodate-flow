import { Outlet, createRootRouteWithContext } from '@tanstack/react-router';
import { type ReactElement, Suspense, lazy } from 'react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import type { RouterContext } from '../router/router';

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
  const { t } = useTranslation('common');
  return (
    <>
      <a
        href="#main"
        style={{
          position: 'absolute',
          insetInlineStart: '-9999px',
        }}
      >
        {t('a11y.skipToContent')}
      </a>
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
});
