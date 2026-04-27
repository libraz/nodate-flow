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

  const navLinkStyle = {
    display: 'block',
    padding: 'var(--nf-space-2) var(--nf-space-3)',
    color: 'var(--nf-color-fg)',
    textDecoration: 'none',
    fontSize: 'var(--nf-text-sm)',
    borderRadius: 'var(--nf-radius-md)',
  };

  return (
    <div
      style={{
        display: 'flex',
        minHeight: '100vh',
        background: 'var(--nf-color-bg)',
      }}
    >
      <aside
        style={{
          width: '220px',
          borderInlineEnd: '1px solid var(--nf-color-border)',
          padding: 'var(--nf-space-4)',
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-1)',
        }}
      >
        <h2
          style={{
            fontFamily: 'var(--nf-font-sans)',
            fontSize: 'var(--nf-text-lg)',
            margin: '0 0 var(--nf-space-4) 0',
            padding: '0 var(--nf-space-3)',
          }}
        >
          {t('title')}
        </h2>
        <Link to="/admin/users" style={navLinkStyle}>
          {t('nav.users')}
        </Link>
        <Link to="/admin/workspaces" style={navLinkStyle}>
          {t('nav.workspaces')}
        </Link>
        <Link to="/admin/audit-logs" style={navLinkStyle}>
          {t('nav.audit_logs')}
        </Link>
        <Link to="/admin/admins" style={navLinkStyle}>
          {t('nav.admins')}
        </Link>
        <Link to="/admin/stats" style={navLinkStyle}>
          {t('nav.stats')}
        </Link>
        <Link to="/admin/settings" style={navLinkStyle}>
          {t('nav.settings')}
        </Link>
        <div style={{ flex: 1 }} />
        <Link
          to="/profile"
          style={{
            ...navLinkStyle,
            color: 'var(--nf-color-fg-muted)',
            fontSize: 'var(--nf-text-xs)',
          }}
        >
          {t('common.back_to_profile')}
        </Link>
      </aside>
      <main
        style={{
          flex: 1,
          padding: 'var(--nf-space-8)',
          maxWidth: '1200px',
        }}
      >
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
