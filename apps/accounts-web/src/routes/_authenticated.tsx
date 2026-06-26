/**
 * Pathless layout route guarding all authenticated pages. While
 * bootstrap is in progress we render nothing. On unauthenticated state
 * the user is redirected to /login.
 */

import { createFileRoute, Link, Outlet, useNavigate } from '@tanstack/react-router';
import { type ReactElement, useEffect } from 'react';
import { useTranslation } from 'react-i18next';

import { selectIsAuthenticated, selectUser, useAuth } from '../features/auth/auth-store';
import { useAuthBootstrap } from '../hooks/use-auth-bootstrap';

import styles from './_authenticated.module.css';

export function AuthenticatedLayout(): ReactElement | null {
  const { t } = useTranslation('admin');
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
        <nav className={styles.adminNav}>
          <Link to="/admin/users" className={styles.adminLink}>
            {t('title')}
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
