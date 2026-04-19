/**
 * /login -- public page. Redirects to `?redirect=` target or / when
 * already authenticated.
 *
 * Uses React Hook Form + Zod for client-side validation. The TOTP
 * challenge step remains useState-based because it is a secondary
 * step triggered by the server, not a user-filled form.
 */

import { zodResolver } from '@hookform/resolvers/zod';
import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { Link, createFileRoute, useNavigate, useSearch } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, useCallback, useEffect, useState } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';

import AuthCard from '../components/auth-card';
import type { ProblemJson } from '../lib/api-error';
import { type AuthErrorI18nKey, mapAuthError, mapAuthThrown } from '../lib/auth-errors';
import { type LoginFormValues, loginSchema } from '../lib/auth-schemas';
import { sdk } from '../lib/sdk';
import { type AuthUser, authStore, selectIsAuthenticated, useAuth } from '../stores/auth-store';

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
  themePreference: string;
  isInstanceAdmin: boolean;
}

function LoginPage(): ReactElement {
  const { t } = useTranslation('auth');
  const navigate = useNavigate();
  const isAuthenticated = useAuth(selectIsAuthenticated);
  const { redirect: redirectTarget } = useSearch({ from: '/login' });

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<LoginFormValues>({
    resolver: zodResolver(loginSchema),
    defaultValues: { email: '', password: '' },
  });

  const [serverError, setServerError] = useState<AuthErrorI18nKey | null>(null);
  const [challengeToken, setChallengeToken] = useState<string | null>(null);
  const [totpCode, setTotpCode] = useState('');
  const [useRecovery, setUseRecovery] = useState(false);
  const [recoveryCode, setRecoveryCode] = useState('');
  const [totpSubmitting, setTotpSubmitting] = useState(false);

  const redirectAfterLogin = useCallback((): void => {
    if (redirectTarget) {
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
    const { data, error } = await sdk.GET('/auth/me');
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
    if (useRecovery ? recoveryCode.trim().length < 10 : totpCode.length !== 6) return;
    setTotpSubmitting(true);
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
      setTotpSubmitting(false);
    }
  };

  const handleCancelTotp = (): void => {
    setChallengeToken(null);
    setTotpCode('');
    setRecoveryCode('');
    setUseRecovery(false);
    setServerError(null);
  };

  if (challengeToken != null) {
    return (
      <AuthCard>
        <form
          onSubmit={(e) => {
            void handleTotpSubmit(e);
          }}
          noValidate
          style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-5, 1.5rem)' }}
        >
          <h1
            style={{
              fontFamily: 'var(--nf-font-display, var(--font-display))',
              fontSize: 'var(--nf-text-2xl, 1.5rem)',
              margin: 0,
            }}
          >
            {t('login.totp_title')}
          </h1>
          <p
            style={{
              margin: 0,
              color: 'var(--nf-color-fg-muted)',
              fontSize: 'var(--nf-text-sm, 0.875rem)',
            }}
          >
            {t('login.totp_instructions')}
          </p>
          {useRecovery ? (
            <FormField label={t('login.recovery_code')} required>
              {(control) => (
                <Input
                  {...control}
                  autoComplete="one-time-code"
                  value={recoveryCode}
                  onChange={(e) => {
                    setRecoveryCode(e.target.value.toUpperCase().slice(0, 20));
                  }}
                  autoFocus
                />
              )}
            </FormField>
          ) : (
            <FormField label={t('login.totp_code')} required>
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
                  autoFocus
                />
              )}
            </FormField>
          )}
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              setUseRecovery((v) => !v);
              setServerError(null);
            }}
            disabled={totpSubmitting}
          >
            {useRecovery ? t('login.totp_use_code') : t('login.totp_use_recovery')}
          </Button>
          {serverError ? (
            <p
              role="alert"
              style={{
                margin: 0,
                color: 'var(--nf-color-danger)',
                fontSize: 'var(--nf-text-sm, 0.875rem)',
              }}
            >
              {t(serverError)}
            </p>
          ) : null}
          <Button
            type="submit"
            variant="primary"
            disabled={
              totpSubmitting ||
              (useRecovery ? recoveryCode.trim().length < 10 : totpCode.length !== 6)
            }
          >
            {useRecovery ? t('login.recovery_submit') : t('login.totp_submit')}
          </Button>
          <Button
            type="button"
            variant="ghost"
            onClick={handleCancelTotp}
            disabled={totpSubmitting}
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
        style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-5, 1.5rem)' }}
      >
        <h1
          style={{
            fontFamily: 'var(--nf-font-display, var(--font-display))',
            fontSize: 'var(--nf-text-2xl, 1.5rem)',
            margin: 0,
          }}
        >
          {t('login.title')}
        </h1>

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
              <Input
                {...control}
                {...field}
                ref={ref}
                type="password"
                autoComplete="current-password"
              />
            );
          }}
        </FormField>

        {serverError ? (
          <p
            role="alert"
            style={{
              margin: 0,
              color: 'var(--nf-color-danger)',
              fontSize: 'var(--nf-text-sm, 0.875rem)',
            }}
          >
            {t(serverError)}
          </p>
        ) : null}

        <Button type="submit" variant="primary" disabled={isSubmitting}>
          {isSubmitting ? t('login.submitting') : t('login.submit')}
        </Button>

        <p
          style={{
            margin: 0,
            fontSize: 'var(--nf-text-sm, 0.875rem)',
            color: 'var(--nf-color-fg-muted)',
          }}
        >
          {t('login.no_account')} <Link to="/signup">{t('login.signup_link')}</Link>
        </p>
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
