/**
 * /workspaces/$id/tasks/archived — Archive Room (lazy). Renders every
 * archived task in the workspace, grouped into editorial "chapter"
 * strata (this week / earlier this month / quarter / year / older).
 * The page mounts inside the route's Suspense + ErrorBoundary, so
 * data fetching errors and loading states are surfaced through the
 * root fallback UI; per-row mutation errors emit local toasts.
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import ArchivedTasksPage from '../features/tasks/archived/archived-tasks-page';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/tasks/archived');

function ArchivedRoute(): ReactElement {
  const { id } = routeApi.useParams();
  return <ArchivedTasksPage workspaceId={id} />;
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/tasks/archived')({
  component: ArchivedRoute,
});
