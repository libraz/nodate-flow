import { Navigate, createFileRoute } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import { useAuthStore } from '../stores/auth-store';
import { useWorkspaceStore } from '../stores/workspace-store';

export const Route = createFileRoute('/')({
  component: IndexRedirect,
});

function IndexRedirect(): ReactElement {
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated);
  const workspaceId = useWorkspaceStore((s) => s.workspaceId);

  if (!isAuthenticated) return <Navigate to="/login" />;
  if (!workspaceId) return <Navigate to="/setup" />;
  return <Navigate to="/calendar" />;
}
