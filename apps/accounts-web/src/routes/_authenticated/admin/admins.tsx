/**
 * /admin/admins -- Instance administrator list with grant/revoke.
 */

import type { components } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import Combobox, { type ComboboxOption } from '@nodate-flow/ui/primitives/combobox';
import { confirmAction } from '@nodate-flow/ui/primitives/confirm/action';
import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { adminTableStyle, adminTdStyle, adminThStyle } from '../../../features/admin/styles';
import { useInvalidateInstanceStats } from '../../../features/admin-stats/api';
import { formatTimestamp } from '../../../lib/format-timestamp';
import { sdk } from '../../../lib/sdk';
import { useSubmitGuard } from '../../../lib/use-submit-guard';

/**
 * SDK-derived shapes; the local interfaces this replaced rendered
 * `granted_by` as undefined because the field is named `grantedByDisplayName`
 * in the Go schema, and treated grant POST responses as `{admin: ...}` even
 * though the API returns `{ok: boolean}`.
 */
type InstanceAdmin = components['schemas']['InstanceAdmin'];
type AdminsResponse = components['schemas']['ListAdminsOutputBody'];

/** Subset of `User` consumed by the grant-search Combobox. */
type UserSearchResult = Pick<components['schemas']['User'], 'id' | 'email' | 'displayName'>;

