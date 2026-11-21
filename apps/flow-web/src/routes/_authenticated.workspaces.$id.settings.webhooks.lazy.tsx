/**
 * /workspaces/$id/settings/webhooks — workspace-scoped outbound webhooks
 * admin (lazy). The page lists subscriptions, lets workspace admins
 * create / delete / toggle them, send a synthetic test delivery, and
 * inspect the delivery history. See feature module for the full doc.
 */

import { createLazyFileRoute } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { ErrorBoundary, type FallbackProps } from 'react-error-boundary';

import SettingsListSkeleton from '../features/settings/settings-list-skeleton';
import WebhooksSettingsPage from '../features/webhooks/webhooks-settings-page';
import AccessRestricted from '../features/workspaces/access-restricted';
import { ApiError } from '../lib/api-error';

/**
 * Renders <AccessRestricted> for 401 / 403, rethrows everything else so
 * the root FatalFallback keeps its normal behaviour on unexpected errors.
 */
function SettingsErrorFallback({ error }: FallbackProps): ReactElement {
  if (error instanceof ApiError && (error.httpStatus === 401 || error.httpStatus === 403)) {
    return <AccessRestricted sectionTitleKey="nav.webhooks" />;
  }
  throw error;
}

function WebhooksRoute(): ReactElement {
  return (
    <ErrorBoundary FallbackComponent={SettingsErrorFallback}>
      <Suspense fallback={<SettingsListSkeleton />}>
        <WebhooksSettingsPage />
      </Suspense>
    </ErrorBoundary>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/webhooks')({
  component: WebhooksRoute,
});
