/**
 * /workspaces -- List workspaces the caller belongs to. Each row links to
 * the per-workspace settings page where timezone and country can be edited.
 */

import type { components } from '@nodate-flow/sdk';
import VisuallyHidden from '@nodate-flow/ui/a11y/visually-hidden';
import { useFirstLoadStagger } from '@nodate-flow/ui/hooks/use-first-load-stagger';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { createFileRoute, Link } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import AuthCard from '../../components/auth-card';
import { apiRequest } from '../../lib/api';

import styles from './workspaces.module.css';

type Workspace = components['schemas']['Workspace'];
type WorkspacesListOutputBody = components['schemas']['WorkspacesListOutputBody'];

function WorkspacesPage(): ReactElement {
  const { t } = useTranslation('auth');
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const stagger = useFirstLoadStagger();

  /** Static keys per known role; an unknown role falls back to its raw value. */
  const roleLabel = (role: Workspace['role']): string => {
    switch (role) {
      case 'owner':
        return t('workspaces.roles.owner');
      case 'admin':
        return t('workspaces.roles.admin');
      case 'member':
        return t('workspaces.roles.member');
      case 'guest':
        return t('workspaces.roles.guest');
      default:
        return role ?? '';
    }
  };

  useEffect(() => {
    let cancelled = false;
    void apiRequest(
      (client) => client.GET('/workspaces', { params: { query: { limit: 50, offset: 0 } } }),
      'Failed to load workspaces',
      { onError: 'empty', empty: null },
    ).then((result) => {
      if (cancelled) return;
      if (result === null) {
        setError(t('errors.generic'));
        setLoading(false);
        return;
      }
      const body = result as WorkspacesListOutputBody;
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
        <div className="aw-stack aw-stack-2" aria-busy="true">
          <VisuallyHidden>{t('workspaces.loading')}</VisuallyHidden>
          <Skeleton className={styles.rowSkeleton} />
          <Skeleton className={styles.rowSkeleton} />
        </div>
      ) : workspaces.length === 0 ? (
        <EmptyState title={t('workspaces.empty')} titleAs="p" />
      ) : (
        <ul className="aw-list">
          {workspaces.map((ws, index) => (
            <li key={ws.id} className={motion.enter} style={stagger(index)}>
              <Link to="/workspaces/$wsId" params={{ wsId: ws.id }} className="aw-link-row">
                <div className="aw-stack aw-stack-1">
                  <span className="aw-weight-semibold">{ws.name}</span>
                  <span className="aw-muted aw-text-xs">
                    {ws.slug}
                    <span className="aw-bullet">·</span>
                    {ws.timezone}
                    {ws.country ? (
                      <>
                        <span className="aw-bullet">·</span>
                        {ws.country}
                      </>
                    ) : null}
                  </span>
                </div>
                <span className={styles.role}>{roleLabel(ws.role)}</span>
              </Link>
            </li>
          ))}
        </ul>
      )}

      <Link to="/profile" className="aw-muted aw-text-sm">
        {t('workspaces.profile_link')}
      </Link>
    </AuthCard>
  );
}

export const Route = createFileRoute('/_authenticated/workspaces')({
  component: WorkspacesPage,
});
