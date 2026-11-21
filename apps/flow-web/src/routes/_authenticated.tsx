/**
 * Pathless layout route guarding all authenticated pages. Renders the
 * AppShell around its children. While bootstrap is in progress we render a
 * quiet skeleton that only fades in if the probe is slow. On unauthenticated state
 * the user is redirected to /login.
 */

import { isSafeRedirect } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import PageSkeleton from '@nodate-flow/ui/primitives/page-skeleton';
import { createFileRoute, Outlet } from '@tanstack/react-router';
import { type ReactElement, useEffect } from 'react';
import { useTranslation } from 'react-i18next';

import AppShell from '../components/layout/app-shell';
import ShellOverlays from '../components/layout/shell-overlays';
import NotFound from '../components/not-found';
import { selectIsAuthenticated, useAuth } from '../features/auth/auth-store';
import { useAuthBootstrap } from '../features/auth/use-auth-bootstrap';
import AiSuggestionsDock from '../features/glass-dock/glass-dock';
import { ACCOUNTS_WEB_BASE, accountsWebUrl } from '../lib/accounts-web-url';
import styles from './_authenticated.module.css';

function AuthenticatedLayout(): ReactElement | null {
  const { status, retry } = useAuthBootstrap();
  const isAuthenticated = useAuth(selectIsAuthenticated);

  useEffect(() => {
    if (status === 'unauthenticated' && !isAuthenticated) {
      const target = `${accountsWebUrl('/login')}?redirect=${encodeURIComponent(window.location.href)}`;
      // The accounts-web base comes from build-time config, so it is the allowlist
      // here; the check still stops a malformed value from turning into
      // a navigation to an unexpected origin or scheme.
      if (isSafeRedirect(target, window.location.origin, [ACCOUNTS_WEB_BASE])) {
        window.location.href = target;
      }
    }
  }, [status, isAuthenticated]);

  if (status === 'loading') return <PageSkeleton className={styles.boot} />;
  // We could not reach the auth service, which says nothing about whether
  // the session is still valid. Offer the retry instead of bouncing the
  // user to a login screen they may not even need.
  if (status === 'offline') return <SessionUnreachable onRetry={retry} />;
  if (!isAuthenticated) return null;

  return (
    <AppShell>
      <Outlet />
      <AiSuggestionsDock />
      <ShellOverlays />
    </AppShell>
  );
}

/**
 * Shown when the session probe could not reach the auth service on load.
 * Deliberately not a redirect: the session may well still be valid, and
 * a redirect would throw away whatever the user had on screen.
 */
function SessionUnreachable({ onRetry }: { onRetry: () => void }): ReactElement {
  const { t } = useTranslation('common');
  return (
    <main aria-label={t('auth.offline.title')} className={styles.offline}>
      <h1 className={styles.offlineTitle}>{t('auth.offline.title')}</h1>
      <p className={styles.offlineText}>{t('auth.offline.description')}</p>
      <Button type="button" variant="primary" onClick={onRetry}>
        {t('auth.offline.retry')}
      </Button>
    </main>
  );
}

function AuthenticatedNotFound(): ReactElement {
  return (
    <AppShell>
      <NotFound />
      <AiSuggestionsDock />
      <ShellOverlays />
    </AppShell>
  );
}

export const Route = createFileRoute('/_authenticated')({
  component: AuthenticatedLayout,
  notFoundComponent: AuthenticatedNotFound,
});
