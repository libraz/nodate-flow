/**
 * /workspaces/$id/settings/public-shares — layout wrapper (lazy).
 *
 * Renders only an `<Outlet />` so the nested index (share list) and
 * `$shareId` (share detail) routes can mount. The previous implementation
 * rendered the list here directly, which left no slot for the detail child
 * and silently hid `/public-shares/$shareId`.
 */

import { createLazyFileRoute, Outlet } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import SettingsListSkeleton from '../features/settings/settings-list-skeleton';

function WorkspacePublicSharesLayout(): ReactElement {
  return (
    <Suspense fallback={<SettingsListSkeleton />}>
      <Outlet />
    </Suspense>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/public-shares')({
  component: WorkspacePublicSharesLayout,
});
