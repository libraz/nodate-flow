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
import { apiRequest } from '../lib/api-client';
import { type AuthErrorI18nKey, mapAuthError, mapAuthThrown } from '../lib/auth-errors';
import { type SignupFormValues, signupSchema } from '../lib/auth-schemas';
import { type AuthUser, authStore, selectIsAuthenticated, useAuth } from '../stores/auth-store';

interface RegisterResponse {
  accessToken: string;
}

interface MeResponse {
  id: string;
  email: string;
  displayName: string;
  locale: string;
  themePreference: string;
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
      const result = await apiRequest<RegisterResponse>('/auth/register', {
        method: 'POST',
        body: {
          email: values.email,
          password: values.password,
          displayName: values.displayName,
        },
      });
      if (result.error || !result.data) {
        setServerError(mapAuthError(result.error));
        return;
      }
      authStore.getState().setAccessToken(result.data.accessToken);
      const me = await apiRequest<MeResponse>('/auth/me');
      if (me.error || !me.data) {
        setServerError(mapAuthError(me.error));
        authStore.getState().clearSession();
        return;
      }
      const user: AuthUser = {
        id: me.data.id,
        email: me.data.email,
        displayName: me.data.displayName,
        locale: me.data.locale,
        themePreference: me.data.themePreference,
      };
      authStore.getState().setSession(result.data.accessToken, user);
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
              color: 'var(--nf-color-fg-danger, var(--color-danger))',
              fontSize: 'var(--nf-text-sm, 0.875rem)',
            }}
          >
            {t(serverError)}
          </p>
        ) : null}

        <Button type="submit" variant="primary" disabled={isSubmitting}>
          {isSubmitting ? t('signup.submitting') : t('signup.submit')}
        </Button>

        <p
          style={{
            margin: 0,
            fontSize: 'var(--nf-text-sm, 0.875rem)',
            color: 'var(--nf-color-fg-muted, var(--color-muted))',
          }}
        >
          {t('signup.haveAccount')} <Link to="/login">{t('signup.loginLink')}</Link>
        </p>
      </form>
    </AuthCard>
  );
}

export const Route = createFileRoute('/signup')({
  component: SignupPage,
});
