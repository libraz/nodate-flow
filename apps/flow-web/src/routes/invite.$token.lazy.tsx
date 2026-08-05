/**
 * /invite/$token — accept a workspace invite link (lazy component).
 *
 * Flow:
 * 1. Fetch invite info (public, no auth needed) via useSuspenseQuery
 * 2. Wait for the session probe — this route sits outside
 *    `_authenticated`, so an already-signed-in visitor arrives with a
 *    refresh cookie but an empty in-memory store. Invite links are
 *    followed from mail and chat, which is exactly the case where that
 *    happens, so deciding "signed out" before the probe settles would
 *    turn the product's main onboarding path into a dead end.
 * 3. If the visitor really is signed out, point them at login with
 *    returnTo
 * 4. If authenticated, show confirmation card with "Join" button
 * 5. On accept, navigate to the joined workspace
 */

import Button from '@nodate-flow/ui/primitives/button';
import Spinner from '@nodate-flow/ui/primitives/spinner';
import { createLazyFileRoute, getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import { type ReactElement, type ReactNode, useState } from 'react';
import { useTranslation } from 'react-i18next';

import AuthCard from '../components/auth/auth-card';
import { selectIsAuthenticated, useAuth } from '../features/auth/auth-store';
import { useAuthBootstrap } from '../features/auth/use-auth-bootstrap';
import { useAcceptInvite, useInviteInfoQuery } from '../features/workspaces/invite-api';
import { resolveInviteErrorKey } from '../features/workspaces/invite-errors';
import { formatEpochDateTime } from '../lib/format';
import { useSubmitGuard } from '../lib/use-submit-guard';

type InviteRole = 'owner' | 'admin' | 'member' | 'guest';
const KNOWN_ROLES: ReadonlySet<InviteRole> = new Set(['owner', 'admin', 'member', 'guest']);

const routeApi = getRouteApi('/invite/$token');

/**
 * InviteAuthCardBody — shared chrome for the two invite states (signed-in
 * vs not). Wraps a labelled `<main>` landmark with the join-title heading
 * and lets each caller render its branch-specific copy + actions.
 */
function InviteAuthCardBody({
  workspaceName,
  children,
}: {
  workspaceName: string;
  children: ReactNode;
}): ReactElement {
  const { t } = useTranslation('common');
  const title = t('workspaces.invites.join_title', { workspace: workspaceName });
  return (
    <AuthCard>
      <main aria-label={title} style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}>
        <h1
          style={{
            fontFamily: 'var(--nf-font-sans)',
            fontSize: 'var(--nf-text-2xl)',
            margin: 0,
          }}
        >
          {title}
        </h1>
        {children}
      </main>
    </AuthCard>
  );
}

function InviteAcceptPage(): ReactElement {
  const { t, i18n } = useTranslation('common');
  const navigate = useNavigate();
  const { token } = routeApi.useParams();
  const isAuthenticated = useAuth(selectIsAuthenticated);
  const { status: authStatus } = useAuthBootstrap();
  const acceptInvite = useAcceptInvite();
  const submitGuard = useSubmitGuard();
  const [error, setError] = useState<string | null>(null);

  const { data: info } = useInviteInfoQuery(token);

  // Session state unknown so far: wait rather than guess. Skipped when
  // the store already holds a session (in-app navigation), so the extra
  // frame only costs the cold-open case it exists for.
  if (!isAuthenticated && authStatus === 'loading') {
    return (
      <InviteAuthCardBody workspaceName={info.workspaceName}>
        <Spinner label={t('common.loading')} />
      </InviteAuthCardBody>
    );
  }

  // If not logged in, prompt the user to sign in first
  if (!isAuthenticated) {
    return (
      <InviteAuthCardBody workspaceName={info.workspaceName}>
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>
          {t('workspaces.invites.login_required')}
        </p>
        <Link
          to="/login"
          search={{ returnTo: `/invite/${token}` }}
          style={{
            display: 'inline-block',
            padding: '0.625rem 1.25rem',
            borderRadius: '0.5rem',
            background: 'var(--nf-color-accent)',
            color: 'var(--nf-color-fg-on-accent)',
            textDecoration: 'none',
            textAlign: 'center',
            fontWeight: 500,
          }}
        >
          {t('workspaces.invites.go_to_login')}
        </Link>
      </InviteAuthCardBody>
    );
  }

  const handleAccept = async (): Promise<void> => {
    if (submitGuard.guard()) return;
    setError(null);
    try {
      const result = await acceptInvite.mutateAsync(token);
      void navigate({ to: '/workspaces/$id', params: { id: result.workspaceId }, replace: true });
    } catch (err) {
      setError(t(resolveInviteErrorKey(err)));
    } finally {
      submitGuard.end();
    }
  };

  const roleLabel = (role: string): string => {
    if (!KNOWN_ROLES.has(role as InviteRole)) return t('common.unknown_role');
    switch (role as InviteRole) {
      case 'owner':
        return t('workspaces.roles.owner');
      case 'admin':
        return t('workspaces.roles.admin');
      case 'member':
        return t('workspaces.roles.member');
      case 'guest':
        return t('workspaces.roles.guest');
    }
  };

  return (
    <InviteAuthCardBody workspaceName={info.workspaceName}>
      <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>
        {t('workspaces.invites.join_description', {
          workspace: info.workspaceName,
          role: roleLabel(info.role),
        })}
      </p>
      {info.expiresAt ? (
        <p
          style={{
            margin: 0,
            fontSize: 'var(--nf-text-sm)',
            color: 'var(--nf-color-fg-muted)',
          }}
        >
          {t('workspaces.invites.expires_at', {
            date: formatEpochDateTime(info.expiresAt, i18n.resolvedLanguage ?? 'en'),
          })}
        </p>
      ) : null}
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
      <div style={{ display: 'flex', gap: '0.75rem' }}>
        <Link
          to="/"
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            padding: '0.625rem 1.25rem',
            borderRadius: '0.5rem',
            border: '1px solid var(--nf-color-border)',
            background: 'transparent',
            color: 'var(--nf-color-fg)',
            textDecoration: 'none',
          }}
        >
          {t('workspaces.form.cancel')}
        </Link>
        <Button
          variant="primary"
          disabled={submitGuard.submitting}
          onClick={() => {
            void handleAccept();
          }}
        >
          {submitGuard.submitting
            ? t('workspaces.invites.joining')
            : t('workspaces.invites.join_button')}
        </Button>
      </div>
    </InviteAuthCardBody>
  );
}

export const Route = createLazyFileRoute('/invite/$token')({
  component: InviteAcceptPage,
});
