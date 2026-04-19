/**
 * /admin/admins -- Instance administrator list with grant/revoke.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Input from '@nodate-flow/ui/primitives/input';
import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { sdk } from '../../../lib/sdk';

interface InstanceAdmin {
  id: string;
  email: string;
  displayName: string;
  grantedAt: number;
  grantedBy: string;
}

interface AdminsResponse {
  items: InstanceAdmin[];
  total: number;
}

const tableStyle: React.CSSProperties = {
  width: '100%',
  borderCollapse: 'collapse',
  fontSize: 'var(--nf-text-sm, 0.875rem)',
};

const thStyle: React.CSSProperties = {
  textAlign: 'start',
  padding: 'var(--nf-space-2, 0.5rem) var(--nf-space-3, 0.75rem)',
  borderBlockEnd: '2px solid var(--nf-color-border)',
  fontWeight: 600,
  color: 'var(--nf-color-fg-muted)',
};

const tdStyle: React.CSSProperties = {
  padding: 'var(--nf-space-2, 0.5rem) var(--nf-space-3, 0.75rem)',
  borderBlockEnd: '1px solid var(--nf-color-border)',
};

function formatTimestamp(ts: number): string {
  return new Date(ts * 1000).toLocaleString();
}

function AdminsPage(): ReactElement {
  const { t } = useTranslation('admin');
  const [admins, setAdmins] = useState<InstanceAdmin[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [actionLoading, setActionLoading] = useState(false);
  const [grantUserId, setGrantUserId] = useState('');
  const [grantError, setGrantError] = useState<string | null>(null);

  useEffect(() => {
    setLoading(true);
    setError(null);
    void sdk.GET('/admin/admins').then((result) => {
      if (result.error || !result.data) {
        setError(t('errors.generic'));
        setLoading(false);
        return;
      }
      const body = result.data as AdminsResponse;
      setAdmins(body.items);
      setLoading(false);
    });
  }, [t]);

  const handleRevoke = async (adminId: string) => {
    if (!window.confirm(t('admins.confirm_revoke'))) return;

    setActionLoading(true);
    const { error: err } = await sdk.DELETE('/admin/admins/{adminId}', {
      params: { path: { adminId } },
    });
    setActionLoading(false);

    if (!err) {
      setAdmins((prev) => prev.filter((a) => a.id !== adminId));
    }
  };

  const handleGrant = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!grantUserId.trim()) return;

    setGrantError(null);
    setActionLoading(true);
    const { data, error: err } = await sdk.POST('/admin/admins', {
      body: { userId: grantUserId.trim() },
    });
    setActionLoading(false);

    if (err || !data) {
      setGrantError(t('errors.generic'));
      return;
    }

    const body = data as { admin: InstanceAdmin };
    setAdmins((prev) => [...prev, body.admin]);
    setGrantUserId('');
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-6, 1.5rem)' }}>
      <h1
        style={{
          fontFamily: 'var(--nf-font-display, var(--font-display))',
          fontSize: 'var(--nf-text-2xl, 1.5rem)',
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
            fontSize: 'var(--nf-text-sm, 0.875rem)',
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
          <table style={tableStyle}>
            <thead>
              <tr>
                <th style={thStyle}>{t('admins.name')}</th>
                <th style={thStyle}>{t('admins.email')}</th>
                <th style={thStyle}>{t('admins.granted_at')}</th>
                <th style={thStyle}>{t('admins.granted_by')}</th>
                <th style={thStyle} />
              </tr>
            </thead>
            <tbody>
              {admins.map((admin) => (
                <tr key={admin.id}>
                  <td style={tdStyle}>{admin.displayName}</td>
                  <td style={tdStyle}>{admin.email}</td>
                  <td style={tdStyle}>{formatTimestamp(admin.grantedAt)}</td>
                  <td style={tdStyle}>{admin.grantedBy}</td>
                  <td style={tdStyle}>
                    <Button
                      variant="danger"
                      disabled={actionLoading}
                      onClick={() => void handleRevoke(admin.id)}
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
          padding: 'var(--nf-space-4, 1rem)',
          border: '1px solid var(--nf-color-border)',
          borderRadius: 'var(--nf-radius-md, 0.375rem)',
        }}
      >
        <h2
          style={{
            fontFamily: 'var(--nf-font-display, var(--font-display))',
            fontSize: 'var(--nf-text-lg, 1.125rem)',
            margin: '0 0 var(--nf-space-4, 1rem) 0',
          }}
        >
          {t('admins.grant')}
        </h2>
        <form
          onSubmit={(e) => void handleGrant(e)}
          style={{ display: 'flex', gap: 'var(--nf-space-3, 0.75rem)', alignItems: 'flex-end' }}
        >
          <div style={{ flex: 1 }}>
            <label
              htmlFor="grant-user-id"
              style={{
                display: 'block',
                fontSize: 'var(--nf-text-xs, 0.75rem)',
                color: 'var(--nf-color-fg-muted)',
                marginBlockEnd: 'var(--nf-space-1, 0.25rem)',
              }}
            >
              {t('admins.grant_user_id_label')}
            </label>
            <Input
              id="grant-user-id"
              type="text"
              value={grantUserId}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => setGrantUserId(e.target.value)}
              placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
            />
          </div>
          <Button type="submit" variant="primary" disabled={actionLoading || !grantUserId.trim()}>
            {t('admins.grant_submit')}
          </Button>
        </form>
        {grantError ? (
          <p
            role="alert"
            style={{
              margin: 'var(--nf-space-2, 0.5rem) 0 0 0',
              color: 'var(--nf-color-danger)',
              fontSize: 'var(--nf-text-sm, 0.875rem)',
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
