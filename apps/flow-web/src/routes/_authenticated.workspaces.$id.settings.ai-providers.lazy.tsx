/**
 * /workspaces/$id/settings/ai-providers — workspace AI provider management (lazy).
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import ProviderList from '../features/ai-providers/provider-list';
import SettingsListSkeleton from '../features/settings/settings-list-skeleton';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/ai-providers');

function AiProvidersRoute(): ReactElement {
  const { id } = routeApi.useParams();
  return (
    <Suspense fallback={<SettingsListSkeleton />}>
      <ProviderList workspaceId={id} />
    </Suspense>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/ai-providers')({
  component: AiProvidersRoute,
});
