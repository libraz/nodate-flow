/**
 * /signup -- public registration page. Posts to POST /auth/register.
 *
 * Uses React Hook Form + Zod for client-side validation.
 */

import { useZodForm } from '@nodate-flow/ui/hooks/use-zod-form';
import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Link, createFileRoute, useNavigate } from '@tanstack/react-router';
import { type ReactElement, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import AuthCard from '../components/auth-card';
import { type SignupFormValues, signupSchema } from '../features/auth/auth-schemas';
import {
  type AuthUser,
  authStore,
  selectIsAuthenticated,
  useAuth,
} from '../features/auth/auth-store';
import PasswordInput from '../features/auth/password-input';
import OAuthButtonRow from '../features/oauth/oauth-button-row';
import type { ProblemJson } from '../lib/api-error';
import { type AuthErrorI18nKey, mapAuthError, mapAuthThrown } from '../lib/auth-errors';
import { sdk } from '../lib/sdk';
import { useSubmitGuard } from '../lib/use-submit-guard';

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
  } = useZodForm<typeof signupSchema>(signupSchema, {
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
    if (serverError) {
      alertRef.current?.focus();
    }
  }, [serverError]);

  const onSubmit = async (values: SignupFormValues): Promise<void> => {
    if (submitGuard.guard()) return;
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
                autoComplete="email"
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
        </FormField>

        <FormField
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
        </FormField>

        {serverError ? (
          <p
            ref={alertRef}
            role="alert"
            tabIndex={-1}
            className="aw-error"
            data-testid="signup-server-error"
          >
            {t(serverError)}
          </p>
        ) : null}

        <Button type="submit" variant="primary" disabled={isSubmitting || submitGuard.submitting}>
          {isSubmitting || submitGuard.submitting ? t('signup.submitting') : t('signup.submit')}
        </Button>

        <OAuthButtonRow mode="signup" onError={setServerError} />

        <p className="aw-flush aw-muted aw-text-sm aw-text-center">
          {t('signup.have_account')}{' '}
          <Link to="/login" className="aw-link">
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
