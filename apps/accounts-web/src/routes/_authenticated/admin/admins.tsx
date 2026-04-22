/**
 * /admin/admins -- Instance administrator list with grant/revoke.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Input from '@nodate-flow/ui/primitives/input';
import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useCallback, useEffect, useRef, useState } from 'react';
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

interface UserSearchResult {
  id: string;
  email: string;
  displayName: string;
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
  const [grantError, setGrantError] = useState<string | null>(null);
  const [userSearch, setUserSearch] = useState('');
  const [userResults, setUserResults] = useState<UserSearchResult[]>([]);
  const [selectedUser, setSelectedUser] = useState<UserSearchResult | null>(null);
  const [showDropdown, setShowDropdown] = useState(false);
  const [searching, setSearching] = useState(false);
  const searchRef = useRef<HTMLDivElement>(null);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const searchUsers = useCallback(
    (query: string) => {
      if (debounceRef.current) clearTimeout(debounceRef.current);
      if (!query.trim()) {
        setUserResults([]);
        setShowDropdown(false);
        return;
      }
      debounceRef.current = setTimeout(() => {
        setSearching(true);
        void sdk
          .GET('/admin/users', {
            params: { query: { page: '1', perPage: '8', search: query.trim() } },
          })
          .then((result) => {
            setSearching(false);
            if (result.error || !result.data) return;
            const body = result.data as { items: UserSearchResult[] };
            // Exclude users that are already admins
            const adminIds = new Set(admins.map((a) => a.id));
            setUserResults(body.items.filter((u) => !adminIds.has(u.id)));
            setShowDropdown(true);
          });
      }, 300);
    },
    [admins],
  );

  // Close dropdown on outside click
  useEffect(() => {
    const handleClick = (e: MouseEvent) => {
      if (searchRef.current && !searchRef.current.contains(e.target as Node)) {
        setShowDropdown(false);
      }
    };
    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, []);

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
      setAdmins(body.items);
      setLoading(false);
    });
  }, [t]);

  const handleRevoke = async (adminId: string) => {
    if (!window.confirm(t('admins.confirm_revoke'))) return;

    setActionLoading(true);
    const { error: err } = await sdk.DELETE('/admin/instance-admins/{adminId}', {
      params: { path: { adminId } },
    });
    setActionLoading(false);

    if (!err) {
      setAdmins((prev) => prev.filter((a) => a.id !== adminId));
    }
  };

  const handleGrant = async (user: UserSearchResult) => {
    setGrantError(null);
    setActionLoading(true);
    const { data, error: err } = await sdk.POST('/admin/instance-admins', {
      body: { userId: user.id },
    });
    setActionLoading(false);

    if (err || !data) {
      setGrantError(t('errors.generic'));
      return;
    }

    const body = data as { admin: InstanceAdmin };
    setAdmins((prev) => [...prev, body.admin]);
    setSelectedUser(null);
    setUserSearch('');
    setUserResults([]);
    setShowDropdown(false);
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
        <div style={{ display: 'flex', gap: 'var(--nf-space-3, 0.75rem)', alignItems: 'flex-end' }}>
          <div style={{ flex: 1, position: 'relative' }} ref={searchRef}>
            <label
              htmlFor="grant-user-search"
              style={{
                display: 'block',
                fontSize: 'var(--nf-text-xs, 0.75rem)',
                color: 'var(--nf-color-fg-muted)',
                marginBlockEnd: 'var(--nf-space-1, 0.25rem)',
              }}
            >
              {t('admins.grant_search_label')}
            </label>
            {selectedUser ? (
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 'var(--nf-space-2, 0.5rem)',
                  padding: '0.5rem 0.75rem',
                  border: '1px solid var(--nf-color-accent)',
                  borderRadius: 'var(--nf-radius-md, 0.375rem)',
                  background: 'color-mix(in srgb, var(--nf-color-accent) 8%, var(--nf-color-bg))',
                  fontSize: 'var(--nf-text-sm, 0.875rem)',
                }}
              >
                <span style={{ flex: 1 }}>
                  {selectedUser.displayName}{' '}
                  <span style={{ color: 'var(--nf-color-fg-muted)' }}>({selectedUser.email})</span>
                </span>
                <button
                  type="button"
                  onClick={() => {
                    setSelectedUser(null);
                    setUserSearch('');
                  }}
                  style={{
                    background: 'none',
                    border: 'none',
                    cursor: 'pointer',
                    color: 'var(--nf-color-fg-muted)',
                    fontSize: 'var(--nf-text-lg, 1.125rem)',
                    lineHeight: 1,
                    padding: 0,
                  }}
                  aria-label={t('admins.clear_selection')}
                >
                  &times;
                </button>
              </div>
            ) : (
              <Input
                id="grant-user-search"
                type="search"
                value={userSearch}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) => {
                  setUserSearch(e.target.value);
                  searchUsers(e.target.value);
                }}
                onFocus={() => {
                  if (userResults.length > 0) setShowDropdown(true);
                }}
                placeholder={t('admins.grant_search_placeholder')}
              />
            )}
            {showDropdown && (
              <ul
                style={{
                  position: 'absolute',
                  top: '100%',
                  left: 0,
                  right: 0,
                  zIndex: 10,
                  margin: 'var(--nf-space-1, 0.25rem) 0 0 0',
                  padding: 0,
                  listStyle: 'none',
                  background: 'var(--nf-color-bg)',
                  border: '1px solid var(--nf-color-border)',
                  borderRadius: 'var(--nf-radius-md, 0.375rem)',
                  boxShadow: 'var(--nf-shadow-md, 0 4px 6px rgba(0,0,0,0.1))',
                  maxHeight: '240px',
                  overflowY: 'auto',
                }}
              >
                {searching ? (
                  <li
                    style={{
                      padding: 'var(--nf-space-2, 0.5rem) var(--nf-space-3, 0.75rem)',
                      color: 'var(--nf-color-fg-muted)',
                      fontSize: 'var(--nf-text-sm, 0.875rem)',
                    }}
                  >
                    {t('common.loading')}
                  </li>
                ) : userResults.length === 0 ? (
                  <li
                    style={{
                      padding: 'var(--nf-space-2, 0.5rem) var(--nf-space-3, 0.75rem)',
                      color: 'var(--nf-color-fg-muted)',
                      fontSize: 'var(--nf-text-sm, 0.875rem)',
                    }}
                  >
                    {t('users.no_results')}
                  </li>
                ) : (
                  userResults.map((u) => (
                    <li key={u.id}>
                      <button
                        type="button"
                        onClick={() => {
                          setSelectedUser(u);
                          setShowDropdown(false);
                          setUserSearch('');
                        }}
                        style={{
                          display: 'block',
                          width: '100%',
                          textAlign: 'start',
                          padding: 'var(--nf-space-2, 0.5rem) var(--nf-space-3, 0.75rem)',
                          background: 'none',
                          border: 'none',
                          cursor: 'pointer',
                          fontSize: 'var(--nf-text-sm, 0.875rem)',
                          color: 'var(--nf-color-fg)',
                        }}
                        onMouseEnter={(e) => {
                          (e.currentTarget as HTMLElement).style.background =
                            'var(--nf-color-bg-muted, #f5f5f5)';
                        }}
                        onMouseLeave={(e) => {
                          (e.currentTarget as HTMLElement).style.background = 'none';
                        }}
                      >
                        <div>{u.displayName}</div>
                        <div
                          style={{
                            color: 'var(--nf-color-fg-muted)',
                            fontSize: 'var(--nf-text-xs, 0.75rem)',
                          }}
                        >
                          {u.email}
                        </div>
                      </button>
                    </li>
                  ))
                )}
              </ul>
            )}
          </div>
          <Button
            type="button"
            variant="primary"
            disabled={actionLoading || !selectedUser}
            onClick={() => {
              if (selectedUser) void handleGrant(selectedUser);
            }}
          >
            {t('admins.grant_submit')}
          </Button>
        </div>
        <p
          style={{
            margin: 'var(--nf-space-2, 0.5rem) 0 0 0',
            color: 'var(--nf-color-fg-muted)',
            fontSize: 'var(--nf-text-xs, 0.75rem)',
          }}
        >
          {t('admins.grant_hint')}
        </p>
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
