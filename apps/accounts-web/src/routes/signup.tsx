/**
 * /signup -- public registration page. Posts to POST /auth/register.
 *
 * Uses React Hook Form + Zod for client-side validation.
 */

import { zodResolver } from '@hookform/resolvers/zod';
import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { Link, createFileRoute, useNavigate } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';

import AuthCard from '../components/auth-card';
import { type SignupFormValues, signupSchema } from '../features/auth/auth-schemas';
import {
  type AuthUser,
  authStore,
  selectIsAuthenticated,
  useAuth,
} from '../features/auth/auth-store';
import OAuthButtonRow from '../features/oauth/oauth-button-row';
import type { ProblemJson } from '../lib/api-error';
import { type AuthErrorI18nKey, mapAuthError, mapAuthThrown } from '../lib/auth-errors';
import { sdk } from '../lib/sdk';

interface RegisterResponse {
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

function SignupPage(): ReactElement {
  const { t } = useTranslation('auth');
  const navigate = useNavigate();
  const isAuthenticated = useAuth(selectIsAuthenticated);

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<SignupFormValues>({
    resolver: zodResolver(signupSchema),
    defaultValues: { email: '', password: '', displayName: '' },
  });

  const [serverError, setServerError] = useState<AuthErrorI18nKey | null>(null);

  useEffect(() => {
    if (isAuthenticated) {
      void navigate({ to: '/profile', replace: true });
    }
  }, [isAuthenticated, navigate]);

  const onSubmit = async (values: SignupFormValues): Promise<void> => {
    setServerError(null);
    try {
      const { data, error } = await sdk.POST('/auth/register', {
        body: {
          email: values.email,
          password: values.password,
          displayName: values.displayName,
        },
      });
      if (error || !data) {
        setServerError(mapAuthError(error as ProblemJson | undefined));
        return;
      }
      const reg = data as RegisterResponse;
      authStore.getState().setAccessToken(reg.accessToken);
      const { data: meData, error: meError } = await sdk.GET('/me');
      if (meError || !meData) {
        setServerError(mapAuthError(meError as ProblemJson | undefined));
        authStore.getState().clearSession();
        return;
      }
      const me = meData as MeResponse;
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
      authStore.getState().setSession(reg.accessToken, user);
      void navigate({ to: '/profile', replace: true });
    } catch (err) {
      setServerError(mapAuthThrown(err));
    }
  };

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
          {t('signup.title')}
        </h1>

        <FormField
          label={t('signup.name')}
          required
          {...(errors.displayName?.message ? { error: t(errors.displayName.message) } : {})}
        >
          {(control) => {
            const { ref, ...field } = register('displayName');
            return (
              <Input {...control} {...field} ref={ref} type="text" autoComplete="name" autoFocus />
            );
          }}
        </FormField>

        <FormField
          label={t('login.email')}
          required
          {...(errors.email?.message ? { error: t(errors.email.message) } : {})}
        >
          {(control) => {
            const { ref, ...field } = register('email');
            return <Input {...control} {...field} ref={ref} type="email" autoComplete="email" />;
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
                autoComplete="new-password"
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
          {isSubmitting ? t('signup.submitting') : t('signup.submit')}
        </Button>

        <OAuthButtonRow mode="signup" onError={setServerError} />

        <p
          style={{
            margin: 0,
            fontSize: 'var(--nf-text-sm, 0.875rem)',
            color: 'var(--nf-color-fg-muted)',
            textAlign: 'center',
          }}
        >
          {t('signup.have_account')}{' '}
          <Link to="/login" style={{ fontWeight: 500, color: 'var(--nf-color-accent)' }}>
            {t('signup.login_link')}
          </Link>
        </p>
      </form>
    </AuthCard>
  );
}

export const Route = createFileRoute('/signup')({
  component: SignupPage,
});
