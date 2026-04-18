/**
 * Workspaces section layout. Just renders an <Outlet /> so the section
 * has its own route namespace under the authenticated shell.
 */

import { Outlet, createFileRoute } from '@tanstack/react-router';
import type { ReactElement } from 'react';

function WorkspacesLayout(): ReactElement {
  return <Outlet />;
}

export const Route = createFileRoute('/_authenticated/workspaces')({
  component: WorkspacesLayout,
});
