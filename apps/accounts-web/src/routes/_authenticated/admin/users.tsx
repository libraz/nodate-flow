/**
 * /admin/users -- Paginated user management list.
 */

import type { components } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import Input from '@nodate-flow/ui/primitives/input';
import { Link, createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatTimestamp } from '../../../lib/format-timestamp';
import { sdk } from '../../../lib/sdk';

/**
 * SDK-derived shapes; the local interfaces this replaced silently allowed
 * sending unsupported `page` / `perPage` / `status` query params to the API.
 */
type AdminUser = components['schemas']['User'];
type UsersResponse = components['schemas']['ListUsersOutputBody'];

type StatusFilter = 'all' | 'active' | 'suspended';

/** UI status filter -> API `enabled` query parameter mapping. */
const STATUS_TO_ENABLED: Record<StatusFilter, 'true' | 'false' | '' | undefined> = {
  all: undefined,
  active: 'true',
  suspended: 'false',
};

function UsersPage(): ReactElement {
  const { t } = useTranslation('admin');
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [perPage] = useState(20);
  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);

    const offset = (page - 1) * perPage;
    const enabledParam = STATUS_TO_ENABLED[statusFilter];

    void sdk
      .GET('/admin/users', {
        params: {
          query: {
            limit: perPage,
            offset,
            ...(search ? { search } : {}),
            ...(enabledParam !== undefined ? { enabled: enabledParam } : {}),
          },
        },
      })
      .then((result) => {
        if (cancelled) return;
        if (result.error || !result.data) {
          setError(t('errors.generic'));
          setLoading(false);
          return;
        }
        const body = result.data as UsersResponse;
        setUsers(body.items ?? []);
        setTotal(body.total);
        setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [page, perPage, search, statusFilter, t]);

  const totalPages = Math.max(1, Math.ceil(total / perPage));

  const handleSearchChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setSearch(e.target.value);
    setPage(1);
  };

  const handleFilterChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    setStatusFilter(e.target.value as StatusFilter);
    setPage(1);
  };

  return (
    <div className="aw-stack aw-stack-6">
      <h1 className="aw-page-title">{t('users.title')}</h1>

      <div className="aw-row aw-row-3">
        <div className="aw-grow">
          <Input
            type="search"
            placeholder={t('users.search')}
            value={search}
            onChange={handleSearchChange}
          />
        </div>
        <select
          value={statusFilter}
          onChange={handleFilterChange}
          aria-label={t('users.status')}
          className="aw-select"
        >
          <option value="all">{t('users.all_users')}</option>
          <option value="active">{t('users.active_only')}</option>
          <option value="suspended">{t('users.suspended_only')}</option>
        </select>
      </div>

      {error ? (
        <p role="alert" className="aw-error">
          {error}
        </p>
      ) : null}

      {loading ? (
        <p className="aw-muted">{t('common.loading')}</p>
      ) : users.length === 0 ? (
        <p className="aw-muted">{t('users.no_results')}</p>
      ) : (
        <div className="aw-table-scroll">
          <table className="aw-table">
            <thead>
              <tr>
                <th className="aw-th">{t('users.email')}</th>
                <th className="aw-th">{t('users.name')}</th>
                <th className="aw-th">{t('users.status')}</th>
                <th className="aw-th">{t('users.admin')}</th>
                <th className="aw-th">{t('users.workspaces')}</th>
                <th className="aw-th">{t('users.last_login')}</th>
                <th className="aw-th">{t('users.created_at')}</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => (
                <tr key={u.id}>
                  <td className="aw-td">
                    <Link to="/admin/users/$userId" params={{ userId: u.id }} className="aw-link">
                      {u.email}
                    </Link>
                  </td>
                  <td className="aw-td">{u.displayName}</td>
                  <td className="aw-td">
                    <span
                      className={
                        u.enabled ? 'aw-badge aw-badge-success' : 'aw-badge aw-badge-danger'
                      }
                    >
                      {u.enabled ? t('users.enabled') : t('users.disabled')}
                    </span>
                  </td>
                  <td className="aw-td">{u.isInstanceAdmin ? t('common.yes') : t('common.no')}</td>
                  <td className="aw-td">{u.workspaceCount}</td>
                  <td className="aw-td">{formatTimestamp(u.lastLoginAt, t('common.never'))}</td>
                  <td className="aw-td">{formatTimestamp(u.createdAt, t('common.never'))}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className="aw-pagination">
        <Button variant="default" disabled={page <= 1} onClick={() => setPage((p) => p - 1)}>
          {t('common.previous')}
        </Button>
        <span className="aw-muted">{t('common.page', { page, total: totalPages })}</span>
        <Button
          variant="default"
          disabled={page >= totalPages}
          onClick={() => setPage((p) => p + 1)}
        >
          {t('common.next')}
        </Button>
      </div>
    </div>
  );
}

export const Route = createFileRoute('/_authenticated/admin/users')({
  component: UsersPage,
});
