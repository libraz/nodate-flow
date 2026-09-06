import { ApiError, NetworkError } from '@nodate-flow/sdk';
import { createRootRouteWithContext, Link, Outlet, useRouterState } from '@tanstack/react-router';
import { lazy, type ReactElement, Suspense, useEffect, useRef } from 'react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import { mapAuthThrown } from '../lib/auth-errors';
import type { RouterContext } from '../router/router';
import styles from './__root.module.css';

function NotFound(): ReactElement {
  const { t } = useTranslation('auth');
  return (
    <main className={styles.shell}>
      <h1 className={styles.notFoundHeading}>404</h1>
      <p className={styles.subtle}>{t('not_found.description')}</p>
      <Link to="/login" className={styles.linkButton}>
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
  } else if (error instanceof NetworkError || error instanceof TypeError) {
    // A transport failure carries the browser's own English wording
    // ("Failed to fetch"), which is not a sentence to paint into the page
    // of a reader who chose another language.
    message = t(mapAuthThrown(error));
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
    <main className={styles.shell}>
      <h1 className={styles.fatalHeading}>{t('fatal.title')}</h1>
      <p className={styles.subtle}>{t('fatal.description')}</p>
      {message ? <pre className={styles.errorBlock}>{message}</pre> : null}
      <div className={styles.actionRow}>
        <button type="button" onClick={resetErrorBoundary} className={styles.ghostButton}>
          {t('fatal.retry')}
        </button>
        <Link to="/login" className={styles.linkButton}>
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
