/**
 * Pathless layout route guarding all authenticated pages. While
 * bootstrap is in progress we render nothing. On unauthenticated state
 * the user is redirected to /login.
 */

import { Outlet, createFileRoute, useNavigate } from '@tanstack/react-router';
import { type ReactElement, useEffect } from 'react';

import { useAuthBootstrap } from '../hooks/use-auth-bootstrap';
import { selectIsAuthenticated, useAuth } from '../stores/auth-store';

function AuthenticatedLayout(): ReactElement | null {
  const { status } = useAuthBootstrap();
  const isAuthenticated = useAuth(selectIsAuthenticated);
  const navigate = useNavigate();

  useEffect(() => {
    if (status === 'unauthenticated' && !isAuthenticated) {
      void navigate({ to: '/login', replace: true });
    }
  }, [status, isAuthenticated, navigate]);

  if (status === 'loading') return null;
  if (!isAuthenticated) return null;

  return <Outlet />;
}

export const Route = createFileRoute('/_authenticated')({
  component: AuthenticatedLayout,
});
