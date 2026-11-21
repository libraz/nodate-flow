/**
 * /signup -- public registration page. Posts to POST /auth/register.
 *
 * Uses React Hook Form + Zod for client-side validation.
 */

import type { components } from '@nodate-flow/sdk';
import { useZodForm } from '@nodate-flow/ui/hooks/use-zod-form';
import Button from '@nodate-flow/ui/primitives/button';
import ErrorFallback from '@nodate-flow/ui/primitives/error-fallback';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router';
import { type ReactElement, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import AuthCard from '../components/auth-card';
import CollapsibleStatus from '../components/collapsible-status';
import { type SignupFormValues, signupSchema } from '../features/auth/auth-schemas';
import { authStore, selectIsAuthenticated, useAuth } from '../features/auth/auth-store';
import PasswordInput, { PasswordField } from '../features/auth/password-input';
import { capsOf, useCapabilities } from '../features/auth/use-capabilities';
import { type MeResponse, userFromMe } from '../features/auth/user-from-me';
import OAuthButtonRow from '../features/oauth/oauth-button-row';
import { apiRequest } from '../lib/api';
import { type AuthErrorI18nKey, mapAuthThrown } from '../lib/auth-errors';
import { useSubmitGuard } from '../lib/use-submit-guard';

/**
 * SDK-derived response bodies. Generated from the Go schema by `make
 * gen-sdk`; mirrors auth-api's POST /auth/register and GET /me exactly.
 */
type RegisterResponse = components['schemas']['AuthTokens'];

function SignupPage(): ReactElement | null {
  const { t } = useTranslation('auth');
  const navigate = useNavigate();
  const isAuthenticated = useAuth(selectIsAuthenticated);
  const capsState = useCapabilities();
  const caps = capsOf(capsState);
  // Sign-up here creates a password account, so an instance without
  // password accounts sends visitors to the sign-in page, which offers the
  // identity providers.
  const passwordLoginDisabled = caps !== null && !caps.passwordLogin;

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useZodForm<SignupFormValues>(signupSchema, {
    email: '',
    password: '',
    newPasswordConfirm: '',
    displayName: '',
  });

  const submitGuard = useSubmitGuard();
  const [serverError, setServerError] = useState<AuthErrorI18nKey | null>(null);

  /**
   * Focus refs (F4). Mirrors the login page pattern: validation failures
   * land focus on the first invalid field; server errors land focus on
   * the live alert region so screen-reader users hear the failure.
   */
  const displayNameRef = useRef<HTMLInputElement>(null);
  const emailRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);
  const passwordConfirmRef = useRef<HTMLInputElement>(null);
  const alertRef = useRef<HTMLParagraphElement>(null);

  useEffect(() => {
    if (isAuthenticated) {
      void navigate({ to: '/profile', replace: true });
    }
  }, [isAuthenticated, navigate]);

  useEffect(() => {
    if (passwordLoginDisabled) {
      void navigate({ to: '/login', replace: true });
    }
  }, [passwordLoginDisabled, navigate]);

  useEffect(() => {
    if (serverError) {
      alertRef.current?.focus();
    }
  }, [serverError]);

  const onSubmit = async (values: SignupFormValues): Promise<void> => {
    if (submitGuard.guard()) return;
    setServerError(null);
    try {
      const data = await apiRequest(
        (client) =>
          client.POST('/auth/register', {
            body: {
              email: values.email,
              password: values.password,
              displayName: values.displayName,
            },
          }),
        'Registration failed',
      );
      const reg = data as RegisterResponse;
      authStore.getState().setAccessToken(reg.accessToken);
      let meData: unknown;
      try {
        meData = await apiRequest(
          (client) => client.GET('/me'),
          'Failed to load the signed-in user',
        );
      } catch (meError) {
        setServerError(mapAuthThrown(meError));
        authStore.getState().clearSession();
        return;
      }
      authStore.getState().setSession(reg.accessToken, userFromMe(meData as MeResponse));
      toaster.show({ message: t('signup.success'), tone: 'success' });
      void navigate({ to: '/profile', replace: true });
    } catch (err) {
      setServerError(mapAuthThrown(err));
    } finally {
      submitGuard.end();
    }
  };

  const submitWithFocus = handleSubmit(
    async (values) => {
      await onSubmit(values);
    },
    (formErrors) => {
      if (formErrors.displayName) {
        displayNameRef.current?.focus();
      } else if (formErrors.email) {
        emailRef.current?.focus();
      } else if (formErrors.password) {
        passwordRef.current?.focus();
      } else if (formErrors.newPasswordConfirm) {
        passwordConfirmRef.current?.focus();
      }
    },
  );

  if (capsState.status === 'error') {
    return (
      <AuthCard>
        <ErrorFallback
          title={t('fatal.title')}
          action={{ label: t('fatal.retry'), onClick: capsState.retry }}
        />
      </AuthCard>
    );
  }
  if (caps === null || passwordLoginDisabled) return null;

  return (
    <AuthCard>
      <form
        onSubmit={(e) => {
          void submitWithFocus(e);
        }}
        noValidate
        className="aw-stack aw-stack-5"
      >
        <h1 className="aw-page-title">{t('signup.title')}</h1>

        <FormField
          label={t('signup.name')}
          required
          {...(errors.displayName?.message ? { error: t(errors.displayName.message) } : {})}
        >
          {(control) => {
            const { ref: rhfRef, ...field } = register('displayName');
            return (
              <Input
                {...control}
                {...field}
                ref={(el) => {
                  rhfRef(el);
                  displayNameRef.current = el;
                }}
                type="text"
                autoComplete="name"
                autoFocus
              />
            );
          }}
        </FormField>

        <FormField
          label={t('login.email')}
          required
          {...(errors.email?.message ? { error: t(errors.email.message) } : {})}
        >
          {(control) => {
            const { ref: rhfRef, ...field } = register('email');
            return (
              <Input
                {...control}
                {...field}
                ref={(el) => {
                  rhfRef(el);
                  emailRef.current = el;
                }}
                type="email"
                autoComplete="username"
                inputMode="email"
                spellCheck={false}
              />
            );
          }}
        </FormField>

        <PasswordField
          label={t('login.password')}
          required
          {...(errors.password?.message ? { error: t(errors.password.message) } : {})}
        >
          {(control) => {
            const { ref: rhfRef, ...field } = register('password');
            return (
              <PasswordInput
                {...control}
                {...field}
                ref={(el) => {
                  rhfRef(el);
                  passwordRef.current = el;
                }}
                autoComplete="new-password"
              />
            );
          }}
        </PasswordField>

        <PasswordField
          label={t('signup.password_confirm')}
          required
          {...(errors.newPasswordConfirm?.message
            ? { error: t(errors.newPasswordConfirm.message) }
            : {})}
        >
          {(control) => {
            const { ref: rhfRef, ...field } = register('newPasswordConfirm');
            return (
              <PasswordInput
                {...control}
                {...field}
                ref={(el) => {
                  rhfRef(el);
                  passwordConfirmRef.current = el;
                }}
                autoComplete="new-password"
              />
            );
          }}
        </PasswordField>

        <CollapsibleStatus value={serverError}>
          {(key) => (
            <p
              ref={alertRef}
              role="alert"
              tabIndex={-1}
              className="aw-error"
              data-testid="signup-server-error"
            >
              {t(key)}
            </p>
          )}
        </CollapsibleStatus>

        <Button
          type="submit"
          variant="primary"
          aria-busy={isSubmitting || submitGuard.submitting || undefined}
          disabled={isSubmitting || submitGuard.submitting}
        >
          {t('signup.submit')}
        </Button>

        <OAuthButtonRow mode="signup" onError={setServerError} />

        <p className="aw-flush aw-muted aw-text-sm aw-text-center">
          {t('signup.have_account')}{' '}
          <Link to="/login" className="aw-link nf-focus-ring">
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
