import PageSkeleton from '@nodate-flow/ui/primitives/page-skeleton';
import type { QueryClient } from '@tanstack/react-query';
import { createRouter } from '@tanstack/react-router';
import type { i18n as I18nInstance } from 'i18next';

import { i18n } from '../i18n';
import { queryClient } from '../providers/query-client';
import { routeTree } from '../routeTree.gen';

/** Router context shared with every route/loader. */
export interface RouterContext {
  queryClient: QueryClient;
  i18n: I18nInstance;
}

/** Singleton app router instance. */
export const router = createRouter({
  routeTree,
  context: { queryClient, i18n } satisfies RouterContext,
  defaultPreload: 'intent',
  // Loads under 200ms never show the skeleton; once shown it stays long enough not to flicker.
  defaultPendingComponent: PageSkeleton,
  defaultPendingMs: 200,
  defaultPendingMinMs: 300,
});

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
