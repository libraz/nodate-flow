/**
 * /workspaces/$id/settings/general — edit basic workspace metadata (lazy).
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';

import WorkspaceSettingsForm, {
  WorkspaceSettingsSkeleton,
} from '../features/workspaces/workspace-settings-form';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/general');

function WorkspaceGeneralRoute(): ReactElement {
  const { id } = routeApi.useParams();
  return (
    <Suspense fallback={<WorkspaceSettingsSkeleton />}>
      <WorkspaceSettingsForm workspaceId={id} />
    </Suspense>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/general')({
  component: WorkspaceGeneralRoute,
});
