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
import { ErrorBoundary, type FallbackProps } from 'react-error-boundary';

import SettingsListSkeleton from '../features/settings/settings-list-skeleton';
import AccessRestricted from '../features/workspaces/access-restricted';
import { ApiError } from '../lib/api-error';

/**
 * Renders <AccessRestricted> for 401 / 403, rethrows everything else so
 * the root FatalFallback keeps its normal behaviour on unexpected errors.
 */
function SettingsErrorFallback({ error }: FallbackProps): ReactElement {
  if (error instanceof ApiError && (error.httpStatus === 401 || error.httpStatus === 403)) {
    return <AccessRestricted sectionTitleKey="nav.public_shares" />;
  }
  throw error;
}

function WorkspacePublicSharesLayout(): ReactElement {
  return (
    <ErrorBoundary FallbackComponent={SettingsErrorFallback}>
      <Suspense fallback={<SettingsListSkeleton />}>
        <Outlet />
      </Suspense>
    </ErrorBoundary>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/public-shares')({
  component: WorkspacePublicSharesLayout,
});
