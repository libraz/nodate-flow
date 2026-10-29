/**
 * /magic-link -- passwordless login callback.
 *
 * Auth-api emails point here with `?token=<opaque one-time token>`.
 * The page consumes the token, then either completes the session or
 * asks for the account's configured TOTP second factor.
 */

import type { components } from '@nodate-flow/sdk';
import Spinner from '@nodate-flow/ui/primitives/spinner';
import { createFileRoute, Link, useNavigate, useSearch } from '@tanstack/react-router';
import { type ReactElement, useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import AuthCard from '../components/auth-card';
import { authStore } from '../features/auth/auth-store';
import TotpChallengeForm, { type TotpCredential } from '../features/auth/totp-challenge-form';
import { type MeResponse, userFromMe } from '../features/auth/user-from-me';
import { apiRequest } from '../lib/api';
import { type AuthErrorI18nKey, mapAuthThrown } from '../lib/auth-errors';
import { useSubmitGuard } from '../lib/use-submit-guard';

const VERIFY_CACHE_MS = 60_000;

type LoginResponse = components['schemas']['LoginBody'];
type TotpResponse = components['schemas']['AuthTokens'];

export interface MagicLinkSearch {
  token?: string;
}

type VerifyResult = { ok: true; body: LoginResponse } | { ok: false; error: unknown };

const verifyPromises = new Map<string, Promise<VerifyResult>>();

function verifyMagicLink(token: string): Promise<VerifyResult> {
  const cached = verifyPromises.get(token);
  if (cached) return cached;

  const promise: Promise<VerifyResult> = apiRequest(
    (client) => client.GET('/auth/magic-link/verify', { params: { query: { token } } }),
    'The sign-in link could not be verified',
  )
    .then((data): VerifyResult => ({ ok: true, body: data as LoginResponse }))
    .catch((error: unknown): VerifyResult => ({ ok: false, error }))
    .finally(() => {
      window.setTimeout(() => {
        verifyPromises.delete(token);
      }, VERIFY_CACHE_MS);
    });
  verifyPromises.set(token, promise);
  return promise;
}

export function MagicLinkPage(): ReactElement {
  const { t } = useTranslation('auth');
  const navigate = useNavigate();
  const { token } = useSearch({ from: '/magic-link' });
  const [status, setStatus] = useState<'verifying' | 'totp' | 'error'>('verifying');
  const [error, setError] = useState<AuthErrorI18nKey | null>(null);
  const [challengeToken, setChallengeToken] = useState<string | null>(null);
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
      void navigate({ to: '/profile', replace: true });
    },
    [navigate],
  );

  useEffect(() => {
    let cancelled = false;

    async function run(): Promise<void> {
      if (!token) {
        setError('auth:errors.magic_link_malformed');
        setStatus('error');
        return;
      }
      setStatus('verifying');
      setError(null);
      try {
        const result = await verifyMagicLink(token);
        if (cancelled) return;
        if (!result.ok) {
          setError(mapAuthThrown(result.error));
          setStatus('error');
          return;
        }
        if (result.body.step === 'totp_required') {
          setChallengeToken(result.body.challengeToken ?? '');
          setStatus('totp');
          return;
        }
        if (!result.body.accessToken) {
          setError('auth:errors.generic');
          setStatus('error');
          return;
        }
        await completeSignIn(result.body.accessToken);
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
  }, [token, completeSignIn]);

  const handleTotpSubmit = async (credential: TotpCredential): Promise<void> => {
    if (challengeToken == null) return;
    if (totpGuard.guard()) return;
    setError(null);
    try {
      const data = await apiRequest(
        (client) => client.POST('/auth/login/totp', { body: { challengeToken, ...credential } }),
        'Sign-in failed',
      );
      const totp = data as TotpResponse;
      await completeSignIn(totp.accessToken);
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
      <div className="aw-stack aw-stack-5">
        <h1 className="aw-page-title">{t('login.magic_link_title')}</h1>
        {status === 'verifying' ? (
          <p className="aw-flush aw-row aw-row-2 aw-muted aw-text-sm">
            <Spinner size="sm" label={t('login.magic_link_verifying')} />
            <span aria-hidden="true">{t('login.magic_link_verifying')}</span>
          </p>
        ) : (
          <>
            <p role="alert" className="aw-error">
              {t(error ?? 'auth:errors.generic')}
            </p>
            <Link to="/login" className="aw-link aw-text-sm">
              {t('login.magic_link_back')}
            </Link>
          </>
        )}
      </div>
    </AuthCard>
  );
}

export const Route = createFileRoute('/magic-link')({
  validateSearch: (search: Record<string, unknown>): MagicLinkSearch => {
    const token = typeof search.token === 'string' ? search.token : undefined;
    return token ? { token } : {};
  },
  component: MagicLinkPage,
});
