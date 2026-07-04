/**
 * /magic-link -- passwordless login callback.
 *
 * Auth-api emails point here with `?token=<opaque one-time token>`.
 * The page consumes the token, then either completes the session or
 * asks for the account's configured TOTP second factor.
 */

import type { components } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { createFileRoute, Link, useNavigate, useSearch } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import AuthCard from '../components/auth-card';
import { type AuthUser, authStore } from '../features/auth/auth-store';
import { useCapsLockHint } from '../features/auth/use-caps-lock-hint';
import type { ProblemJson } from '../lib/api-error';
import { type AuthErrorI18nKey, mapAuthError, mapAuthThrown } from '../lib/auth-errors';
import { sdk } from '../lib/sdk';
import { useSubmitGuard } from '../lib/use-submit-guard';

const RECOVERY_MIN_LEN = 8;
const RECOVERY_MAX_LEN = 20;
const VERIFY_CACHE_MS = 60_000;

type LoginResponse = components['schemas']['LoginBody'];
type TotpResponse = components['schemas']['AuthTokens'];
type MeResponse = components['schemas']['MeBody'];

export interface MagicLinkSearch {
  token?: string;
}

type VerifyResult =
  | { ok: true; body: LoginResponse }
  | { ok: false; error: ProblemJson | undefined };

const verifyPromises = new Map<string, Promise<VerifyResult>>();

function verifyMagicLink(token: string): Promise<VerifyResult> {
  const cached = verifyPromises.get(token);
  if (cached) return cached;

  const promise: Promise<VerifyResult> = sdk
    .GET('/auth/magic-link/verify', { params: { query: { token } } })
    .then(({ data, error }) => {
      if (error || !data) {
        const result: VerifyResult = { ok: false, error: error as ProblemJson | undefined };
        return result;
      }
      const result: VerifyResult = { ok: true, body: data as LoginResponse };
      return result;
    })
    .finally(() => {
      window.setTimeout(() => {
        verifyPromises.delete(token);
      }, VERIFY_CACHE_MS);
    });
  verifyPromises.set(token, promise);
  return promise;
}

function userFromMe(me: MeResponse): AuthUser {
  return {
    id: me.id,
    email: me.email,
    displayName: me.displayName,
    locale: me.locale,
    timezone: me.timezone,
    country: me.country,
    themePreference: me.themePreference,
    isInstanceAdmin: me.isInstanceAdmin,
    avatarUrl: me.avatarUrl ?? null,
  };
}

export function MagicLinkPage(): ReactElement {
  const { t } = useTranslation('auth');
  const navigate = useNavigate();
  const { token } = useSearch({ from: '/magic-link' });
  const [status, setStatus] = useState<'verifying' | 'totp' | 'error'>('verifying');
  const [error, setError] = useState<AuthErrorI18nKey | null>(null);
  const [challengeToken, setChallengeToken] = useState<string | null>(null);
  const [totpCode, setTotpCode] = useState('');
  const [useRecovery, setUseRecovery] = useState(false);
  const [recoveryCode, setRecoveryCode] = useState('');
  const totpGuard = useSubmitGuard();
  const capsHint = useCapsLockHint();

  const completeSignIn = useCallback(
    async (accessToken: string): Promise<void> => {
      authStore.getState().setAccessToken(accessToken);
      const { data, error: meError } = await sdk.GET('/me');
      if (meError || !data) {
        authStore.getState().clearSession();
        setError(mapAuthError(meError as ProblemJson | undefined));
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
          setError(mapAuthError(result.error));
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

  const handleTotpSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (challengeToken == null) return;
    if (useRecovery ? recoveryCode.trim().length < RECOVERY_MIN_LEN : totpCode.length !== 6) return;
    if (totpGuard.guard()) return;
    setError(null);
    try {
      const { data, error: totpError } = await sdk.POST('/auth/login/totp', {
        body: useRecovery
          ? { challengeToken, recoveryCode: recoveryCode.trim() }
          : { challengeToken, code: totpCode },
      });
      if (totpError || !data) {
        setError(mapAuthError(totpError as ProblemJson | undefined));
        return;
      }
      const totp = data as TotpResponse;
      await completeSignIn(totp.accessToken);
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
          <Link to="/login" className="aw-link">
            {t('login.totp_cancel')}
          </Link>
        </form>
      </AuthCard>
    );
  }

  return (
    <AuthCard>
      <div className="aw-stack aw-stack-5">
        <h1 className="aw-page-title">{t('login.magic_link_title')}</h1>
        {status === 'verifying' ? (
          <p className="aw-flush aw-muted aw-text-sm" aria-live="polite">
            {t('login.magic_link_verifying')}
          </p>
        ) : (
          <>
            <p role="alert" className="aw-error">
              {t(error ?? 'auth:errors.generic')}
            </p>
            <Link to="/login" className="aw-link">
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
