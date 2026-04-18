/**
 * /workspaces — list view.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import WorkspaceList from '../features/workspaces/workspace-list';

function WorkspacesIndexRoute(): ReactElement {
  return (
    <Suspense
      fallback={
        <div style={{ padding: '2rem', display: 'flex', flexDirection: 'column', gap: '1rem' }}>
          <Skeleton style={{ blockSize: '2rem', inlineSize: '12rem' }} />
          <Skeleton style={{ blockSize: '16rem', inlineSize: '100%' }} />
        </div>
      }
    >
      <WorkspaceList />
    </Suspense>
  );
}

export const Route = createFileRoute('/_authenticated/workspaces/')({
  component: WorkspacesIndexRoute,
});
