/**
 * Admin layout route. Gates access to instance admin pages.
 *
 * The guard renders three states:
 *   1. user undefined: a centered loading skeleton (auth bootstrap not done).
 *   2. user known + non-admin: emit a one-shot redirect to /profile, render null.
 *   3. user known + admin: render the admin shell.
 *
 * Previous implementation flashed `null` for one frame while the auth state
 * was loading, which produced a brief blank page on direct /admin loads.
 */

import Spinner from '@nodate-flow/ui/primitives/spinner';
import { Link, Outlet, createFileRoute, useNavigate } from '@tanstack/react-router';
import { type ReactElement, useEffect } from 'react';
import { useTranslation } from 'react-i18next';

import { selectUser, useAuth } from '../../features/auth/auth-store';
import styles from './admin.module.css';

export function AdminLayout(): ReactElement | null {
  const user = useAuth(selectUser);
  const navigate = useNavigate();
  const { t } = useTranslation('admin');

  // _authenticated guards on session; `user === null` here means the /me
  // hydrate has not landed yet, so we render a loading skeleton instead of
  // returning null and flashing a blank page.
  const userResolved = user !== null;
  const isAdmin = userResolved && user.isInstanceAdmin === true;

  useEffect(() => {
    if (userResolved && !isAdmin) {
      void navigate({ to: '/profile', replace: true });
    }
  }, [userResolved, isAdmin, navigate]);

  if (!userResolved) {
    return (
      <div data-testid="admin-guard-loading" style={loadingStyle}>
        <Spinner label={t('common.loading')} size="md" />
      </div>
    );
  }

  if (!isAdmin) return null;

  return (
    <div className={styles.shell}>
      <aside className={styles.aside}>
        <h2 className={styles.title}>{t('title')}</h2>
        <Link
          to="/admin/users"
          className={styles.navLink}
          activeProps={{ className: `${styles.navLink} ${styles.navLinkActive}` }}
        >
          {t('nav.users')}
        </Link>
        <Link
          to="/admin/workspaces"
          className={styles.navLink}
          activeProps={{ className: `${styles.navLink} ${styles.navLinkActive}` }}
        >
          {t('nav.workspaces')}
        </Link>
        <Link
          to="/admin/audit-logs"
          className={styles.navLink}
          activeProps={{ className: `${styles.navLink} ${styles.navLinkActive}` }}
        >
          {t('nav.audit_logs')}
        </Link>
        <Link
          to="/admin/admins"
          className={styles.navLink}
          activeProps={{ className: `${styles.navLink} ${styles.navLinkActive}` }}
        >
          {t('nav.admins')}
        </Link>
        <Link
          to="/admin/stats"
          className={styles.navLink}
          activeProps={{ className: `${styles.navLink} ${styles.navLinkActive}` }}
        >
          {t('nav.stats')}
        </Link>
        <Link
          to="/admin/settings"
          className={styles.navLink}
          activeProps={{ className: `${styles.navLink} ${styles.navLinkActive}` }}
        >
          {t('nav.settings')}
        </Link>
        <div className={styles.spacer} />
        <Link to="/profile" className={styles.backLink}>
          {t('common.back_to_profile')}
        </Link>
      </aside>
      <main className={styles.main}>
        <Outlet />
      </main>
    </div>
  );
}

const loadingStyle = {
  minHeight: '100vh',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
} as const;

export const Route = createFileRoute('/_authenticated/admin')({
  component: AdminLayout,
});
