/**
 * Pathless layout route guarding all authenticated pages. While
 * bootstrap is in progress we render nothing. On unauthenticated state
 * the user is redirected to /login.
 */

import { Link, Outlet, createFileRoute, useNavigate } from '@tanstack/react-router';
import { type ReactElement, useEffect } from 'react';

import { selectIsAuthenticated, selectUser, useAuth } from '../features/auth/auth-store';
import { useAuthBootstrap } from '../hooks/use-auth-bootstrap';

function AuthenticatedLayout(): ReactElement | null {
  const { status } = useAuthBootstrap();
  const isAuthenticated = useAuth(selectIsAuthenticated);
  const user = useAuth(selectUser);
  const navigate = useNavigate();

  useEffect(() => {
    if (status === 'unauthenticated' && !isAuthenticated) {
      void navigate({ to: '/login', replace: true });
    }
  }, [status, isAuthenticated, navigate]);

  if (status === 'loading') return null;
  if (!isAuthenticated) return null;

  return (
    <>
      {user?.isInstanceAdmin ? (
        <nav
          style={{
            position: 'fixed',
            top: 0,
            insetInlineEnd: 0,
            padding: 'var(--nf-space-2, 0.5rem) var(--nf-space-4, 1rem)',
            zIndex: 100,
          }}
        >
          <Link
            to="/admin/users"
            style={{
              fontSize: 'var(--nf-text-xs, 0.75rem)',
              color: 'var(--nf-color-fg-muted)',
              textDecoration: 'none',
            }}
          >
            Admin
          </Link>
        </nav>
      ) : null}
      <Outlet />
    </>
  );
}

export const Route = createFileRoute('/_authenticated')({
  component: AuthenticatedLayout,
});