export function AdminsPage(): ReactElement {
  const { t } = useTranslation('admin');
  const invalidateInstanceStats = useInvalidateInstanceStats();
  const [admins, setAdmins] = useState<InstanceAdmin[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const { guard: guardSubmit, submitting: actionLoading, end: endSubmit } = useSubmitGuard();
  const [grantError, setGrantError] = useState<string | null>(null);
  const [userResults, setUserResults] = useState<UserSearchResult[]>([]);
  const [selectedUserId, setSelectedUserId] = useState<string | undefined>(undefined);
  const [searching, setSearching] = useState(false);

  const searchUsers = useCallback(
    (query: string) => {
      const trimmed = query.trim();
      if (!trimmed) {
        setUserResults([]);
        return;
      }
      setSearching(true);
      void sdk
        .GET('/admin/users', {
          params: { query: { limit: 8, offset: 0, search: trimmed } },
        })
        .then((result) => {
          setSearching(false);
          if (result.error || !result.data) return;
          const body = result.data as components['schemas']['ListUsersOutputBody'];
          // Exclude users already promoted to instance admin. Compare against
          // the admin row's `userId` (the user's public_id), not `id` (the
          // grant row's public_id), so the exclusion actually matches.
          const adminIds = new Set(admins.map((a) => a.userId));
          setUserResults((body.items ?? []).filter((u) => !adminIds.has(u.id)));
        });
    },
    [admins],
  );

  useEffect(() => {
    setLoading(true);
    setError(null);
    void sdk.GET('/admin/instance-admins').then((result) => {
      if (result.error || !result.data) {
        setError(t('errors.generic'));
        setLoading(false);
        return;
      }
      const body = result.data as AdminsResponse;
      setAdmins(body.items ?? []);
      setLoading(false);
    });
  }, [t]);

  const handleRevoke = async (userId: string) => {
    const ok = await confirmAction({
      tone: 'danger',
      message: t('admins.confirm_revoke'),
      confirmLabel: t('admins.revoke'),
    });
    if (!ok) return;
    if (guardSubmit()) return;
    try {
      const { error: err } = await sdk.DELETE('/admin/instance-admins/{userId}', {
        params: { path: { userId } },
      });
      if (err) {
        setError(t('errors.generic'));
        return;
      }
      setAdmins((prev) => prev.filter((a) => a.userId !== userId));
      void invalidateInstanceStats();
    } finally {
      endSubmit();
    }
  };

  const handleGrant = async (user: UserSearchResult) => {
    setGrantError(null);
    if (guardSubmit()) return;
    try {
      const { data, error: err } = await sdk.POST('/admin/instance-admins', {
        body: { userId: user.id },
      });
      if (err || !data) {
        setGrantError(t('errors.generic'));
        return;
      }
      // The grant endpoint returns `{ok: boolean}`, not the new admin row,
      // so refetch the list to surface the freshly-promoted admin with the
      // server-side `grantedAt` / `grantedByDisplayName` fields filled in.
      const refreshed = await sdk.GET('/admin/instance-admins');
      if (refreshed.data) {
        const refreshedBody = refreshed.data as AdminsResponse;
        setAdmins(refreshedBody.items ?? []);
      }
      setSelectedUserId(undefined);
      setUserResults([]);
      void invalidateInstanceStats();
    } finally {
      endSubmit();
    }
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-6)' }}>
      <h1
        style={{
          fontFamily: 'var(--nf-font-sans)',
          fontSize: 'var(--nf-text-2xl)',
          margin: 0,
        }}
      >
        {t('admins.title')}
      </h1>

      {error ? (
        <p
          role="alert"
          style={{
            margin: 0,
            color: 'var(--nf-color-danger)',
            fontSize: 'var(--nf-text-sm)',
          }}
        >
          {error}
        </p>
      ) : null}

      {loading ? (
        <p style={{ color: 'var(--nf-color-fg-muted)' }}>{t('common.loading')}</p>
      ) : admins.length === 0 ? (
        <p style={{ color: 'var(--nf-color-fg-muted)' }}>{t('admins.no_admins')}</p>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table style={adminTableStyle}>
            <thead>
              <tr>
                <th style={adminThStyle}>{t('admins.name')}</th>
                <th style={adminThStyle}>{t('admins.email')}</th>
                <th style={adminThStyle}>{t('admins.granted_at')}</th>
                <th style={adminThStyle}>{t('admins.granted_by')}</th>
                <th style={adminThStyle} />
              </tr>
            </thead>
            <tbody>
              {admins.map((admin) => (
                <tr key={admin.id}>
                  <td style={adminTdStyle}>{admin.displayName}</td>
                  <td style={adminTdStyle}>{admin.email}</td>
                  <td style={adminTdStyle}>{formatTimestamp(admin.grantedAt)}</td>
                  <td style={adminTdStyle}>{admin.grantedByDisplayName ?? ''}</td>
                  <td style={adminTdStyle}>
                    <Button
                      variant="danger"
                      disabled={actionLoading}
                      onClick={() => void handleRevoke(admin.userId)}
                    >
                      {t('admins.revoke')}
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div
        style={{
          padding: 'var(--nf-space-4)',
          border: '1px solid var(--nf-color-border)',
          borderRadius: 'var(--nf-radius-md)',
        }}
      >
        <h2
          style={{
            fontFamily: 'var(--nf-font-sans)',
            fontSize: 'var(--nf-text-lg)',
            margin: '0 0 var(--nf-space-4) 0',
          }}
        >
          {t('admins.grant')}
        </h2>
        <div style={{ display: 'flex', gap: 'var(--nf-space-3)', alignItems: 'flex-end' }}>
          <div style={{ flex: 1 }}>
            <label
              htmlFor="grant-user-search"
              style={{
                display: 'block',
                fontSize: 'var(--nf-text-xs)',
                color: 'var(--nf-color-fg-muted)',
                marginBlockEnd: 'var(--nf-space-1)',
              }}
            >
              {t('admins.grant_search_label')}
            </label>
            <Combobox
              id="grant-user-search"
              aria-label={t('admins.grant_search_label')}
              placeholder={t('admins.grant_search_placeholder')}
              options={userResults.map<ComboboxOption>((u) => ({
                value: u.id,
                label: `${u.displayName} (${u.email})`,
              }))}
              value={selectedUserId}
              onChange={setSelectedUserId}
              onSearch={searchUsers}
              isLoading={searching}
              loadingMessage={t('common.loading')}
              emptyMessage={t('users.no_results')}
              renderItem={(opt) => {
                const user = userResults.find((u) => u.id === opt.value);
                if (!user) return opt.label;
                return (
                  <div>
                    <div>{user.displayName}</div>
                    <div
                      style={{
                        color: 'var(--nf-color-fg-muted)',
                        fontSize: 'var(--nf-text-xs)',
                      }}
                    >
                      {user.email}
                    </div>
                  </div>
                );
              }}
            />
          </div>
          <Button
            type="button"
            variant="primary"
            disabled={actionLoading || !selectedUserId}
            onClick={() => {
              const picked = userResults.find((u) => u.id === selectedUserId);
              if (picked) void handleGrant(picked);
            }}
          >
            {t('admins.grant_submit')}
          </Button>
        </div>
        <p
          style={{
            margin: 'var(--nf-space-2) 0 0 0',
            color: 'var(--nf-color-fg-muted)',
            fontSize: 'var(--nf-text-xs)',
          }}
        >
          {t('admins.grant_hint')}
        </p>
        {grantError ? (
          <p
            role="alert"
            style={{
              margin: 'var(--nf-space-2) 0 0 0',
              color: 'var(--nf-color-danger)',
              fontSize: 'var(--nf-text-sm)',
            }}
          >
            {grantError}
          </p>
        ) : null}
      </div>
    </div>
  );
}

export const Route = createFileRoute('/_authenticated/admin/admins')({
  component: AdminsPage,
});
