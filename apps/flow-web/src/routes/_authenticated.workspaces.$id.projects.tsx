/**
 * /workspaces/$id/projects — section layout. Renders an <Outlet />.
 */

import { createFileRoute, Outlet } from '@tanstack/react-router';
import type { ReactElement } from 'react';

function WorkspaceProjectsLayout(): ReactElement {
  return <Outlet />;
}

export const Route = createFileRoute('/_authenticated/workspaces/$id/projects')({
  component: WorkspaceProjectsLayout,
});
