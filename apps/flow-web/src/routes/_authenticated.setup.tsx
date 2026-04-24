/**
 * /setup -- first-run onboarding for an authenticated user who does not
 * yet belong to a workspace. Creating a workspace drops the user into
 * /calendar. If the user already has a workspace we redirect here before
 * any component renders, so returning to /setup never shows the form
 * twice.
 *
 * Placed under `_authenticated.*` so the parent layout handles session
 * bootstrap and unauth redirects to accounts-web. The shell chrome
 * (sidebar/topbar) remains visible; that's intentional -- the user can
 * see where they'll land as soon as the workspace is created.
 */

import type { components } from '@nodate-flow/sdk';
import { createFileRoute, redirect } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import { workspacesKeys } from '../features/workspaces/api';
import SetupPage from '../features/workspaces/setup-page';
import { sdk } from '../lib/sdk';

type Workspace = components['schemas']['Workspace'];

/**
 * Prime the shared `workspacesKeys.list()` cache entry so later routes
 * that rely on `useWorkspacesQuery` hit warm data instead of double-
 * fetching. Mirrors the queryFn in `features/workspaces/api.ts` so the
 * cached shape stays compatible.
 */
async function fetchWorkspaceList(): Promise<Workspace[]> {
  const { data, error } = await sdk.GET('/workspaces', {});
  if (error || !data) return [];
  return data.workspaces ?? [];
}

function SetupRoute(): ReactElement {
  return <SetupPage />;
}

export const Route = createFileRoute('/_authenticated/setup')({
  beforeLoad: async ({ context }) => {
    const list = await context.queryClient.ensureQueryData({
      queryKey: workspacesKeys.list(),
      queryFn: fetchWorkspaceList,
    });
    if (list.length > 0) {
      throw redirect({ to: '/calendar', replace: true });
    }
  },
  component: SetupRoute,
});
