import { Outlet, createRootRouteWithContext } from '@tanstack/react-router';
import { type ReactElement, Suspense, lazy } from 'react';

import ThemeInitializer from '../components/theme-initializer';
import type { RouterContext } from '../router/router';

const TanStackRouterDevtools = import.meta.env.DEV
  ? lazy(() =>
      import('@tanstack/router-devtools').then((m) => ({
        default: m.TanStackRouterDevtools,
      })),
    )
  : null;

function RootLayout(): ReactElement {
  return (
    <div className="app-bg flex h-full min-h-screen flex-col">
      <ThemeInitializer />
      <Suspense fallback={null}>
        <div className="flex flex-1 flex-col">
          <Outlet />
        </div>
      </Suspense>
      {TanStackRouterDevtools ? (
        <Suspense fallback={null}>
          <TanStackRouterDevtools />
        </Suspense>
      ) : null}
    </div>
  );
}

export const Route = createRootRouteWithContext<RouterContext>()({
  component: RootLayout,
});
