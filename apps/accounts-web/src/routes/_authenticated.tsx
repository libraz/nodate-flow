/**
 * Pathless layout route guarding all authenticated pages. While
 * bootstrap is in progress we render nothing. On unauthenticated state
 * the user is redirected to /login, carrying where they were headed so
 * an expired session does not turn a bookmark or an emailed link to
 * /security or /workspaces/... into a trip to /profile. Every signed-in
 * page renders inside the account shell.
 */

import PageSkeleton from '@nodate-flow/ui/primitives/page-skeleton';
import { createFileRoute, Outlet, useLocation, useNavigate } from '@tanstack/react-router';
import { type ReactElement, useEffect, useRef } from 'react';

import AccountShell from '../components/account-shell';
import { selectIsAuthenticated, selectUser, useAuth } from '../features/auth/auth-store';
import { useAuthBootstrap } from '../hooks/use-auth-bootstrap';

import styles from './_authenticated.module.css';

export function AuthenticatedLayout(): ReactElement | null {
  const { status } = useAuthBootstrap();
  const isAuthenticated = useAuth(selectIsAuthenticated);
  const user = useAuth(selectUser);
  const navigate = useNavigate();
  const location = useLocation();
  const isAdmin = location.pathname.startsWith('/admin');
  const pageKey = isAdmin ? '/admin' : location.pathname;
  // pathname + search + hash including the basepath, without the origin:
  // /login hands it to window.location, which knows nothing of the router
  // basepath, and resolves it against this app's own origin.
  const returnTo = location.publicHref;
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
  // A token without a hydrated user: hold the shell's place until /me lands.
  if (!user) return <PageSkeleton sidebar />;

  return (
    <AccountShell user={user} wide={isAdmin}>
      {/* Keyed so each page fades in as it replaces the last; the admin shell keeps one key and fades its own pages. */}
      <div key={pageKey} className={styles.page}>
        <Outlet />
      </div>
    </AccountShell>
  );
}

export const Route = createFileRoute('/_authenticated')({
  component: AuthenticatedLayout,
});
