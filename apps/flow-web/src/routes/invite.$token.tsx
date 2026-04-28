/**
 * /invite/$token — public route stub for accepting workspace invite links.
 *
 * The lazy component below uses `useSuspenseQuery(useInviteInfoQuery)` to
 * fetch invite info. Without a `beforeLoad` prefetch the token-not-found
 * error throws inside Suspense and bubbles to the route's error boundary
 * after the component has already mounted, briefly flashing AuthCard
 * chrome before swapping to the error UI.
 *
 * Prefetching here lands the query result (or its error) in the cache
 * before the lazy component mounts, so the suspense boundary resolves
 * synchronously and the error path renders without a chrome flash.
 *
 * The `.catch()` is intentional: we want the error to flow through to
 * the lazy component's own ErrorBoundary, not to abort `beforeLoad`
 * (which would surface the route-level fallback instead of the branded
 * one).
 */

import { createFileRoute } from '@tanstack/react-router';

import { inviteInfoQueryOptions } from '../features/workspaces/invite-api';

export const Route = createFileRoute('/invite/$token')({
  beforeLoad: async ({ params, context: { queryClient } }) => {
    await queryClient.ensureQueryData(inviteInfoQueryOptions(params.token)).catch(() => {
      // Swallow: the lazy component re-runs the same query via
      // useSuspenseQuery and renders its branded error UI.
    });
  },
});
