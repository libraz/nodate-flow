import { Navigate, createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useEffect } from 'react';

import { selectIsAuthenticated, useAuth } from '../features/auth/auth-store';
import { useAuthBootstrap } from '../features/auth/use-auth-bootstrap';
import { flowWebUrl } from '../lib/sdk';
import { selectWorkspaceId, useWorkspace } from '../stores/workspace-store';

export const Route = createFileRoute('/')({
  component: IndexRedirect,
});

/**
 * Authenticated users are handed off to flow-web, which owns the
 * unified /calendar UX. time-web keeps only the public share flow,
 * auth bridge, and workspace setup.
 */
function IndexRedirect(): ReactElement | null {
  const { status } = useAuthBootstrap();
  const isAuthenticated = useAuth(selectIsAuthenticated);
  const workspaceId = useWorkspace(selectWorkspaceId);

  useEffect(() => {
    if (status !== 'loading' && isAuthenticated && workspaceId) {
      window.location.replace(`${flowWebUrl}/calendar`);
    }
  }, [status, isAuthenticated, workspaceId]);

  if (status === 'loading') return null;
  if (!isAuthenticated) return <Navigate to="/login" />;
  if (!workspaceId) return <Navigate to="/setup" />;
  return null;
}
