/**
 * /workspaces/$id/tasks/drafts — Retro drafts queue (lazy).
 *
 * Surfaces every retrospective task drafted by the signal_judge
 * Applier. Mounts inside the route's Suspense +
 * ErrorBoundary, so the underlying suspense query's loading state
 * and any fetch error flow through the route fallback UI; per-row
 * mutation errors emit local toasts.
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import RetroDraftsPage from '../features/tasks/retro-drafts/retro-drafts-page';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/tasks/drafts');

function RetroDraftsRoute(): ReactElement {
  const { id } = routeApi.useParams();
  return <RetroDraftsPage workspaceId={id} />;
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/tasks/drafts')({
  component: RetroDraftsRoute,
});
