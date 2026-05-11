/**
 * /admin/users/$userId -- User detail with sessions, admin grant/revoke,
 * and suspend/enable actions.
 */

import type { components } from '@nodate-flow/sdk';
import VisuallyHidden from '@nodate-flow/ui/a11y/visually-hidden';
import Button from '@nodate-flow/ui/primitives/button';
import { confirmAction } from '@nodate-flow/ui/primitives/confirm/action';
import { Link, createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useInvalidateInstanceStats } from '../../../features/admin-stats/api';
import {
  adminBadgeBase,
  adminLabelStyle,
  adminTdStyle,
  adminThStyle,
  adminValueStyle,
} from '../../../features/admin/styles';
import { formatTimestamp } from '../../../lib/format-timestamp';
import { sdk } from '../../../lib/sdk';
import { useSubmitGuard } from '../../../lib/use-submit-guard';

/**
 * SDK-derived shapes; mirror the auth-api Go schema verbatim so a server-side
 * field rename surfaces as a typecheck failure instead of a silent undefined.
 */
type UserDetail = components['schemas']['User'];
type UserSession = components['schemas']['Session'];
type SessionsResponse = components['schemas']['ListUserSessionsOutputBody'];

/**
 * Renders the user detail content. Exported so unit tests can mount the
 * page without a real router; the production route still wires it via
 * `createFileRoute(...)({ component: UserDetailPage })` below.
 */
export function UserDetailPage(): ReactElement {
  const { userId } = Route.useParams();
  const { t } = useTranslation('admin');
  const invalidateInstanceStats = useInvalidateInstanceStats();
  const [user, setUser] = useState<UserDetail | null>(null);
  const [sessions, setSessions] = useState<UserSession[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // Each destructive action gets its own guard so a slow suspend cannot
  // race with an admin grant / session revoke. The previous shared boolean
  // disabled all buttons during any in-flight call but did not prevent the
  // second handler from firing if the user clicked it during the same tick
  // before React re-rendered. Per-action guards close that race and let
  // unrelated buttons (e.g. revoke session N vs revoke session M) keep
  // operating independently.
  const enabledGuard = useSubmitGuard();
  const adminGuard = useSubmitGuard();
  const sessionGuard = useSubmitGuard();

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
        setSessions(sessBody.items ?? []);
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
    if (enabledGuard.guard()) return;
    try {
      await sdk.PATCH('/admin/users/{userId}', {
        params: { path: { userId } },
        body: { enabled: !user.enabled },
      });
      // Refetch to get updated state
      const result = await sdk.GET('/admin/users/{userId}', { params: { path: { userId } } });
      if (result.data) {
        setUser(result.data as UserDetail);
        void invalidateInstanceStats();
      }
    } finally {
      enabledGuard.end();
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
      if (adminGuard.guard()) return;
      try {
        await sdk.DELETE('/admin/instance-admins/{adminId}', {
          params: { path: { adminId: userId } },
        });
      } finally {
        adminGuard.end();
      }
    } else {
      if (adminGuard.guard()) return;
      try {
        await sdk.POST('/admin/instance-admins', {
          body: { userId: user.id },
        });
      } finally {
        adminGuard.end();
      }
    }

    // Refetch
    const result = await sdk.GET('/admin/users/{userId}', { params: { path: { userId } } });
    if (result.data) {
      setUser(result.data as UserDetail);
      void invalidateInstanceStats();
    }
  };

  const handleRevokeSession = async (sessionId: string) => {
    if (sessionGuard.guard()) return;
    try {
      const { error: err } = await sdk.DELETE('/admin/sessions/{sessionId}', {
        params: { path: { sessionId } },
      });
      if (!err) {
        setSessions((prev) => prev.filter((s) => s.id !== sessionId));
      }
    } finally {
      sessionGuard.end();
    }
  };

  if (loading) {
    return <p style={{ color: 'var(--nf-color-fg-muted)' }}>{t('common.loading')}</p>;
  }

  if (error || !user) {
    return (
      <div>
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
        <Link to="/admin/users" className="aw-link aw-text-sm">
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
        <div style={adminLabelStyle}>{t('users.name')}</div>
        <div style={adminValueStyle}>{user.displayName}</div>

        <div style={adminLabelStyle}>{t('users.email')}</div>
        <div style={adminValueStyle}>{user.email}</div>

        <div style={adminLabelStyle}>{t('users.status')}</div>
        <div style={adminValueStyle}>
          <span
            style={{
              ...adminBadgeBase,
              background: user.enabled
                ? 'color-mix(in srgb, var(--nf-color-success) 15%, transparent)'
                : 'color-mix(in srgb, var(--nf-color-danger) 15%, transparent)',
              color: user.enabled ? 'var(--nf-color-success)' : 'var(--nf-color-danger)',
            }}
          >
            {user.enabled ? t('users.enabled') : t('users.disabled')}
          </span>
        </div>

        <div style={adminLabelStyle}>{t('users.admin')}</div>
        <div style={adminValueStyle}>{user.isInstanceAdmin ? t('common.yes') : t('common.no')}</div>

        <div style={adminLabelStyle}>{t('users.workspaces')}</div>
        <div style={adminValueStyle}>{user.workspaceCount}</div>

        <div style={adminLabelStyle}>{t('users.last_login')}</div>
        <div style={adminValueStyle}>{formatTimestamp(user.lastLoginAt, t('common.never'))}</div>

        <div style={adminLabelStyle}>{t('users.created_at')}</div>
        <div style={adminValueStyle}>{formatTimestamp(user.createdAt, t('common.never'))}</div>
      </div>

      <div style={{ display: 'flex', gap: 'var(--nf-space-3)' }}>
        <Button
          variant="default"
          disabled={enabledGuard.submitting}
          onClick={() => void handleToggleEnabled()}
        >
          {user.enabled ? t('users.suspend') : t('users.enable')}
        </Button>
        <Button
          variant="default"
          disabled={adminGuard.submitting}
          onClick={() => void handleToggleAdmin()}
        >
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
                <th style={adminThStyle}>{t('users.user_agent')}</th>
                <th style={adminThStyle}>{t('users.ip_address')}</th>
                <th style={adminThStyle}>{t('users.created_at')}</th>
                <th style={adminThStyle}>
                  <VisuallyHidden>{t('users.session_actions')}</VisuallyHidden>
                </th>
              </tr>
            </thead>
            <tbody>
              {sessions.map((s) => (
                <tr key={s.id}>
                  <td style={adminTdStyle}>{s.userAgent}</td>
                  <td style={adminTdStyle}>{s.ipAddress}</td>
                  <td style={adminTdStyle}>{formatTimestamp(s.createdAt, t('common.never'))}</td>
                  <td style={adminTdStyle}>
                    {s.active ? (
                      <Button
                        variant="danger"
                        disabled={sessionGuard.submitting}
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
