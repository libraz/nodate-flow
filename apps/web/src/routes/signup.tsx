/**
 * /signup -- public registration page. Posts to POST /auth/register.
 *
 * Uses React Hook Form + Zod for client-side validation. The backend
 * field is `displayName` (not `name`); the form label uses
 * `auth.signup.name` for brevity but binds to displayName.
 */

import { zodResolver } from '@hookform/resolvers/zod';
import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { Link, createFileRoute, useNavigate } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';

import AuthCard from '../components/auth/auth-card';
import { type AuthErrorI18nKey, mapAuthError, mapAuthThrown } from '../features/auth/auth-errors';
import { type SignupFormValues, signupSchema } from '../features/auth/auth-schemas';
import {
  type AuthUser,
  authStore,
  selectIsAuthenticated,
  useAuth,
} from '../features/auth/auth-store';
import { sdk } from '../lib/sdk';

function SignupPage(): ReactElement {
  const { t } = useTranslation('common');
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
      void navigate({ to: '/', replace: true });
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
        setServerError(mapAuthError(error ?? null));
        return;
      }
      authStore.getState().setAccessToken(data.accessToken);
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
      authStore.getState().setSession(data.accessToken, user);
      void navigate({ to: '/', replace: true });
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
          {t('auth.signup.title')}
        </h1>

        <FormField
          label={t('auth.signup.name')}
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
          label={t('auth.login.email')}
          required
          {...(errors.email?.message ? { error: t(errors.email.message) } : {})}
        >
          {(control) => {
            const { ref, ...field } = register('email');
            return <Input {...control} {...field} ref={ref} type="email" autoComplete="email" />;
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
          {isSubmitting ? t('auth.signup.submitting') : t('auth.signup.submit')}
        </Button>

        <p
          style={{
            margin: 0,
            fontSize: 'var(--nf-text-sm, 0.875rem)',
            color: 'var(--nf-color-fg-muted, var(--color-muted))',
          }}
        >
          {t('auth.signup.have_account')} <Link to="/login">{t('auth.signup.login_link')}</Link>
        </p>
      </form>
    </AuthCard>
  );
}

export const Route = createFileRoute('/signup')({
  component: SignupPage,
});
