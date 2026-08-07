/**
 * Pathless layout route guarding all authenticated pages. While
 * bootstrap is in progress we render nothing. On unauthenticated state
 * the user is redirected to /login, carrying where they were headed so
 * an expired session does not turn a bookmark or an emailed link to
 * /security or /workspaces/... into a trip to /profile.
 */

import { createFileRoute, Link, Outlet, useLocation, useNavigate } from '@tanstack/react-router';
import { type ReactElement, useEffect, useRef } from 'react';
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
  const location = useLocation();
  // pathname + search + hash, without the origin — /login resolves it
  // against this app's own origin, so it stays a same-origin target.
  const returnTo = location.href;
  // The layout stays mounted while the bounce is in flight, and
  // `returnTo` follows the location, so without a latch the effect would
  // fire again with `/login?redirect=...` as the new destination and keep
  // wrapping itself.
  const bounced = useRef(false);

  useEffect(() => {
    if (status === 'unauthenticated' && !isAuthenticated && !bounced.current) {
      bounced.current = true;
      void navigate({ to: '/login', search: { redirect: returnTo }, replace: true });
    }
  }, [status, isAuthenticated, navigate, returnTo]);

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
