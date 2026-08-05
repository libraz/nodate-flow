/**
 * /workspaces/$id/settings/public-shares — layout wrapper (lazy).
 *
 * Renders only an `<Outlet />` so the nested index (share list) and
 * `$shareId` (share detail) routes can mount. The previous implementation
 * rendered the list here directly, which left no slot for the detail child
 * and silently hid `/public-shares/$shareId`.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute, Outlet } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

function WorkspacePublicSharesLayout(): ReactElement {
  return (
    <Suspense
      fallback={
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-3)' }}>
          <Skeleton style={{ blockSize: '2rem', inlineSize: '16rem' }} />
          <Skeleton style={{ blockSize: '8rem', inlineSize: '100%' }} />
        </div>
      }
    >
      <Outlet />
    </Suspense>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/public-shares')({
  component: WorkspacePublicSharesLayout,
});
