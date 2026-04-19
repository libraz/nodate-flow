/**
 * Admin layout route. Gates access to instance admin pages.
 * Redirects non-admins to /profile.
 */

import { Link, Outlet, createFileRoute, useNavigate } from '@tanstack/react-router';
import { type ReactElement, useEffect } from 'react';
import { useTranslation } from 'react-i18next';

import { selectUser, useAuth } from '../../features/auth/auth-store';

function AdminLayout(): ReactElement | null {
  const user = useAuth(selectUser);
  const navigate = useNavigate();
  const { t } = useTranslation('admin');

  useEffect(() => {
    if (user && !user.isInstanceAdmin) {
      void navigate({ to: '/profile', replace: true });
    }
  }, [user, navigate]);

  if (!user?.isInstanceAdmin) return null;

  const navLinkStyle = {
    display: 'block',
    padding: 'var(--nf-space-2, 0.5rem) var(--nf-space-3, 0.75rem)',
    color: 'var(--nf-color-fg)',
    textDecoration: 'none',
    fontSize: 'var(--nf-text-sm, 0.875rem)',
    borderRadius: 'var(--nf-radius-md, 0.375rem)',
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
          padding: 'var(--nf-space-4, 1rem)',
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-1, 0.25rem)',
        }}
      >
        <h2
          style={{
            fontFamily: 'var(--nf-font-display, var(--font-display))',
            fontSize: 'var(--nf-text-lg, 1.125rem)',
            margin: '0 0 var(--nf-space-4, 1rem) 0',
            padding: '0 var(--nf-space-3, 0.75rem)',
          }}
        >
          Admin
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
        <Link to="/admin/settings" style={navLinkStyle}>
          {t('nav.settings')}
        </Link>
        <div style={{ flex: 1 }} />
        <Link
          to="/profile"
          style={{
            ...navLinkStyle,
            color: 'var(--nf-color-fg-muted)',
            fontSize: 'var(--nf-text-xs, 0.75rem)',
          }}
        >
          {t('common.back')}
        </Link>
      </aside>
      <main
        style={{
          flex: 1,
          padding: 'var(--nf-space-8, 2rem)',
          maxWidth: '1200px',
        }}
      >
        <Outlet />
      </main>
    </div>
  );
}

export const Route = createFileRoute('/_authenticated/admin')({
  component: AdminLayout,
});
