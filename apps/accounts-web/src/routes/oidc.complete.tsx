/**
 * /oidc/complete -- browser landing page for auth-api OIDC callbacks.
 *
 * auth-api redirects here after the IdP callback. Successful single-factor
 * OIDC has already set the httpOnly refresh cookie, so this page refreshes
 * an access token and loads /me. TOTP-required OIDC carries only a short-lived
 * challenge token in the URL fragment and finishes through /auth/login/totp.
 *
 * The page the user actually asked for is picked up from
 * {@link takeOidcRedirect}, which re-validates it: a deep link into the
 * product frontend must survive the provider hand-off the same way it
 * survives a password sign-in.
 */

import type { components } from '@nodate-flow/sdk';
import Spinner from '@nodate-flow/ui/primitives/spinner';
import { createFileRoute, Link, useLocation, useNavigate } from '@tanstack/react-router';
import { type ReactElement, useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import AuthCard from '../components/auth-card';
import { authStore } from '../features/auth/auth-store';
import TotpChallengeForm, { type TotpCredential } from '../features/auth/totp-challenge-form';
import { type MeResponse, userFromMe } from '../features/auth/user-from-me';
import { takeOidcRedirect } from '../features/oauth/oidc-redirect';
import { apiRequest } from '../lib/api';
import { type AuthErrorI18nKey, mapAuthThrown } from '../lib/auth-errors';
import { refreshAccessToken } from '../lib/sdk';
import { useSubmitGuard } from '../lib/use-submit-guard';

type TotpResponse = components['schemas']['AuthTokens'];

function readOIDCFragment(hash: string): { step: string; challengeToken: string } {
  const trimmed = hash.startsWith('#') ? hash.slice(1) : hash;
  const params = new URLSearchParams(trimmed);
  return {
    step: params.get('step') ?? '',
    challengeToken: params.get('challengeToken') ?? '',
  };
}

/**
 * Rewrites the current history entry without its fragment, using
 * replaceState so the fragment is not merely hidden behind a new entry
 * the back button can return to.
 */
export function scrubOIDCFragment(): void {
  if (!window.location.hash) return;
  window.history.replaceState(
    window.history.state,
    '',
    `${window.location.pathname}${window.location.search}`,
  );
}

export function OIDCCompletePage(): ReactElement {
  const { t } = useTranslation('auth');
  const navigate = useNavigate();
  const location = useLocation();
  const [status, setStatus] = useState<'verifying' | 'totp' | 'error'>('verifying');
  const [error, setError] = useState<AuthErrorI18nKey | null>(null);
  const [challengeToken, setChallengeToken] = useState('');
  const totpGuard = useSubmitGuard();

  const completeSignIn = useCallback(
    async (accessToken: string): Promise<void> => {
      authStore.getState().setAccessToken(accessToken);
      let data: unknown;
      try {
        data = await apiRequest((client) => client.GET('/me'), 'Failed to load the signed-in user');
      } catch (meError) {
        authStore.getState().clearSession();
        setError(mapAuthThrown(meError));
        setStatus('error');
        return;
      }
      authStore.getState().setSession(accessToken, userFromMe(data as MeResponse));
      // Consume the target the sign-in started from. It is re-validated
      // inside takeOidcRedirect, so an unsafe or expired value lands the
      // user on /profile exactly as if none had been given.
      const target = takeOidcRedirect();
      if (target) {
        window.location.href = target;
        return;
      }
      void navigate({ to: '/profile', replace: true });
    },
    [navigate],
  );

  useEffect(() => {
    let cancelled = false;
    const hash = location.hash || window.location.hash;
    const fragment = readOIDCFragment(hash);
    // Take the fragment out of the address bar as soon as it has been
    // read. It carries the step-up challenge, and a fragment survives in
    // history, in a restored session, and in anything the user copies
    // out of the URL bar long after the login it authorised finished.
    // The challenge is already in component state by the time this runs.
    scrubOIDCFragment();

    async function run(): Promise<void> {
      setStatus('verifying');
      setError(null);
      if (fragment.step === 'totp_required') {
        if (!fragment.challengeToken) {
          setError('auth:errors.generic');
          setStatus('error');
          return;
        }
        setChallengeToken(fragment.challengeToken);
        setStatus('totp');
        return;
      }
      if (fragment.step !== 'complete') {
        setError('auth:errors.generic');
        setStatus('error');
        return;
      }
      try {
        const token = await refreshAccessToken();
        if (cancelled) return;
        if (!token) {
          setError('auth:errors.token_refresh_invalid');
          setStatus('error');
          return;
        }
        await completeSignIn(token);
      } catch (err) {
        if (!cancelled) {
          setError(mapAuthThrown(err));
          setStatus('error');
        }
      }
    }

    void run();
    return () => {
      cancelled = true;
    };
  }, [location.hash, completeSignIn]);

  const handleTotpSubmit = async (credential: TotpCredential): Promise<void> => {
    if (totpGuard.guard()) return;
    setError(null);
    try {
      const data = await apiRequest(
        (client) => client.POST('/auth/login/totp', { body: { challengeToken, ...credential } }),
        'Sign-in failed',
      );
      await completeSignIn((data as TotpResponse).accessToken);
    } catch (err) {
      setError(mapAuthThrown(err));
    } finally {
      totpGuard.end();
    }
  };

  if (status === 'totp') {
    return (
      <AuthCard>
        <TotpChallengeForm
          className="aw-step-forward"
          error={error}
          submitting={totpGuard.submitting}
          onSubmit={(credential) => {
            void handleTotpSubmit(credential);
          }}
          onModeChange={() => {
            setError(null);
          }}
          cancel={
            <Link to="/login" className="aw-link aw-text-sm aw-self-center">
              {t('login.totp_cancel')}
            </Link>
          }
        />
      </AuthCard>
    );
  }

  return (
    <AuthCard>
      <div className="aw-stack aw-stack-5" role={status === 'error' ? 'alert' : undefined}>
        <h1 className="aw-page-title">{t('login.oidc_title')}</h1>
        {status === 'verifying' ? (
          <p className="aw-flush aw-row aw-row-2 aw-muted aw-text-sm">
            <Spinner size="sm" label={t('login.oidc_verifying')} />
            <span aria-hidden="true">{t('login.oidc_verifying')}</span>
          </p>
        ) : (
          <>
            <p className="aw-flush aw-error">{error ? t(error) : t('errors.generic')}</p>
            <Link to="/login" className="aw-link aw-text-sm">
              {t('login.magic_link_back')}
            </Link>
          </>
        )}
      </div>
    </AuthCard>
  );
}

export const Route = createFileRoute('/oidc/complete')({
  component: OIDCCompletePage,
});
