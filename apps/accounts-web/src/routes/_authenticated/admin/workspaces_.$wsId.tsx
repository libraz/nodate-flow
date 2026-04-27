/**
 * /admin/workspaces/$wsId -- Workspace detail with suspend/enable.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { confirmAction } from '@nodate-flow/ui/primitives/confirm/action';
import { Link, createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { sdk } from '../../../lib/sdk';

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
  color: 'var(--nf-color-fg-muted)',
  fontSize: 'var(--nf-text-xs)',
  marginBlockEnd: 'var(--nf-space-1)',
};

const valueStyle: React.CSSProperties = {
  fontSize: 'var(--nf-text-sm)',
  marginBlockEnd: 'var(--nf-space-3)',
};

const badgeBase: React.CSSProperties = {
  display: 'inline-block',
  padding: '0.125rem 0.5rem',
  borderRadius: 'var(--nf-radius-pill)',
  fontSize: 'var(--nf-text-xs)',
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

    void sdk.GET('/admin/workspaces/{wsId}', { params: { path: { wsId } } }).then((result) => {
      if (cancelled) return;
      if (result.error || !result.data) {
        setError(t('errors.generic'));
        setLoading(false);
        return;
      }
      setWorkspace(result.data as WorkspaceDetail);
      setLoading(false);
    });

    return () => {
      cancelled = true;
    };
  }, [wsId, t]);

  const handleToggleEnabled = async () => {
    if (!workspace) return;
    const action = workspace.enabled ? t('workspaces.suspend') : t('workspaces.enable');
    const ok = await confirmAction({
      tone: 'danger',
      message: action,
      confirmLabel: action,
    });
    if (!ok) return;

    setActionLoading(true);
    await sdk.PATCH('/admin/workspaces/{wsId}', {
      params: { path: { wsId } },
      body: { enabled: !workspace.enabled },
    });
    setActionLoading(false);

    // Refetch
    const result = await sdk.GET('/admin/workspaces/{wsId}', { params: { path: { wsId } } });
    if (result.data) {
      setWorkspace(result.data as WorkspaceDetail);
    }
  };

  if (loading) {
    return <p style={{ color: 'var(--nf-color-fg-muted)' }}>{t('common.loading')}</p>;
  }

  if (error || !workspace) {
    return (
      <div>
        <Link to="/admin/workspaces" style={{ color: 'var(--nf-color-accent)' }}>
          {t('common.back_to_workspaces')}
        </Link>
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
        <Link
          to="/admin/workspaces"
          style={{
            color: 'var(--nf-color-accent)',
            fontSize: 'var(--nf-text-sm)',
          }}
        >
          {t('common.back_to_workspaces')}
        </Link>
      </div>

      <h1
        style={{
          fontFamily: 'var(--nf-font-sans)',
          fontSize: 'var(--nf-text-2xl)',
          margin: 0,
        }}
      >
        {t('workspaces.detail')}
      </h1>

      <div
        style={{
          padding: 'var(--nf-space-4)',
          border: '1px solid var(--nf-color-border)',
          borderRadius: 'var(--nf-radius-md)',
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
              background: workspace.enabled ? 'var(--nf-color-success)' : 'var(--nf-color-danger)',
              color: workspace.enabled ? 'var(--nf-color-success)' : 'var(--nf-color-danger)',
            }}
          >
            {workspace.enabled ? t('workspaces.enabled') : t('workspaces.disabled')}
          </span>
        </div>

        <div style={labelStyle}>{t('workspaces.created_at')}</div>
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
