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
import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { createFileRoute, Link, useLocation, useNavigate } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import AuthCard from '../components/auth-card';
import { authStore } from '../features/auth/auth-store';
import { useCapsLockHint } from '../features/auth/use-caps-lock-hint';
import { type MeResponse, userFromMe } from '../features/auth/user-from-me';
import { takeOidcRedirect } from '../features/oauth/oidc-redirect';
import { apiRequest } from '../lib/api';
import { type AuthErrorI18nKey, mapAuthThrown } from '../lib/auth-errors';
import { refreshAccessToken } from '../lib/sdk';
import { useSubmitGuard } from '../lib/use-submit-guard';

const RECOVERY_MIN_LEN = 8;
const RECOVERY_MAX_LEN = 20;

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
  const [totpCode, setTotpCode] = useState('');
  const [useRecovery, setUseRecovery] = useState(false);
  const [recoveryCode, setRecoveryCode] = useState('');
  const totpGuard = useSubmitGuard();
  const capsHint = useCapsLockHint();

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

  const handleTotpSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (useRecovery ? recoveryCode.trim().length < RECOVERY_MIN_LEN : totpCode.length !== 6) return;
    if (totpGuard.guard()) return;
    setError(null);
    try {
      const data = await apiRequest(
        (client) =>
          client.POST('/auth/login/totp', {
            body: useRecovery
              ? { challengeToken, recoveryCode: recoveryCode.trim() }
              : { challengeToken, code: totpCode },
          }),
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
    const recoveryRemaining = RECOVERY_MIN_LEN - recoveryCode.trim().length;
    const totpStatus = useRecovery
      ? recoveryRemaining > 0
        ? t('login.recovery_helper')
        : t('login.totp_status_awaiting')
      : totpCode.length < 6
        ? t('login.totp_status_need_digits')
        : t('login.totp_status_awaiting');
    const totpError = error ? t(error) : null;
    return (
      <AuthCard>
        <form
          onSubmit={(e) => {
            void handleTotpSubmit(e);
          }}
          noValidate
          className="aw-stack aw-stack-5"
        >
          <h1 className="aw-page-title">{t('login.totp_title')}</h1>
          <p className="aw-flush aw-muted aw-text-sm">{t('login.totp_instructions')}</p>
          {useRecovery ? (
            <FormField
              label={t('login.recovery_code')}
              required
              description={totpStatus}
              {...(totpError ? { error: totpError } : {})}
            >
              {(control) => (
                <Input
                  {...control}
                  autoComplete="one-time-code"
                  maxLength={RECOVERY_MAX_LEN}
                  value={recoveryCode}
                  onChange={(e) => {
                    setRecoveryCode(e.target.value.toUpperCase().slice(0, RECOVERY_MAX_LEN));
                  }}
                  onKeyDown={capsHint.handlers.onKeyDown}
                  onFocus={capsHint.handlers.onFocus}
                  onBlur={capsHint.handlers.onBlur}
                  autoFocus
                />
              )}
            </FormField>
          ) : (
            <FormField
              label={t('login.totp_code')}
              required
              description={totpStatus}
              {...(totpError ? { error: totpError } : {})}
            >
              {(control) => (
                <Input
                  {...control}
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  pattern="[0-9]{6}"
                  maxLength={6}
                  value={totpCode}
                  onChange={(e) => {
                    setTotpCode(e.target.value.replace(/\D/g, '').slice(0, 6));
                  }}
                  onKeyDown={capsHint.handlers.onKeyDown}
                  onFocus={capsHint.handlers.onFocus}
                  onBlur={capsHint.handlers.onBlur}
                  autoFocus
                />
              )}
            </FormField>
          )}
          {capsHint.capsLockOn ? (
            <output aria-live="polite" className="aw-flush aw-muted aw-text-xs">
              {t('login.caps_lock_on')}
            </output>
          ) : null}
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              setUseRecovery((v) => !v);
              setError(null);
            }}
            disabled={totpGuard.submitting}
          >
            {useRecovery ? t('login.totp_use_code') : t('login.totp_use_recovery')}
          </Button>
          <Button
            type="submit"
            variant="primary"
            disabled={
              totpGuard.submitting ||
              (useRecovery ? recoveryCode.trim().length < RECOVERY_MIN_LEN : totpCode.length !== 6)
            }
          >
            {useRecovery ? t('login.recovery_submit') : t('login.totp_submit')}
          </Button>
        </form>
      </AuthCard>
    );
  }

  return (
    <AuthCard>
      <div className="aw-stack aw-stack-5" role={status === 'error' ? 'alert' : undefined}>
        <h1 className="aw-page-title">{t('login.oidc_title')}</h1>
        {status === 'verifying' ? (
          <p className="aw-flush aw-muted">{t('login.oidc_verifying')}</p>
        ) : (
          <>
            <p className="aw-flush aw-error">{error ? t(error) : t('errors.generic')}</p>
            <Link to="/login" className="aw-link">
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
