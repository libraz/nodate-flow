/**
 * /login — public page. Redirects to / when already authenticated.
 *
 * TODO(f3): switch to React Hook Form + @hookform/resolvers/zod once
 * those packages are added to apps/web. They are not currently in
 * package.json; the agent that wrote F3 cannot run `bun add`. Native
 * useState + zod safeParse is used in the meantime; the component
 * surface (FormField + Input + Button) is unchanged.
 */

import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { Link, createFileRoute, useNavigate } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';

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
}

function buildSchema() {
  return z.object({
    email: z
      .string()
      .min(1, 'auth.validation.email_required')
      .email('auth.validation.email_invalid'),
    password: z.string().min(8, 'auth.validation.password_min'),
  });
}

function CenteredCard({ children }: { children: ReactElement }): ReactElement {
  return (
    <main
      style={{
        minBlockSize: '100dvh',
        display: 'grid',
        placeItems: 'center',
        padding: 'var(--nf-space-6, 2rem)',
        background: 'var(--nf-color-bg, var(--color-bg))',
      }}
    >
      <section
        style={{
          inlineSize: 'min(28rem, 100%)',
          background: 'var(--nf-color-bg-elevated, var(--color-surface))',
          border: 'var(--nf-space-px, 1px) solid var(--nf-color-border, var(--color-hairline))',
          borderRadius: 'var(--nf-radius-lg, 0.75rem)',
          padding: 'var(--nf-space-6, 2rem)',
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-5, 1.5rem)',
        }}
      >
        {children}
      </section>
    </main>
  );
}

function LoginPage(): ReactElement {
  const { t } = useTranslation('common');
  const navigate = useNavigate();
  const isAuthenticated = useAuth(selectIsAuthenticated);

  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
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
    const parsed = buildSchema().safeParse({ email, password });
    if (!parsed.success) {
      const next: FormErrors = {};
      for (const issue of parsed.error.issues) {
        const field = issue.path[0];
        if (field === 'email') next.email = issue.message;
        if (field === 'password') next.password = issue.message;
      }
      setErrors(next);
      return;
    }
    setErrors({});
    setSubmitting(true);
    try {
      const { data, error } = await sdk.POST('/auth/login', {
        body: { email: parsed.data.email, password: parsed.data.password },
      });
      if (error || !data) {
        setServerError(mapAuthError(error ?? null));
        return;
      }
      // Backend returns only AuthTokens; fetch /me to populate the user.
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
    <CenteredCard>
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
          {t('auth.login.title')}
        </h1>

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
              autoComplete="current-password"
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
          {t('auth.login.submit')}
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
    </CenteredCard>
  );
}

export const Route = createFileRoute('/login')({
  component: LoginPage,
});
