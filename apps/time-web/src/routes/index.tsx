import { Navigate, createFileRoute } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import { selectIsAuthenticated, useAuth } from '../stores/auth-store';
import { selectWorkspaceId, useWorkspace } from '../stores/workspace-store';

export const Route = createFileRoute('/')({
  component: IndexRedirect,
});

function IndexRedirect(): ReactElement {
  const isAuthenticated = useAuth(selectIsAuthenticated);
  const workspaceId = useWorkspace(selectWorkspaceId);

  if (!isAuthenticated) return <Navigate to="/login" />;
  if (!workspaceId) return <Navigate to="/setup" />;
  return <Navigate to="/calendar" />;
}
