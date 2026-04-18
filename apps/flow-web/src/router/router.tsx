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
});

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
