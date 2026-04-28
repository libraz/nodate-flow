/**
 * /login -- public page. Redirects to `?redirect=` target or / when
 * already authenticated.
 *
 * Uses React Hook Form + Zod for client-side validation. The TOTP
 * challenge step remains useState-based because it is a secondary
 * step triggered by the server, not a user-filled form.
 */

import { isSafeRedirect } from '@nodate-flow/sdk';
import { useZodForm } from '@nodate-flow/ui/hooks/use-zod-form';
import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { Link, createFileRoute, useNavigate, useSearch } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useCapabilities } from '../features/auth/use-capabilities';

import AuthCard from '../components/auth-card';
import { type LoginFormValues, loginSchema } from '../features/auth/auth-schemas';
import {
  type AuthUser,
  authStore,
  selectIsAuthenticated,
  useAuth,
} from '../features/auth/auth-store';
import PasswordInput from '../features/auth/password-input';
import { useCapsLockHint } from '../features/auth/use-caps-lock-hint';
import OAuthButtonRow from '../features/oauth/oauth-button-row';
import type { ProblemJson } from '../lib/api-error';
import { type AuthErrorI18nKey, mapAuthError, mapAuthThrown } from '../lib/auth-errors';
import { sdk } from '../lib/sdk';
import { useSubmitGuard } from '../lib/use-submit-guard';

/**
 * Recovery code character bounds enforced by the auth-api: codes are
 * generated as 10-character base32 strings but we accept up to 20 chars
 * defensively so a future longer-format rotation does not silently
 * truncate user input. Mirrored in the i18n helper text key so the copy
 * and the validation cannot drift.
 */
const RECOVERY_MIN_LEN = 8;
const RECOVERY_MAX_LEN = 20;

export interface LoginSearch {
  redirect?: string;
}

interface LoginResponse {
  accessToken?: string;
  step?: string;
  challengeToken?: string;
}

interface TotpResponse {
  accessToken: string;
}

interface MeResponse {
  id: string;
  email: string;
  displayName: string;
  locale: string;
  timezone: string;
  country: string;
  themePreference: string;
  isInstanceAdmin: boolean;
}

