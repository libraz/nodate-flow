/**
 * /admin/workspaces -- Paginated workspace management list.
 */

import type { components } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import Input from '@nodate-flow/ui/primitives/input';
import { createFileRoute, Link } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import {
  adminBadgeBase,
  adminTableStyle,
  adminTdStyle,
  adminThStyle,
} from '../../../features/admin/styles';
import { formatTimestamp } from '../../../lib/format-timestamp';
import { sdk } from '../../../lib/sdk';

/**
 * SDK-derived shapes; the local interfaces this replaced silently allowed
 * sending unsupported `page` / `perPage` / `status` query params to the API.
 */
type AdminWorkspace = components['schemas']['AdminWorkspace'];
type WorkspacesResponse = components['schemas']['AdminListWorkspacesOutputBody'];

type StatusFilter = 'all' | 'active' | 'suspended';

/** UI status filter -> API `enabled` query parameter mapping. */
const STATUS_TO_ENABLED: Record<StatusFilter, 'true' | 'false' | '' | undefined> = {
  all: undefined,
  active: 'true',
  suspended: 'false',
};

function WorkspacesPage(): ReactElement {
  const { t } = useTranslation('admin');
  const [workspaces, setWorkspaces] = useState<AdminWorkspace[]>([]);
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
      .GET('/admin/workspaces', {
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
        const body = result.data as WorkspacesResponse;
        setWorkspaces(body.items ?? []);
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
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-6)' }}>
      <h1
        style={{
          fontFamily: 'var(--nf-font-sans)',
          fontSize: 'var(--nf-text-2xl)',
          margin: 0,
        }}
      >
        {t('workspaces.title')}
      </h1>

      <div style={{ display: 'flex', gap: 'var(--nf-space-3)', alignItems: 'center' }}>
        <div style={{ flex: 1 }}>
          <Input
            type="search"
            placeholder={t('workspaces.search')}
            value={search}
            onChange={handleSearchChange}
          />
        </div>
        <select
          value={statusFilter}
          onChange={handleFilterChange}
          aria-label={t('workspaces.status')}
          style={{
            padding: '0.5rem 0.75rem',
            borderRadius: 'var(--nf-radius-md)',
            border: 'var(--nf-space-px) solid var(--nf-color-border)',
            background: 'var(--nf-color-bg)',
            color: 'var(--nf-color-fg)',
            fontSize: 'var(--nf-text-sm)',
          }}
        >
          <option value="all">{t('workspaces.all_workspaces')}</option>
          <option value="active">{t('workspaces.active_only')}</option>
          <option value="suspended">{t('workspaces.suspended_only')}</option>
        </select>
      </div>

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
      ) : workspaces.length === 0 ? (
        <p style={{ color: 'var(--nf-color-fg-muted)' }}>{t('workspaces.no_results')}</p>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table style={adminTableStyle}>
            <thead>
              <tr>
                <th style={adminThStyle}>{t('workspaces.name')}</th>
                <th style={adminThStyle}>{t('workspaces.slug')}</th>
                <th style={adminThStyle}>{t('workspaces.members')}</th>
                <th style={adminThStyle}>{t('workspaces.projects')}</th>
                <th style={adminThStyle}>{t('workspaces.status')}</th>
                <th style={adminThStyle}>{t('workspaces.created_at')}</th>
              </tr>
            </thead>
            <tbody>
              {workspaces.map((ws) => (
                <tr key={ws.id}>
                  <td style={adminTdStyle}>
                    <Link
                      to="/admin/workspaces/$wsId"
                      params={{ wsId: ws.id }}
                      style={{ color: 'var(--nf-color-accent)' }}
                    >
                      {ws.name}
                    </Link>
                  </td>
                  <td style={adminTdStyle}>{ws.slug}</td>
                  <td style={adminTdStyle}>{ws.memberCount}</td>
                  <td style={adminTdStyle}>{ws.projectCount ?? 0}</td>
                  <td style={adminTdStyle}>
                    <span
                      style={{
                        ...adminBadgeBase,
                        background: ws.enabled
                          ? 'color-mix(in srgb, var(--nf-color-success) 15%, transparent)'
                          : 'color-mix(in srgb, var(--nf-color-danger) 15%, transparent)',
                        color: ws.enabled ? 'var(--nf-color-success)' : 'var(--nf-color-danger)',
                      }}
                    >
                      {ws.enabled ? t('workspaces.enabled') : t('workspaces.disabled')}
                    </span>
                  </td>
                  <td style={adminTdStyle}>{formatTimestamp(ws.createdAt, t('common.never'))}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          fontSize: 'var(--nf-text-sm)',
        }}
      >
        <Button variant="default" disabled={page <= 1} onClick={() => setPage((p) => p - 1)}>
          {t('common.previous')}
        </Button>
        <span style={{ color: 'var(--nf-color-fg-muted)' }}>
          {t('common.page', { page, total: totalPages })}
        </span>
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

export const Route = createFileRoute('/_authenticated/admin/workspaces')({
  component: WorkspacesPage,
});
