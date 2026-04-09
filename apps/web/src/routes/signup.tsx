/**
 * /signup — public registration page. Posts to POST /auth/register.
 *
 * Note: the backend field is `displayName` (not `name`); the form label
 * uses `auth.signup.name` for brevity but binds to displayName.
 *
 * TODO(f3): switch to React Hook Form + zod resolver once those packages
 * are added to apps/web (see login.tsx).
 */

import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { Link, createFileRoute, useNavigate } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';

import AuthCard from '../components/auth/auth-card';
import { type AuthErrorI18nKey, mapAuthError, mapAuthThrown } from '../features/auth/auth-errors';
import {
  type AuthUser,
  authStore,
  selectIsAuthenticated,
  useAuth,
} from '../features/auth/auth-store';
import { sdk } from '../lib/sdk';

interface FormErrors {
  email?: string;
  password?: string;
  displayName?: string;
}

function buildSchema() {
  return z.object({
    email: z
      .string()
      .min(1, 'auth.validation.email_required')
      .email('auth.validation.email_invalid'),
    password: z.string().min(8, 'auth.validation.password_min'),
    displayName: z.string().min(1, 'auth.validation.name_required'),
  });
}

function SignupPage(): ReactElement {
  const { t } = useTranslation('common');
  const navigate = useNavigate();
  const isAuthenticated = useAuth(selectIsAuthenticated);

  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [errors, setErrors] = useState<FormErrors>({});
  const [serverError, setServerError] = useState<AuthErrorI18nKey | null>(null);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (isAuthenticated) {
      void navigate({ to: '/', replace: true });
    }
  }, [isAuthenticated, navigate]);

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    setServerError(null);
    const parsed = buildSchema().safeParse({ email, password, displayName });
    if (!parsed.success) {
      const next: FormErrors = {};
      for (const issue of parsed.error.issues) {
        const field = issue.path[0];
        if (field === 'email') next.email = issue.message;
        if (field === 'password') next.password = issue.message;
        if (field === 'displayName') next.displayName = issue.message;
      }
      setErrors(next);
      return;
    }
    setErrors({});
    setSubmitting(true);
    try {
      const { data, error } = await sdk.POST('/auth/register', {
        body: {
          email: parsed.data.email,
          password: parsed.data.password,
          displayName: parsed.data.displayName,
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
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <AuthCard>
      <form
        onSubmit={(e) => {
          void handleSubmit(e);
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
          {...(errors.displayName ? { error: t(errors.displayName) } : {})}
        >
          {(control) => (
            <Input
              {...control}
              type="text"
              autoComplete="name"
              autoFocus
              value={displayName}
              onChange={(e) => {
                setDisplayName(e.target.value);
              }}
            />
          )}
        </FormField>

        <FormField
          label={t('auth.login.email')}
          required
          {...(errors.email ? { error: t(errors.email) } : {})}
        >
          {(control) => (
            <Input
              {...control}
              type="email"
              autoComplete="email"
              value={email}
              onChange={(e) => {
                setEmail(e.target.value);
              }}
            />
          )}
        </FormField>

        <FormField
          label={t('auth.login.password')}
          required
          {...(errors.password ? { error: t(errors.password) } : {})}
        >
          {(control) => (
            <Input
              {...control}
              type="password"
              autoComplete="new-password"
              value={password}
              onChange={(e) => {
                setPassword(e.target.value);
              }}
            />
          )}
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

        <Button type="submit" variant="primary" disabled={submitting}>
          {submitting ? t('auth.signup.submitting') : t('auth.signup.submit')}
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
