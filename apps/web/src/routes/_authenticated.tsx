/**
 * Pathless layout route guarding all authenticated pages. Renders the
 * AppShell around its children. While bootstrap is in progress we render
 * nothing (Suspense fallback would also work). On unauthenticated state
 * the user is redirected to /login.
 */

import { Outlet, createFileRoute, useNavigate } from '@tanstack/react-router';
import { type ReactElement, useEffect } from 'react';

import AppShell from '../components/layout/app-shell';
import { selectIsAuthenticated, useAuth } from '../features/auth/auth-store';
import { useAuthBootstrap } from '../features/auth/use-auth-bootstrap';

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

  return (
    <AppShell>
      <Outlet />
    </AppShell>
  );
}

export const Route = createFileRoute('/_authenticated')({
  component: AuthenticatedLayout,
});
