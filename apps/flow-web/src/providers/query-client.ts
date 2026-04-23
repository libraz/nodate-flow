/**
 * Re-export the shared query client from @nodate-flow/sdk and wire the
 * flow-web-specific cleanup that runs on a terminal 401 from any query.
 *
 * A terminal 401 means the SDK refresh middleware already attempted a
 * token refresh and failed, or the failing request bypassed the SDK
 * client entirely (e.g. the notifications `fetchJson` poll). In either
 * case the session is dead; we clear the auth store, drop the persisted
 * active-workspace id (so the next login does not resurrect the prior
 * user's workspace as a sidebar shortcut), and redirect to /login.
 *
 * The route guard `_authenticated.tsx` would also bounce the user to
 * /login once `accessToken` becomes null, but a hard redirect is the
 * safer terminal action because it stops all in-flight polls (SSE,
 * reminders, unread-count) in a single shot.
 */

import { authStore, setAuthErrorHandler } from '@nodate-flow/sdk';

import { clearActiveWorkspaceId } from '../lib/use-current-workspace';

export { createQueryClient, queryClient } from '@nodate-flow/sdk';

setAuthErrorHandler(() => {
  authStore.getState().clearSession();
  clearActiveWorkspaceId();
  // Use replace so the dead page is not in history; the /login route
  // in flow-web itself redirects to accounts-web with the current
  // origin as `?redirect=...`, so the user lands back here after login.
  if (typeof window !== 'undefined') {
    window.location.replace('/login');
  }
});
