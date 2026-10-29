/**
 * /admin/admins -- Instance administrator list with grant/revoke.
 */

import type { components } from '@nodate-flow/sdk';
import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Button from '@nodate-flow/ui/primitives/button';
import Combobox, { type ComboboxOption } from '@nodate-flow/ui/primitives/combobox';
import { confirmAction } from '@nodate-flow/ui/primitives/confirm/action';
import PresenceRow from '@nodate-flow/ui/primitives/presence-row';
import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import TableRegion from '../../../components/table-region';
import { useInvalidateInstanceStats } from '../../../features/admin-stats/api';
import { useArrivedRows } from '../../../hooks/use-arrived-rows';
import { apiRequest } from '../../../lib/api';
import { formatTimestamp } from '../../../lib/format-timestamp';
import { useSubmitGuard } from '../../../lib/use-submit-guard';
import styles from './admins.module.css';

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
  const { t, i18n } = useTranslation('admin');
  const locale = i18n.resolvedLanguage ?? 'en';
  const invalidateInstanceStats = useInvalidateInstanceStats();
  const [admins, setAdmins] = useState<InstanceAdmin[]>([]);
  const [loading, setLoading] = useState(true);
  const adminPresence = usePresenceList(admins, (admin) => admin.userId);
  const isArrived = useArrivedRows(
    admins.map((admin) => admin.userId),
    !loading,
  );
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
      void apiRequest(
        (client) =>
          client.GET('/admin/users', {
            params: { query: { limit: 8, offset: 0, search: trimmed } },
          }),
        'Failed to search users',
        { onError: 'empty', empty: null },
      ).then((result) => {
        setSearching(false);
        if (result === null) return;
        const body = result as components['schemas']['ListUsersOutputBody'];
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
    void apiRequest(
      (client) => client.GET('/admin/instance-admins'),
      'Failed to load instance admins',
      { onError: 'empty', empty: null },
    ).then((result) => {
      if (result === null) {
        setError(t('errors.generic'));
        setLoading(false);
        return;
      }
      const body = result as AdminsResponse;
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
      try {
        await apiRequest(
          (client) =>
            client.DELETE('/admin/instance-admins/{userId}', { params: { path: { userId } } }),
          'Failed to revoke the instance admin',
        );
      } catch {
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
      try {
        await apiRequest(
          (client) => client.POST('/admin/instance-admins', { body: { userId: user.id } }),
          'Failed to grant instance admin',
        );
      } catch {
        setGrantError(t('errors.generic'));
        return;
      }
      // The grant endpoint returns `{ok: boolean}`, not the new admin row,
      // so refetch the list to surface the freshly-promoted admin with the
      // server-side `grantedAt` / `grantedByDisplayName` fields filled in.
      const refreshed = await apiRequest(
        (client) => client.GET('/admin/instance-admins'),
        'Failed to load instance admins',
        { onError: 'empty', empty: null },
      );
      if (refreshed) {
        const refreshedBody = refreshed as AdminsResponse;
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
    <div className="aw-stack aw-stack-6">
      <h1 className="aw-page-title">{t('admins.title')}</h1>

      {error ? (
        <p role="alert" className="aw-error">
          {error}
        </p>
      ) : null}

      <TableRegion
        loading={loading}
        rowCount={adminPresence.entries.length}
        loadingLabel={t('common.loading')}
        emptyTitle={t('admins.no_admins')}
      >
        <table className="aw-table">
          <thead>
            <tr>
              <th className="aw-th">{t('admins.name')}</th>
              <th className="aw-th">{t('admins.email')}</th>
              <th className="aw-th">{t('admins.granted_at')}</th>
              <th className="aw-th">{t('admins.granted_by')}</th>
              <th className="aw-th" />
            </tr>
          </thead>
          <tbody>
            {adminPresence.entries.map(({ key, item: admin, present }) => (
              <PresenceRow
                key={key}
                present={present}
                arrived={isArrived(key)}
                onExited={() => {
                  adminPresence.onExited(key);
                }}
              >
                <td className="aw-td">{admin.displayName}</td>
                <td className="aw-td">{admin.email}</td>
                <td className="aw-td">{formatTimestamp(admin.grantedAt, { locale })}</td>
                <td className="aw-td">{admin.grantedByDisplayName ?? ''}</td>
                <td className="aw-td">
                  <Button
                    variant="danger"
                    size="sm"
                    aria-busy={actionLoading || undefined}
                    disabled={actionLoading}
                    onClick={() => void handleRevoke(admin.userId)}
                  >
                    {t('admins.revoke')}
                  </Button>
                </td>
              </PresenceRow>
            ))}
          </tbody>
        </table>
      </TableRegion>

      <section className="aw-panel aw-stack aw-stack-2" aria-labelledby="grant-admin-heading">
        <h2 id="grant-admin-heading" className="aw-section-title">
          {t('admins.grant')}
        </h2>
        <div className={styles.grantRow}>
          <div className={styles.grantField}>
            <label htmlFor="grant-user-search" className={styles.grantLabel}>
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
                    <div className={styles.optionEmail}>{user.email}</div>
                  </div>
                );
              }}
            />
          </div>
          <Button
            type="button"
            variant="primary"
            aria-busy={actionLoading || undefined}
            disabled={actionLoading || !selectedUserId}
            onClick={() => {
              const picked = userResults.find((u) => u.id === selectedUserId);
              if (picked) void handleGrant(picked);
            }}
          >
            {t('admins.grant_submit')}
          </Button>
        </div>
        <p className="aw-flush aw-muted aw-text-xs">{t('admins.grant_hint')}</p>
        {grantError ? (
          <p role="alert" className="aw-error">
            {grantError}
          </p>
        ) : null}
      </section>
    </div>
  );
}

export const Route = createFileRoute('/_authenticated/admin/admins')({
  component: AdminsPage,
});
