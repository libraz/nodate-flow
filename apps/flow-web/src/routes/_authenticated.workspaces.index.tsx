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
        <div
          style={{
            padding: 'var(--nf-space-8)',
            display: 'flex',
            flexDirection: 'column',
            gap: 'var(--nf-space-4)',
          }}
        >
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
