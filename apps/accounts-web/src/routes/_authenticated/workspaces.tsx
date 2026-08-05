/**
 * /workspaces -- List workspaces the caller belongs to. Each row links to
 * the per-workspace settings page where timezone and country can be edited.
 */

import type { components } from '@nodate-flow/sdk';
import { createFileRoute, Link } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import AuthCard from '../../components/auth-card';
import { sdk } from '../../lib/sdk';

type Workspace = components['schemas']['Workspace'];
type WorkspacesListOutputBody = components['schemas']['WorkspacesListOutputBody'];

function WorkspacesPage(): ReactElement {
  const { t } = useTranslation('auth');
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    void sdk.GET('/workspaces', { params: { query: { limit: 50, offset: 0 } } }).then((result) => {
      if (cancelled) return;
      if (result.error || !result.data) {
        setError(t('errors.generic'));
        setLoading(false);
        return;
      }
      const body = result.data as WorkspacesListOutputBody;
      setWorkspaces(body.workspaces ?? []);
      setLoading(false);
    });
    return () => {
      cancelled = true;
    };
  }, [t]);

  return (
    <AuthCard width="wide">
      <h1 className="aw-page-title">{t('workspaces.title')}</h1>

      {error ? (
        <p role="alert" className="aw-error">
          {error}
        </p>
      ) : null}

      {loading ? (
        <p className="aw-muted">{t('workspaces.loading')}</p>
      ) : workspaces.length === 0 ? (
        <p className="aw-muted">{t('workspaces.empty')}</p>
      ) : (
        <ul
          style={{
            listStyle: 'none',
            margin: 0,
            padding: 0,
            display: 'flex',
            flexDirection: 'column',
            gap: 'var(--nf-space-2)',
          }}
        >
          {workspaces.map((ws) => (
            <li key={ws.id}>
              <Link
                to="/workspaces/$wsId"
                params={{ wsId: ws.id }}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  padding: 'var(--nf-space-3) var(--nf-space-4)',
                  borderRadius: 'var(--nf-radius-md)',
                  border: '1px solid var(--nf-color-border)',
                  background: 'var(--nf-color-bg-elevated)',
                  textDecoration: 'none',
                  color: 'var(--nf-color-fg)',
                }}
              >
                <div>
                  <div style={{ fontWeight: 'var(--nf-weight-semibold)' }}>{ws.name}</div>
                  <div
                    style={{
                      fontSize: 'var(--nf-text-xs)',
                      color: 'var(--nf-color-fg-muted)',
                    }}
                  >
                    {ws.slug}
                    <span className="aw-bullet">·</span>
                    {ws.timezone}
                    {ws.country ? (
                      <>
                        <span className="aw-bullet">·</span>
                        {ws.country}
                      </>
                    ) : null}
                  </div>
                </div>
                <span
                  style={{
                    padding: 'var(--nf-space-0-5) var(--nf-space-2)',
                    borderRadius: 'var(--nf-radius-pill)',
                    fontSize: 'var(--nf-text-xs)',
                    background: 'var(--nf-color-accent-subtle)',
                    color: 'var(--nf-color-accent)',
                  }}
                >
                  {ws.role}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}

      <Link
        to="/profile"
        style={{
          fontSize: 'var(--nf-text-sm)',
          color: 'var(--nf-color-fg-muted)',
        }}
      >
        {t('workspaces.profile_link')}
      </Link>
    </AuthCard>
  );
}

export const Route = createFileRoute('/_authenticated/workspaces')({
  component: WorkspacesPage,
});
