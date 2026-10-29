/**
 * /invite/$token — public route stub for accepting workspace invite links.
 *
 * The lazy component below uses `useSuspenseQuery(useInviteInfoQuery)` to
 * fetch invite info. Without a `beforeLoad` prefetch the token-not-found
 * error throws inside Suspense and bubbles to the route's error boundary
 * after the component has already mounted, briefly flashing AuthCard
 * chrome before swapping to the error UI.
 *
 * Prefetching here lands the query result (or its error) in the cache
 * before the lazy component mounts, so the suspense boundary resolves
 * synchronously and the error path renders without a chrome flash.
 *
 * The `.catch()` is intentional: we want the error to flow through to
 * the lazy component's own ErrorBoundary, not to abort `beforeLoad`
 * (which would surface the route-level fallback instead of the branded
 * one).
 */

import { buttonClassName } from '@nodate-flow/ui/primitives/button';
import { createFileRoute, Link } from '@tanstack/react-router';
import { AlertCircle } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import AuthCard from '../components/auth/auth-card';
import { inviteInfoQueryOptions } from '../features/workspaces/invite-api';
import { resolveInviteErrorKey } from '../features/workspaces/invite-errors';
import styles from './invite.module.css';

function InviteErrorComponent({ error }: { error: unknown }): ReactElement {
  const { t } = useTranslation('common');
  return (
    <AuthCard>
      <main
        aria-label={t('workspaces.invites.error_title')}
        className={`${styles.main} ${styles['main--center']}`}
      >
        <AlertCircle size={44} aria-hidden="true" className={styles.errorIcon} />
        <h1 className={`${styles.title} ${styles['title--semibold']}`}>
          {t('workspaces.invites.error_title')}
        </h1>
        <p className={styles.text}>{t(resolveInviteErrorKey(error))}</p>
        <Link to="/" className={buttonClassName()}>
          {t('workspaces.invites.error_back')}
        </Link>
      </main>
    </AuthCard>
  );
}

export const Route = createFileRoute('/invite/$token')({
  beforeLoad: async ({ params, context: { queryClient } }) => {
    await queryClient.ensureQueryData(inviteInfoQueryOptions(params.token)).catch(() => {
      // Swallow: the lazy component re-runs the same query via
      // useSuspenseQuery and renders its branded error UI.
    });
  },
  errorComponent: InviteErrorComponent,
});
