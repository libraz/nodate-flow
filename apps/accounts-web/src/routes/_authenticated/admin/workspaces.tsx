/**
 * /admin/workspaces -- Paginated workspace management list.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Input from '@nodate-flow/ui/primitives/input';
import { Link, createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { sdk } from '../../../lib/sdk';

interface AdminWorkspace {
  id: string;
  name: string;
  slug: string;
  memberCount: number;
  projectCount: number;
  enabled: boolean;
  createdAt: number;
}

interface WorkspacesResponse {
  items: AdminWorkspace[];
  total: number;
}

type StatusFilter = 'all' | 'active' | 'suspended';

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

const badgeBase: React.CSSProperties = {
  display: 'inline-block',
  padding: '0.125rem 0.5rem',
  borderRadius: 'var(--nf-radius-full, 9999px)',
  fontSize: 'var(--nf-text-xs, 0.75rem)',
  fontWeight: 500,
};

function formatTimestamp(ts: number | null, never: string): string {
  if (ts === null || ts === 0) return never;
  return new Date(ts * 1000).toLocaleString();
}

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

    const params = new URLSearchParams();
    params.set('page', String(page));
    params.set('perPage', String(perPage));
    if (search) params.set('search', search);
    if (statusFilter !== 'all') params.set('status', statusFilter);

    void sdk
      .GET('/admin/workspaces', {
        params: {
          query: {
            page: String(page),
            perPage: String(perPage),
            ...(search ? { search } : {}),
            ...(statusFilter !== 'all' ? { status: statusFilter } : {}),
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
        setWorkspaces(body.items);
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
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-6, 1.5rem)' }}>
      <h1
        style={{
          fontFamily: 'var(--nf-font-display, var(--font-display))',
          fontSize: 'var(--nf-text-2xl, 1.5rem)',
          margin: 0,
        }}
      >
        {t('workspaces.title')}
      </h1>

      <div style={{ display: 'flex', gap: 'var(--nf-space-3, 0.75rem)', alignItems: 'center' }}>
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
            borderRadius: 'var(--nf-radius-md, 0.375rem)',
            border: 'var(--nf-space-px, 1px) solid var(--nf-color-border)',
            background: 'var(--nf-color-bg)',
            color: 'var(--nf-color-fg)',
            fontSize: 'var(--nf-text-sm, 0.875rem)',
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
            fontSize: 'var(--nf-text-sm, 0.875rem)',
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
          <table style={tableStyle}>
            <thead>
              <tr>
                <th style={thStyle}>{t('workspaces.name')}</th>
                <th style={thStyle}>{t('workspaces.slug')}</th>
                <th style={thStyle}>{t('workspaces.members')}</th>
                <th style={thStyle}>{t('workspaces.projects')}</th>
                <th style={thStyle}>{t('workspaces.status')}</th>
                <th style={thStyle}>{t('workspaces.created_at')}</th>
              </tr>
            </thead>
            <tbody>
              {workspaces.map((ws) => (
                <tr key={ws.id}>
                  <td style={tdStyle}>
                    <Link
                      to="/admin/workspaces/$wsId"
                      params={{ wsId: ws.id }}
                      style={{ color: 'var(--nf-color-accent)' }}
                    >
                      {ws.name}
                    </Link>
                  </td>
                  <td style={tdStyle}>{ws.slug}</td>
                  <td style={tdStyle}>{ws.memberCount}</td>
                  <td style={tdStyle}>{ws.projectCount}</td>
                  <td style={tdStyle}>
                    <span
                      style={{
                        ...badgeBase,
                        background: ws.enabled
                          ? 'var(--nf-color-success, rgba(0,128,0,0.1))'
                          : 'var(--nf-color-danger, rgba(255,0,0,0.1))',
                        color: ws.enabled
                          ? 'var(--nf-color-success, green)'
                          : 'var(--nf-color-danger, red)',
                      }}
                    >
                      {ws.enabled ? t('workspaces.enabled') : t('workspaces.disabled')}
                    </span>
                  </td>
                  <td style={tdStyle}>{formatTimestamp(ws.createdAt, t('common.never'))}</td>
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
          fontSize: 'var(--nf-text-sm, 0.875rem)',
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
