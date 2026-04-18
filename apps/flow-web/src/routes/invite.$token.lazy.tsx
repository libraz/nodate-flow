/**
 * /invite/$token — accept a workspace invite link (lazy component).
 *
 * Flow:
 * 1. Fetch invite info (public, no auth needed) via useSuspenseQuery
 * 2. If user is not authenticated, redirect to login with returnTo
 * 3. If authenticated, show confirmation card with "Join" button
 * 4. On accept, navigate to the joined workspace
 */

import Button from '@nodate-flow/ui/primitives/button';
import { Link, createLazyFileRoute, getRouteApi, useNavigate } from '@tanstack/react-router';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import AuthCard from '../components/auth/auth-card';
import { selectIsAuthenticated, useAuth } from '../features/auth/auth-store';
import { useAcceptInvite, useInviteInfoQuery } from '../features/workspaces/invite-api';

const routeApi = getRouteApi('/invite/$token');

function InviteAcceptPage(): ReactElement {
  const { t } = useTranslation('common');
  const navigate = useNavigate();
  const { token } = routeApi.useParams();
  const isAuthenticated = useAuth(selectIsAuthenticated);
  const acceptInvite = useAcceptInvite();
  const [accepting, setAccepting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const { data: info } = useInviteInfoQuery(token);

  // If not logged in, prompt the user to sign in first
  if (!isAuthenticated) {
    return (
      <AuthCard>
        <div style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}>
          <h1
            style={{
              fontFamily: 'var(--nf-font-display, var(--font-display))',
              fontSize: 'var(--nf-text-2xl, 1.5rem)',
              margin: 0,
            }}
          >
            {t('workspaces.invites.join_title', { workspace: info.workspaceName })}
          </h1>
          <p style={{ margin: 0, color: 'var(--nf-color-fg-muted, var(--color-muted))' }}>
            {t('workspaces.invites.login_required')}
          </p>
          <Link
            to="/login"
            style={{
              display: 'inline-block',
              padding: '0.625rem 1.25rem',
              borderRadius: '0.5rem',
              background: 'var(--color-accent, #9b59b6)',
              color: 'var(--color-on-accent, #fff)',
              textDecoration: 'none',
              textAlign: 'center',
              fontWeight: 500,
            }}
          >
            {t('workspaces.invites.go_to_login')}
          </Link>
        </div>
      </AuthCard>
    );
  }

  const handleAccept = async (): Promise<void> => {
    setAccepting(true);
    setError(null);
    try {
      const result = await acceptInvite.mutateAsync(token);
      void navigate({ to: '/workspaces/$id', params: { id: result.workspaceId }, replace: true });
    } catch {
      setError(t('workspaces.invites.accept_failed'));
    } finally {
      setAccepting(false);
    }
  };

  const roleLabel = (role: string): string => {
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
        return role;
    }
  };

  return (
    <AuthCard>
      <div style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}>
        <h1
          style={{
            fontFamily: 'var(--nf-font-display, var(--font-display))',
            fontSize: 'var(--nf-text-2xl, 1.5rem)',
            margin: 0,
          }}
        >
          {t('workspaces.invites.join_title', { workspace: info.workspaceName })}
        </h1>
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted, var(--color-muted))' }}>
          {t('workspaces.invites.join_description', {
            workspace: info.workspaceName,
            role: roleLabel(info.role),
          })}
        </p>
        {info.expiresAt ? (
          <p
            style={{
              margin: 0,
              fontSize: 'var(--nf-text-sm, 0.875rem)',
              color: 'var(--nf-color-fg-muted, var(--color-muted))',
            }}
          >
            {t('workspaces.invites.expires_at', {
              date: new Intl.DateTimeFormat(undefined, {
                dateStyle: 'medium',
                timeStyle: 'short',
              }).format(new Date(info.expiresAt)),
            })}
          </p>
        ) : null}
        {error ? (
          <p
            role="alert"
            style={{
              margin: 0,
              color: 'var(--nf-color-fg-danger, var(--color-danger))',
              fontSize: 'var(--nf-text-sm, 0.875rem)',
            }}
          >
            {error}
          </p>
        ) : null}
        <div style={{ display: 'flex', gap: '0.75rem' }}>
          <Link
            to="/"
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              padding: '0.625rem 1.25rem',
              borderRadius: '0.5rem',
              border: '1px solid var(--color-border)',
              background: 'transparent',
              color: 'var(--color-fg)',
              textDecoration: 'none',
            }}
          >
            {t('workspaces.form.cancel')}
          </Link>
          <Button
            variant="primary"
            disabled={accepting}
            onClick={() => {
              void handleAccept();
            }}
          >
            {accepting ? t('workspaces.invites.joining') : t('workspaces.invites.join_button')}
          </Button>
        </div>
      </div>
    </AuthCard>
  );
}

export const Route = createLazyFileRoute('/invite/$token')({
  component: InviteAcceptPage,
});
