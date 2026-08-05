/**
 * /admin/users/$userId -- User detail with sessions, admin grant/revoke,
 * and suspend/enable actions.
 */

import type { components } from '@nodate-flow/sdk';
import VisuallyHidden from '@nodate-flow/ui/a11y/visually-hidden';
import Button from '@nodate-flow/ui/primitives/button';
import { confirmAction } from '@nodate-flow/ui/primitives/confirm/action';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  adminBadgeBase,
  adminLabelStyle,
  adminTdStyle,
  adminThStyle,
  adminValueStyle,
} from '../../../features/admin/styles';
import { useInvalidateInstanceStats } from '../../../features/admin-stats/api';
import { selectUser, useAuth } from '../../../features/auth/auth-store';
import type { ProblemJson } from '../../../lib/api-error';
import { extractErrorCode } from '../../../lib/auth-errors';
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
 * DeleteUserDialog — destructive-delete modal with a typed-confirmation
 * gate. The operator must type the user's email exactly before the
 * destructive button activates. Internal to this file because it is
 * tightly coupled to the user-detail page lifecycle.
 */
function DeleteUserDialog({
  open,
  user,
  pending,
  onCancel,
  onConfirm,
}: {
  open: boolean;
  user: UserDetail;
  pending: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}): ReactElement {
  const { t } = useTranslation('admin');
  const [typed, setTyped] = useState('');
  const matches = typed.trim() === user.email;

  // Reset the typed buffer whenever the dialog re-opens. Render-time
  // reconciliation avoids the useEffect dance.
  const [prevOpen, setPrevOpen] = useState(open);
  if (open !== prevOpen) {
    setPrevOpen(open);
    if (open) setTyped('');
  }

  const handleSubmit = (e: FormEvent<HTMLFormElement>): void => {
    e.preventDefault();
    if (!matches || pending) return;
    onConfirm();
  };

  return (
    <Dialog
      open={open}
      onClose={onCancel}
      size="md"
      title={t('users.danger.confirm.title', { email: user.email })}
      dismissOnOverlayClick={!pending}
    >
      <form
        onSubmit={handleSubmit}
        style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-5)' }}
      >
        <p
          style={{
            margin: 0,
            color: 'var(--nf-color-fg-muted)',
            fontSize: 'var(--nf-text-sm)',
            lineHeight: 1.5,
          }}
        >
          {t('users.danger.confirm.warning')}
        </p>

        <div
          style={{
            display: 'flex',
            flexDirection: 'column',
            gap: 'var(--nf-space-2)',
            padding: 'var(--nf-space-3)',
            border: '1px solid var(--nf-color-border)',
            borderRadius: 'var(--nf-radius-md)',
            background: 'var(--nf-color-bg-sunken)',
          }}
        >
          <strong style={{ fontSize: 'var(--nf-text-sm)' }}>
            {t('users.danger.confirm.loses_heading')}
          </strong>
          <ul
            style={{
              margin: 0,
              paddingInlineStart: 'var(--nf-space-5)',
              fontSize: 'var(--nf-text-sm)',
              color: 'var(--nf-color-fg-muted)',
              display: 'flex',
              flexDirection: 'column',
              gap: 'var(--nf-space-1)',
            }}
          >
            <li>{t('users.danger.confirm.loses_account')}</li>
            <li>{t('users.danger.confirm.loses_workspaces', { count: user.workspaceCount })}</li>
            <li>{t('users.danger.confirm.loses_attachments')}</li>
          </ul>
        </div>

        <FormField
          label={t('users.danger.confirm.type_to_confirm_label')}
          description={t('users.danger.confirm.type_to_confirm_help', { email: user.email })}
        >
          {(control) => (
            <Input
              {...control}
              autoComplete="off"
              spellCheck={false}
              dir="ltr"
              value={typed}
              placeholder={t('users.danger.confirm.type_to_confirm_placeholder')}
              onChange={(e) => {
                setTyped(e.target.value);
              }}
            />
          )}
        </FormField>

        <div
          style={{
            display: 'flex',
            justifyContent: 'flex-end',
            gap: 'var(--nf-space-3)',
          }}
        >
          <Button type="button" variant="ghost" disabled={pending} onClick={onCancel}>
            {t('users.danger.confirm.cancel')}
          </Button>
          <Button type="submit" variant="danger" disabled={!matches || pending}>
            {pending ? t('users.danger.deleting') : t('users.danger.confirm.submit')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

/**
 * Renders the user detail content. Exported so unit tests can mount the
 * page without a real router; the production route still wires it via
 * `createFileRoute(...)({ component: UserDetailPage })` below.
 */
export function UserDetailPage(): ReactElement {
  const { userId } = Route.useParams();
  const { t } = useTranslation('admin');
  const navigate = useNavigate();
  const invalidateInstanceStats = useInvalidateInstanceStats();
  const currentUser = useAuth(selectUser);
  const [user, setUser] = useState<UserDetail | null>(null);
  const [sessions, setSessions] = useState<UserSession[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);
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
  const deleteGuard = useSubmitGuard();

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
      const { error: err } = await sdk.PATCH('/admin/users/{userId}', {
        params: { path: { userId } },
        body: { enabled: !user.enabled },
      });
      if (err) {
        const code = extractErrorCode(err as ProblemJson);
        toaster.show({
          tone: 'danger',
          message: code ? `${t('errors.generic')} (${code})` : t('errors.generic'),
        });
        return;
      }
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
        const { error: err } = await sdk.DELETE('/admin/instance-admins/{userId}', {
          params: { path: { userId } },
        });
        if (err) {
          const code = extractErrorCode(err as ProblemJson);
          toaster.show({
            tone: 'danger',
            message: code ? `${t('errors.generic')} (${code})` : t('errors.generic'),
          });
          return;
        }
      } finally {
        adminGuard.end();
      }
    } else {
      if (adminGuard.guard()) return;
      try {
        const { error: err } = await sdk.POST('/admin/instance-admins', {
          body: { userId: user.id },
        });
        if (err) {
          const code = extractErrorCode(err as ProblemJson);
          toaster.show({
            tone: 'danger',
            message: code ? `${t('errors.generic')} (${code})` : t('errors.generic'),
          });
          return;
        }
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
      if (err) {
        const code = extractErrorCode(err as ProblemJson);
        toaster.show({
          tone: 'danger',
          message: code ? `${t('errors.generic')} (${code})` : t('errors.generic'),
        });
        return;
      }
      setSessions((prev) => prev.filter((s) => s.id !== sessionId));
    } finally {
      sessionGuard.end();
    }
  };

  const handleDeleteConfirmed = async (): Promise<void> => {
    if (!user) return;
    if (deleteGuard.guard()) return;
    try {
      const { error: err } = await sdk.DELETE('/admin/users/{userId}', {
        params: { path: { userId } },
        body: { confirm: true },
      });
      if (err) {
        const code = extractErrorCode(err as ProblemJson);
        toaster.show({
          tone: 'danger',
          message: code
            ? t('users.danger.errors.delete_failed_with_code', { code })
            : t('users.danger.errors.delete_failed'),
        });
        return;
      }
      setDeleteOpen(false);
      void invalidateInstanceStats();
      toaster.show({ tone: 'success', message: t('users.danger.deleted') });
      void navigate({ to: '/admin/users' });
    } finally {
      deleteGuard.end();
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

      {currentUser?.id !== user.id ? (
        <section
          aria-labelledby="user-danger-zone-heading"
          style={{
            display: 'flex',
            flexDirection: 'column',
            gap: 'var(--nf-space-3)',
            marginBlockStart: 'var(--nf-space-4)',
            padding: 'var(--nf-space-4)',
            border: '1px solid var(--nf-color-danger)',
            borderRadius: 'var(--nf-radius-md)',
            background: 'color-mix(in srgb, var(--nf-color-danger) 4%, var(--nf-color-bg))',
          }}
        >
          <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-1)' }}>
            <h2
              id="user-danger-zone-heading"
              style={{
                margin: 0,
                fontFamily: 'var(--nf-font-sans)',
                fontSize: 'var(--nf-text-lg)',
                color: 'var(--nf-color-danger)',
              }}
            >
              {t('users.danger.title')}
            </h2>
            <p
              style={{
                margin: 0,
                color: 'var(--nf-color-fg-muted)',
                fontSize: 'var(--nf-text-sm)',
              }}
            >
              {t('users.danger.description')}
            </p>
          </header>
          <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
            <Button
              type="button"
              variant="danger"
              disabled={deleteGuard.submitting}
              onClick={() => {
                setDeleteOpen(true);
              }}
            >
              {t('users.danger.delete')}
            </Button>
          </div>
          <DeleteUserDialog
            open={deleteOpen}
            user={user}
            pending={deleteGuard.submitting}
            onCancel={() => {
              if (!deleteGuard.submitting) setDeleteOpen(false);
            }}
            onConfirm={() => {
              void handleDeleteConfirmed();
            }}
          />
        </section>
      ) : null}
    </div>
  );
}

export const Route = createFileRoute('/_authenticated/admin/users_/$userId')({
  component: UserDetailPage,
});
