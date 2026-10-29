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

import Button, { buttonClassName } from '@nodate-flow/ui/primitives/button';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
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
import styles from './invite.module.css';

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
      <main aria-label={title} className={styles.main}>
        <h1 className={styles.title}>{title}</h1>
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
        <div className={styles.skeleton} aria-busy="true">
          <Skeleton className={styles.skeletonLine} />
          <Skeleton className={styles.skeletonLine} />
          <Skeleton className={styles.skeletonAction} />
        </div>
      </InviteAuthCardBody>
    );
  }

  // If not logged in, prompt the user to sign in first
  if (!isAuthenticated) {
    return (
      <InviteAuthCardBody workspaceName={info.workspaceName}>
        <p className={styles.text}>{t('workspaces.invites.login_required')}</p>
        <Link
          to="/login"
          search={{ returnTo: `/invite/${token}` }}
          className={buttonClassName({ variant: 'primary' })}
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
      <p className={styles.text}>
        {t('workspaces.invites.join_description', {
          workspace: info.workspaceName,
          role: roleLabel(info.role),
        })}
      </p>
      {info.expiresAt ? (
        <p className={styles.textSm}>
          {t('workspaces.invites.expires_at', {
            date: formatEpochDateTime(info.expiresAt, i18n.resolvedLanguage ?? 'en'),
          })}
        </p>
      ) : null}
      {error ? (
        <p role="alert" className={styles.alert}>
          {error}
        </p>
      ) : null}
      <div className={styles.actions}>
        <Link to="/" className={buttonClassName()}>
          {t('workspaces.form.cancel')}
        </Link>
        <Button
          variant="primary"
          disabled={submitGuard.submitting}
          aria-busy={submitGuard.submitting || undefined}
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
