/**
 * /admin/workspaces/$wsId -- Workspace detail with suspend/enable.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { Link, createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { apiRequest } from '../../../lib/api-client';

interface WorkspaceDetail {
  id: string;
  name: string;
  slug: string;
  memberCount: number;
  projectCount: number;
  enabled: boolean;
  createdAt: number;
}

const labelStyle: React.CSSProperties = {
  color: 'var(--nf-color-fg-muted, var(--color-muted))',
  fontSize: 'var(--nf-text-xs, 0.75rem)',
  marginBlockEnd: 'var(--nf-space-1, 0.25rem)',
};

const valueStyle: React.CSSProperties = {
  fontSize: 'var(--nf-text-sm, 0.875rem)',
  marginBlockEnd: 'var(--nf-space-3, 0.75rem)',
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

function WorkspaceDetailPage(): ReactElement {
  const { wsId } = Route.useParams();
  const { t } = useTranslation('admin');
  const [workspace, setWorkspace] = useState<WorkspaceDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [actionLoading, setActionLoading] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);

    void apiRequest<WorkspaceDetail>(`/admin/workspaces/${wsId}`).then((result) => {
      if (cancelled) return;
      if (result.error || !result.data) {
        setError(t('errors.generic'));
        setLoading(false);
        return;
      }
      setWorkspace(result.data);
      setLoading(false);
    });

    return () => {
      cancelled = true;
    };
  }, [wsId, t]);

  const handleToggleEnabled = async () => {
    if (!workspace) return;
    const confirmMsg = workspace.enabled ? t('workspaces.suspend') : t('workspaces.enable');
    if (!window.confirm(confirmMsg)) return;

    setActionLoading(true);
    await apiRequest<{ ok: boolean }>(`/admin/workspaces/${wsId}`, {
      method: 'PATCH',
      body: { enabled: !workspace.enabled },
    });
    setActionLoading(false);

    // Refetch
    const result = await apiRequest<WorkspaceDetail>(`/admin/workspaces/${wsId}`);
    if (result.data) {
      setWorkspace(result.data);
    }
  };

  if (loading) {
    return (
      <p style={{ color: 'var(--nf-color-fg-muted, var(--color-muted))' }}>{t('common.loading')}</p>
    );
  }

  if (error || !workspace) {
    return (
      <div>
        <Link
          to="/admin/workspaces"
          style={{ color: 'var(--nf-color-fg-accent, var(--color-accent))' }}
        >
          {t('common.back')}
        </Link>
        <p
          role="alert"
          style={{
            color: 'var(--nf-color-fg-danger, var(--color-danger))',
            fontSize: 'var(--nf-text-sm, 0.875rem)',
          }}
        >
          {error ?? t('errors.generic')}
        </p>
      </div>
    );
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-6, 1.5rem)' }}>
      <div>
        <Link
          to="/admin/workspaces"
          style={{
            color: 'var(--nf-color-fg-accent, var(--color-accent))',
            fontSize: 'var(--nf-text-sm, 0.875rem)',
          }}
        >
          {t('common.back')}
        </Link>
      </div>

      <h1
        style={{
          fontFamily: 'var(--nf-font-display, var(--font-display))',
          fontSize: 'var(--nf-text-2xl, 1.5rem)',
          margin: 0,
        }}
      >
        {t('workspaces.detail')}
      </h1>

      <div
        style={{
          padding: 'var(--nf-space-4, 1rem)',
          border: '1px solid var(--nf-color-border, var(--color-hairline))',
          borderRadius: 'var(--nf-radius-md, 0.375rem)',
        }}
      >
        <div style={labelStyle}>{t('workspaces.name')}</div>
        <div style={valueStyle}>{workspace.name}</div>

        <div style={labelStyle}>{t('workspaces.slug')}</div>
        <div style={valueStyle}>{workspace.slug}</div>

        <div style={labelStyle}>{t('workspaces.members')}</div>
        <div style={valueStyle}>{workspace.memberCount}</div>

        <div style={labelStyle}>{t('workspaces.projects')}</div>
        <div style={valueStyle}>{workspace.projectCount}</div>

        <div style={labelStyle}>{t('workspaces.status')}</div>
        <div style={valueStyle}>
          <span
            style={{
              ...badgeBase,
              background: workspace.enabled
                ? 'var(--nf-color-bg-success, rgba(0,128,0,0.1))'
                : 'var(--nf-color-bg-danger, rgba(255,0,0,0.1))',
              color: workspace.enabled
                ? 'var(--nf-color-fg-success, green)'
                : 'var(--nf-color-fg-danger, red)',
            }}
          >
            {workspace.enabled ? t('workspaces.enabled') : t('workspaces.disabled')}
          </span>
        </div>

        <div style={labelStyle}>{t('workspaces.createdAt')}</div>
        <div style={valueStyle}>{formatTimestamp(workspace.createdAt, t('common.never'))}</div>
      </div>

      <div>
        <Button
          variant="default"
          disabled={actionLoading}
          onClick={() => void handleToggleEnabled()}
        >
          {workspace.enabled ? t('workspaces.suspend') : t('workspaces.enable')}
        </Button>
      </div>
    </div>
  );
}

export const Route = createFileRoute('/_authenticated/admin/workspaces_/$wsId')({
  component: WorkspaceDetailPage,
});