function LoginPage(): ReactElement {
  const { t } = useTranslation('auth');
  const navigate = useNavigate();
  const isAuthenticated = useAuth(selectIsAuthenticated);
  const { redirect: redirectTarget } = useSearch({ from: '/login' });
  const caps = useCapabilities();

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useZodForm<typeof loginSchema>(loginSchema, {
    email: '',
    password: '',
  });

  const [serverError, setServerError] = useState<AuthErrorI18nKey | null>(null);
  const [challengeToken, setChallengeToken] = useState<string | null>(null);
  const [totpCode, setTotpCode] = useState('');
  const [useRecovery, setUseRecovery] = useState(false);
  const [recoveryCode, setRecoveryCode] = useState('');
  const totpGuard = useSubmitGuard();
  const totpCapsHint = useCapsLockHint();
  const [showMagicLink, setShowMagicLink] = useState(false);
  const [magicLinkEmail, setMagicLinkEmail] = useState('');
  const [magicLinkSent, setMagicLinkSent] = useState(false);
  const magicLinkGuard = useSubmitGuard();
  const magicLinkEmailRef = useRef<HTMLInputElement>(null);

  const redirectAfterLogin = useCallback((): void => {
    if (redirectTarget && isSafeRedirect(redirectTarget)) {
      window.location.href = redirectTarget;
    } else {
      void navigate({ to: '/profile', replace: true });
    }
  }, [redirectTarget, navigate]);

  useEffect(() => {
    if (isAuthenticated) {
      redirectAfterLogin();
    }
  }, [isAuthenticated, redirectAfterLogin]);

  const completeSignIn = async (accessToken: string): Promise<void> => {
    authStore.getState().setAccessToken(accessToken);
    const { data, error } = await sdk.GET('/me');
    if (error || !data) {
      setServerError(mapAuthError(error as ProblemJson | undefined));
      authStore.getState().clearSession();
      return;
    }
    const me = data as MeResponse;
    const user: AuthUser = {
      id: me.id,
      email: me.email,
      displayName: me.displayName,
      locale: me.locale,
      timezone: me.timezone,
      country: me.country,
      themePreference: me.themePreference,
      isInstanceAdmin: me.isInstanceAdmin,
    };
    authStore.getState().setSession(accessToken, user);
    redirectAfterLogin();
  };

  const onSubmit = async (values: LoginFormValues): Promise<void> => {
    setServerError(null);
    try {
      const { data, error } = await sdk.POST('/auth/login', {
        body: { email: values.email, password: values.password },
      });
      if (error || !data) {
        setServerError(mapAuthError(error as ProblemJson | undefined));
        return;
      }
      const login = data as LoginResponse;
      if (login.step === 'totp_required') {
        setChallengeToken(login.challengeToken ?? '');
        return;
      }
      if (!login.accessToken) {
        setServerError('auth:errors.generic');
        return;
      }
      await completeSignIn(login.accessToken);
    } catch (err) {
      setServerError(mapAuthThrown(err));
    }
  };

  const handleTotpSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    setServerError(null);
    if (challengeToken == null) return;
    if (useRecovery ? recoveryCode.trim().length < RECOVERY_MIN_LEN : totpCode.length !== 6) return;
    // Synchronous re-entrancy guard: a fast double Enter / double click
    // bails on the second call before reaching the network layer.
    if (totpGuard.guard()) return;
    try {
      const { data, error } = await sdk.POST('/auth/login/totp', {
        body: useRecovery
          ? { challengeToken, recoveryCode: recoveryCode.trim() }
          : { challengeToken, code: totpCode },
      });
      if (error || !data) {
        setServerError(mapAuthError(error as ProblemJson | undefined));
        return;
      }
      const totp = data as TotpResponse;
      await completeSignIn(totp.accessToken);
    } catch (err) {
      setServerError(mapAuthThrown(err));
    } finally {
      totpGuard.end();
    }
  };

  const handleCancelTotp = (): void => {
    setChallengeToken(null);
    setTotpCode('');
    setRecoveryCode('');
    setUseRecovery(false);
    setServerError(null);
  };

  const handleMagicLinkSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (!magicLinkEmail.trim()) return;
    // Synchronous re-entrancy guard: a fast double Enter / double click
    // bails on the second call before reaching the network layer.
    if (magicLinkGuard.guard()) return;
    setServerError(null);
    try {
      const { error } = await sdk.POST('/auth/magic-link/request' as never, {
        body: { email: magicLinkEmail.trim() },
      });
      if (error) {
        setServerError(mapAuthError(error as ProblemJson | undefined));
        return;
      }
      setMagicLinkSent(true);
    } catch (err) {
      setServerError(mapAuthThrown(err));
    } finally {
      magicLinkGuard.end();
    }
  };

  if (showMagicLink) {
    if (magicLinkSent) {
      return (
        <AuthCard>
          <div className="aw-stack aw-stack-5">
            <h1 className="aw-page-title">{t('login.magic_link_title')}</h1>
            <p className="aw-flush aw-muted aw-text-sm">
              {t('login.magic_link_sent', { email: magicLinkEmail })}
            </p>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                setShowMagicLink(false);
                setMagicLinkSent(false);
                setMagicLinkEmail('');
              }}
            >
              {t('login.magic_link_back')}
            </Button>
          </div>
        </AuthCard>
      );
    }
    return (
      <AuthCard>
        <form
          onSubmit={(e) => {
            void handleMagicLinkSubmit(e);
          }}
          noValidate
          className="aw-stack aw-stack-5"
        >
          <h1 className="aw-page-title">{t('login.magic_link_title')}</h1>
          <p className="aw-flush aw-muted aw-text-sm">{t('login.magic_link_instructions')}</p>
          <FormField label={t('login.email')} required>
            {(control) => (
              <Input
                {...control}
                ref={magicLinkEmailRef}
                type="email"
                autoComplete="email"
                value={magicLinkEmail}
                onChange={(e) => {
                  setMagicLinkEmail(e.target.value);
                }}
                autoFocus
              />
            )}
          </FormField>
          {serverError ? (
            <p role="alert" className="aw-error">
              {t(serverError)}
            </p>
          ) : null}
          <Button
            type="submit"
            variant="primary"
            disabled={magicLinkGuard.submitting || !magicLinkEmail.trim()}
          >
            {magicLinkGuard.submitting
              ? t('login.magic_link_sending')
              : t('login.magic_link_submit')}
          </Button>
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              setShowMagicLink(false);
              setServerError(null);
            }}
          >
            {t('login.magic_link_back')}
          </Button>
        </form>
      </AuthCard>
    );
  }

  if (challengeToken != null) {
    // Status copy reflects validation state so the `aria-live` region read
    // by screen readers stays in sync with the visible input. We only ever
    // surface a single description string per render to avoid contradictory
    // hints (the disabled submit button is the second cue).
    const recoveryRemaining = RECOVERY_MIN_LEN - recoveryCode.trim().length;
    const totpStatus = useRecovery
      ? recoveryRemaining > 0
        ? t('login.recovery_helper')
        : t('login.totp_status_awaiting')
      : totpCode.length < 6
        ? t('login.totp_status_need_digits')
        : t('login.totp_status_awaiting');
    const totpError = serverError ? t(serverError) : null;
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
                  onKeyDown={totpCapsHint.handlers.onKeyDown}
                  onFocus={totpCapsHint.handlers.onFocus}
                  onBlur={totpCapsHint.handlers.onBlur}
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
                  onKeyDown={totpCapsHint.handlers.onKeyDown}
                  onFocus={totpCapsHint.handlers.onFocus}
                  onBlur={totpCapsHint.handlers.onBlur}
                  autoFocus
                />
              )}
            </FormField>
          )}
          {totpCapsHint.capsLockOn ? (
            <output aria-live="polite" className="aw-flush aw-muted aw-text-xs">
              {t('login.caps_lock_on')}
            </output>
          ) : null}
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              setUseRecovery((v) => !v);
              setServerError(null);
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
          <Button
            type="button"
            variant="ghost"
            onClick={handleCancelTotp}
            disabled={totpGuard.submitting}
          >
            {t('login.totp_cancel')}
          </Button>
        </form>
      </AuthCard>
    );
  }

  return (
    <AuthCard>
      <form
        onSubmit={(e) => {
          void handleSubmit(onSubmit)(e);
        }}
        noValidate
        className="aw-stack aw-stack-5"
      >
        <h1 className="aw-page-title">{t('login.title')}</h1>

        <FormField
          label={t('login.email')}
          required
          {...(errors.email?.message ? { error: t(errors.email.message) } : {})}
        >
          {(control) => {
            const { ref, ...field } = register('email');
            return (
              <Input
                {...control}
                {...field}
                ref={ref}
                type="email"
                autoComplete="email"
                autoFocus
              />
            );
          }}
        </FormField>

        <FormField
          label={t('login.password')}
          required
          {...(errors.password?.message ? { error: t(errors.password.message) } : {})}
        >
          {(control) => {
            const { ref, ...field } = register('password');
            return (
              <PasswordInput {...control} {...field} ref={ref} autoComplete="current-password" />
            );
          }}
        </FormField>

        {serverError ? (
          <p role="alert" className="aw-error">
            {t(serverError)}
          </p>
        ) : null}

        <Button type="submit" variant="primary" disabled={isSubmitting}>
          {isSubmitting ? t('login.submitting') : t('login.submit')}
        </Button>

        <OAuthButtonRow mode="login" onError={setServerError} />

        {caps?.magicLink && (
          <>
            <div className="aw-row aw-row-3 aw-muted aw-text-sm">
              <hr className="aw-rule" />
              <span>{t('login.magic_link_divider')}</span>
              <hr className="aw-rule" />
            </div>

            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                setShowMagicLink(true);
                setServerError(null);
              }}
            >
              {t('login.magic_link_button')}
            </Button>
          </>
        )}

        {caps?.registrationOpen !== false && (
          <p className="aw-flush aw-muted aw-text-sm aw-text-center">
            {t('login.no_account')}{' '}
            <Link to="/signup" className="aw-link">
              {t('login.signup_link')}
            </Link>
          </p>
        )}
      </form>
    </AuthCard>
  );
}

export const Route = createFileRoute('/login')({
  validateSearch: (search: Record<string, unknown>): LoginSearch => {
    const redirect = typeof search.redirect === 'string' ? search.redirect : undefined;
    return redirect ? { redirect } : {};
  },
  component: LoginPage,
});
