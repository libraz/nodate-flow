/**
 * /admin/users/$userId -- User detail with sessions, admin grant/revoke,
 * and suspend/enable actions.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { confirmAction } from '@nodate-flow/ui/primitives/confirm/action';
import { Link, createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useInvalidateInstanceStats } from '../../../features/admin-stats/api';
import { sdk } from '../../../lib/sdk';

interface UserDetail {
  id: string;
  email: string;
  displayName: string;
  enabled: boolean;
  isInstanceAdmin: boolean;
  workspaceCount: number;
  lastLoginAt: number | null;
  createdAt: number;
}

interface UserSession {
  id: string;
  userAgent: string;
  ipAddress: string;
  active: boolean;
  expiresAt: number;
  lastUsedAt: number | null;
  createdAt: number;
}

interface SessionsResponse {
  items: UserSession[];
  total: number;
}

const labelStyle: React.CSSProperties = {
  color: 'var(--nf-color-fg-muted)',
  fontSize: 'var(--nf-text-xs)',
  marginBlockEnd: 'var(--nf-space-1)',
};

const valueStyle: React.CSSProperties = {
  fontSize: 'var(--nf-text-sm)',
  marginBlockEnd: 'var(--nf-space-3)',
};

const badgeBase: React.CSSProperties = {
  display: 'inline-block',
  padding: '0.125rem 0.5rem',
  borderRadius: 'var(--nf-radius-pill)',
  fontSize: 'var(--nf-text-xs)',
  fontWeight: 500,
};

const thStyle: React.CSSProperties = {
  textAlign: 'start',
  padding: 'var(--nf-space-2) var(--nf-space-3)',
  borderBlockEnd: '2px solid var(--nf-color-border)',
  fontWeight: 600,
  color: 'var(--nf-color-fg-muted)',
  fontSize: 'var(--nf-text-sm)',
};

const tdStyle: React.CSSProperties = {
  padding: 'var(--nf-space-2) var(--nf-space-3)',
  borderBlockEnd: '1px solid var(--nf-color-border)',
  fontSize: 'var(--nf-text-sm)',
};

function formatTimestamp(ts: number | null | undefined, never: string): string {
  if (ts === null || ts === undefined || ts === 0) return never;
  return new Date(ts * 1000).toLocaleString();
}

function UserDetailPage(): ReactElement {
  const { userId } = Route.useParams();
  const { t } = useTranslation('admin');
  const invalidateInstanceStats = useInvalidateInstanceStats();
  const [user, setUser] = useState<UserDetail | null>(null);
  const [sessions, setSessions] = useState<UserSession[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [actionLoading, setActionLoading] = useState(false);

  useEffect(() => {
    setLoading(true);
    setError(null);

    void Promise.all([
      sdk.GET('/admin/users/{userId}', { params: { path: { userId } } }),
      sdk.GET('/admin/users/{userId}/sessions', { params: { path: { userId } } }),
    ]).then(([userResult, sessionsResult]) => {
      if (userResult.error || !userResult.data) {
        setError(t('errors.generic'));
        setLoading(false);
        return;
      }
      setUser(userResult.data as UserDetail);
      if (sessionsResult.data) {
        const sessBody = sessionsResult.data as SessionsResponse;
        setSessions(sessBody.items);
      }
      setLoading(false);
    });
  }, [userId, t]);

  const handleToggleEnabled = async () => {
    if (!user) return;
    const ok = await confirmAction({
      tone: 'danger',
      message: user.enabled ? t('users.confirm_suspend') : t('users.confirm_enable'),
      confirmLabel: user.enabled ? t('users.suspend') : t('users.enable'),
    });
    if (!ok) return;

    setActionLoading(true);
    await sdk.PATCH('/admin/users/{userId}', {
      params: { path: { userId } },
      body: { enabled: !user.enabled },
    });
    setActionLoading(false);

    // Refetch to get updated state
    const result = await sdk.GET('/admin/users/{userId}', { params: { path: { userId } } });
    if (result.data) {
      setUser(result.data as UserDetail);
      void invalidateInstanceStats();
    }
  };

  const handleToggleAdmin = async () => {
    if (!user) return;

    if (user.isInstanceAdmin) {
      const ok = await confirmAction({
        tone: 'danger',
        message: t('admins.confirm_revoke'),
        confirmLabel: t('admins.revoke'),
      });
      if (!ok) return;
      setActionLoading(true);
      await sdk.DELETE('/admin/instance-admins/{adminId}', {
        params: { path: { adminId: userId } },
      });
    } else {
      setActionLoading(true);
      await sdk.POST('/admin/instance-admins', {
        body: { userId: user.id },
      });
    }
    setActionLoading(false);

    // Refetch
    const result = await sdk.GET('/admin/users/{userId}', { params: { path: { userId } } });
    if (result.data) {
      setUser(result.data as UserDetail);
      void invalidateInstanceStats();
    }
  };

  const handleRevokeSession = async (sessionId: string) => {
    setActionLoading(true);
    const { error: err } = await sdk.DELETE('/admin/sessions/{sessionId}', {
      params: { path: { sessionId } },
    });
    setActionLoading(false);

    if (!err) {
      setSessions((prev) => prev.filter((s) => s.id !== sessionId));
    }
  };

  if (loading) {
    return <p style={{ color: 'var(--nf-color-fg-muted)' }}>{t('common.loading')}</p>;
  }

  if (error || !user) {
    return (
      <div>
        <Link to="/admin/users" style={{ color: 'var(--nf-color-accent)' }}>
          {t('common.back_to_users')}
        </Link>
        <p
          role="alert"
          style={{
            color: 'var(--nf-color-danger)',
            fontSize: 'var(--nf-text-sm)',
          }}
        >
          {error ?? t('errors.generic')}
        </p>
      </div>
    );
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-6)' }}>
      <div>
        <Link
          to="/admin/users"
          style={{
            color: 'var(--nf-color-accent)',
            fontSize: 'var(--nf-text-sm)',
          }}
        >
          {t('common.back_to_users')}
        </Link>
      </div>

      <h1
        style={{
          fontFamily: 'var(--nf-font-sans)',
          fontSize: 'var(--nf-text-2xl)',
          margin: 0,
        }}
      >
        {t('users.detail')}
      </h1>

      <div
        style={{
          padding: 'var(--nf-space-4)',
          border: '1px solid var(--nf-color-border)',
          borderRadius: 'var(--nf-radius-md)',
        }}
      >
        <div style={labelStyle}>{t('users.name')}</div>
        <div style={valueStyle}>{user.displayName}</div>

        <div style={labelStyle}>{t('users.email')}</div>
        <div style={valueStyle}>{user.email}</div>

        <div style={labelStyle}>{t('users.status')}</div>
        <div style={valueStyle}>
          <span
            style={{
              ...badgeBase,
              background: user.enabled
                ? 'color-mix(in srgb, var(--nf-color-success) 15%, transparent)'
                : 'color-mix(in srgb, var(--nf-color-danger) 15%, transparent)',
              color: user.enabled ? 'var(--nf-color-success)' : 'var(--nf-color-danger)',
            }}
          >
            {user.enabled ? t('users.enabled') : t('users.disabled')}
          </span>
        </div>

        <div style={labelStyle}>{t('users.admin')}</div>
        <div style={valueStyle}>{user.isInstanceAdmin ? t('common.yes') : t('common.no')}</div>

        <div style={labelStyle}>{t('users.workspaces')}</div>
        <div style={valueStyle}>{user.workspaceCount}</div>

        <div style={labelStyle}>{t('users.last_login')}</div>
        <div style={valueStyle}>{formatTimestamp(user.lastLoginAt, t('common.never'))}</div>

        <div style={labelStyle}>{t('users.created_at')}</div>
        <div style={valueStyle}>{formatTimestamp(user.createdAt, t('common.never'))}</div>
      </div>

      <div style={{ display: 'flex', gap: 'var(--nf-space-3)' }}>
        <Button
          variant="default"
          disabled={actionLoading}
          onClick={() => void handleToggleEnabled()}
        >
          {user.enabled ? t('users.suspend') : t('users.enable')}
        </Button>
        <Button variant="default" disabled={actionLoading} onClick={() => void handleToggleAdmin()}>
          {user.isInstanceAdmin ? t('users.revoke_admin') : t('users.grant_admin')}
        </Button>
      </div>

      <h2
        style={{
          fontFamily: 'var(--nf-font-sans)',
          fontSize: 'var(--nf-text-lg)',
          margin: 0,
        }}
      >
        {t('users.sessions')}
      </h2>

      {sessions.length === 0 ? (
        <p
          style={{
            color: 'var(--nf-color-fg-muted)',
            fontSize: 'var(--nf-text-sm)',
          }}
        >
          {t('users.no_results')}
        </p>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse' }}>
            <thead>
              <tr>
                <th style={thStyle}>{t('users.user_agent')}</th>
                <th style={thStyle}>IP</th>
                <th style={thStyle}>{t('users.created_at')}</th>
                <th style={thStyle} />
              </tr>
            </thead>
            <tbody>
              {sessions.map((s) => (
                <tr key={s.id}>
                  <td style={tdStyle}>{s.userAgent}</td>
                  <td style={tdStyle}>{s.ipAddress}</td>
                  <td style={tdStyle}>{formatTimestamp(s.createdAt, t('common.never'))}</td>
                  <td style={tdStyle}>
                    {s.active ? (
                      <Button
                        variant="danger"
                        disabled={actionLoading}
                        onClick={() => void handleRevokeSession(s.id)}
                      >
                        {t('admins.revoke')}
                      </Button>
                    ) : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

export const Route = createFileRoute('/_authenticated/admin/users_/$userId')({
  component: UserDetailPage,
});
