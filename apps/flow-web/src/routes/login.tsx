/**
 * /login -- public page. Redirects to / when already authenticated.
 *
 * Uses React Hook Form + Zod for client-side validation. The TOTP
 * challenge step remains useState-based because it is a secondary
 * step triggered by the server, not a user-filled form.
 */

import { zodResolver } from '@hookform/resolvers/zod';
import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { Link, createFileRoute, useNavigate } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, useEffect, useState } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';

import AuthCard from '../components/auth/auth-card';
import { type AuthErrorI18nKey, mapAuthError, mapAuthThrown } from '../features/auth/auth-errors';
import { type LoginFormValues, loginSchema } from '../features/auth/auth-schemas';
import {
  type AuthUser,
  authStore,
  selectIsAuthenticated,
  useAuth,
} from '../features/auth/auth-store';
import { sdk } from '../lib/sdk';

function LoginPage(): ReactElement {
  const { t } = useTranslation('common');
  const navigate = useNavigate();
  const isAuthenticated = useAuth(selectIsAuthenticated);

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

  useEffect(() => {
    if (isAuthenticated) {
      void navigate({ to: '/', replace: true });
    }
  }, [isAuthenticated, navigate]);

  const completeSignIn = async (accessToken: string): Promise<void> => {
    authStore.getState().setAccessToken(accessToken);
    const me = await sdk.GET('/me');
    if (me.error || !me.data) {
      setServerError(mapAuthError(me.error ?? null));
      authStore.getState().clearSession();
      return;
    }
    const user: AuthUser = {
      id: me.data.id,
      email: me.data.email,
      displayName: me.data.displayName,
      locale: me.data.locale,
    };
    authStore.getState().setSession(accessToken, user);
    void navigate({ to: '/', replace: true });
  };

  const onSubmit = async (values: LoginFormValues): Promise<void> => {
    setServerError(null);
    try {
      const { data, error } = await sdk.POST('/auth/login', {
        body: { email: values.email, password: values.password },
      });
      if (error || !data) {
        setServerError(mapAuthError(error ?? null));
        return;
      }
      if (data.step === 'totp_required') {
        setChallengeToken(data.challengeToken ?? '');
        return;
      }
      if (!data.accessToken) {
        setServerError('auth.errors.generic');
        return;
      }
      await completeSignIn(data.accessToken);
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
        setServerError(mapAuthError(error ?? null));
        return;
      }
      await completeSignIn(data.accessToken);
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
            {t('auth.login.totp_title')}
          </h1>
          <p
            style={{
              margin: 0,
              color: 'var(--nf-color-fg-muted, var(--color-muted))',
              fontSize: 'var(--nf-text-sm, 0.875rem)',
            }}
          >
            {t('auth.login.totp_instructions')}
          </p>
          {useRecovery ? (
            <FormField label={t('auth.login.recovery_code')} required>
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
            <FormField label={t('auth.login.totp_code')} required>
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
            {useRecovery ? t('auth.login.totp_use_code') : t('auth.login.totp_use_recovery')}
          </Button>
          {serverError ? (
            <p
              role="alert"
              style={{
                margin: 0,
                color: 'var(--nf-color-fg-danger, var(--color-danger))',
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
            {useRecovery ? t('auth.login.recovery_submit') : t('auth.login.totp_submit')}
          </Button>
          <Button
            type="button"
            variant="ghost"
            onClick={handleCancelTotp}
            disabled={totpSubmitting}
          >
            {t('auth.login.totp_cancel')}
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
          {t('auth.login.title')}
        </h1>

        <FormField
          label={t('auth.login.email')}
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
          label={t('auth.login.password')}
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
              color: 'var(--nf-color-fg-danger, var(--color-danger))',
              fontSize: 'var(--nf-text-sm, 0.875rem)',
            }}
          >
            {t(serverError)}
          </p>
        ) : null}

        <Button type="submit" variant="primary" disabled={isSubmitting}>
          {isSubmitting ? t('auth.login.submitting') : t('auth.login.submit')}
        </Button>

        <p
          style={{
            margin: 0,
            fontSize: 'var(--nf-text-sm, 0.875rem)',
            color: 'var(--nf-color-fg-muted, var(--color-muted))',
          }}
        >
          {t('auth.login.no_account')} <Link to="/signup">{t('auth.login.signup_link')}</Link>
        </p>
      </form>
    </AuthCard>
  );
}

export const Route = createFileRoute('/login')({
  component: LoginPage,
});
