/**
 * /workspaces — list view.
 */

import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import ListPageSkeleton from '../features/workspaces/list-page-skeleton';
import WorkspaceList from '../features/workspaces/workspace-list';

function WorkspacesIndexRoute(): ReactElement {
  return (
    <Suspense fallback={<ListPageSkeleton />}>
      <WorkspaceList />
    </Suspense>
  );
}

export const Route = createFileRoute('/_authenticated/workspaces/')({
  component: WorkspacesIndexRoute,
});
