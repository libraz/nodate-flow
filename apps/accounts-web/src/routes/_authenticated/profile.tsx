/**
 * /profile -- Edit profile page (displayName, locale, theme, avatar).
 * Authenticated-only, guarded by the _authenticated layout route.
 */

import { zodResolver } from '@hookform/resolvers/zod';
import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { Link, createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useState } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';

import AuthCard from '../../components/auth-card';
import { type ProfileFormValues, profileSchema } from '../../features/auth/auth-schemas';
import { type AuthUser, authStore, selectUser, useAuth } from '../../features/auth/auth-store';
import { type SupportedLanguage, setLanguage } from '../../i18n';
import { sdk } from '../../lib/sdk';
import { type ThemePreference, useTheme } from '../../providers/theme-provider';

interface MeResponse {
  id: string;
  email: string;
  displayName: string;
  locale: string;
  themePreference: string;
  isInstanceAdmin: boolean;
}

function ProfilePage(): ReactElement {
  const { t } = useTranslation('auth');
  const user = useAuth(selectUser);
  const { setPreference } = useTheme();
  const [serverError, setServerError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<ProfileFormValues>({
    resolver: zodResolver(profileSchema),
    defaultValues: {
      displayName: user?.displayName ?? '',
      locale: (user?.locale as 'en' | 'ja') ?? 'en',
      themePreference: (user?.themePreference as ProfileFormValues['themePreference']) ?? 'system',
    },
  });

  const onSubmit = async (values: ProfileFormValues): Promise<void> => {
    setServerError(null);
    setSuccess(false);
    try {
      const { data, error } = await sdk.PATCH('/auth/me', {
        body: {
          displayName: values.displayName,
          locale: values.locale,
          themePreference: values.themePreference,
        },
      });
      if (error || !data) {
        setServerError(t('errors.generic'));
        return;
      }
      const me = data as MeResponse;
      const updatedUser: AuthUser = {
        id: me.id,
        email: me.email,
        displayName: me.displayName,
        locale: me.locale,
        themePreference: me.themePreference,
        isInstanceAdmin: authStore.getState().user?.isInstanceAdmin ?? false,
      };
      authStore.getState().setSession(authStore.getState().accessToken ?? '', updatedUser);
      setPreference(values.themePreference as ThemePreference);
      setLanguage(values.locale as SupportedLanguage);
      setSuccess(true);
    } catch {
      setServerError(t('errors.unknown'));
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
          {t('profile.title')}
        </h1>

        <FormField
          label={t('profile.display_name')}
          required
          {...(errors.displayName?.message ? { error: t(errors.displayName.message) } : {})}
        >
          {(control) => {
            const { ref, ...field } = register('displayName');
            return <Input {...control} {...field} ref={ref} type="text" autoComplete="name" />;
          }}
        </FormField>

        <FormField label={t('profile.locale')}>
          {(control) => {
            const { ref, ...field } = register('locale');
            return (
              <select
                {...control}
                {...field}
                ref={ref}
                style={{
                  padding: '0.5rem 0.75rem',
                  borderRadius: 'var(--nf-radius-md, 0.375rem)',
                  border: 'var(--nf-space-px, 1px) solid var(--nf-color-border)',
                  background: 'var(--nf-color-bg)',
                  color: 'var(--nf-color-fg)',
                  fontSize: 'var(--nf-text-sm, 0.875rem)',
                }}
              >
                <option value="en">English</option>
                <option value="ja">{t('profile.locale_ja')}</option>
              </select>
            );
          }}
        </FormField>

        <FormField label={t('profile.theme')}>
          {(control) => {
            const { ref, ...field } = register('themePreference');
            return (
              <select
                {...control}
                {...field}
                ref={ref}
                style={{
                  padding: '0.5rem 0.75rem',
                  borderRadius: 'var(--nf-radius-md, 0.375rem)',
                  border: 'var(--nf-space-px, 1px) solid var(--nf-color-border)',
                  background: 'var(--nf-color-bg)',
                  color: 'var(--nf-color-fg)',
                  fontSize: 'var(--nf-text-sm, 0.875rem)',
                }}
              >
                <option value="system">{t('profile.theme_system')}</option>
                <option value="aurora-light">{t('profile.theme_aurora_light')}</option>
                <option value="aurora-dark">{t('profile.theme_aurora_dark')}</option>
                <option value="dotline-light">{t('profile.theme_dotline_light')}</option>
                <option value="dotline-dark">{t('profile.theme_dotline_dark')}</option>
              </select>
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
            {serverError}
          </p>
        ) : null}

        {success ? (
          <output
            style={{
              margin: 0,
              color: 'var(--nf-color-success, var(--nf-color-success))',
              fontSize: 'var(--nf-text-sm, 0.875rem)',
            }}
          >
            {t('profile.saved')}
          </output>
        ) : null}

        <Button type="submit" variant="primary" disabled={isSubmitting}>
          {isSubmitting ? t('profile.saving') : t('profile.save')}
        </Button>

        <p
          style={{
            margin: 0,
            fontSize: 'var(--nf-text-sm, 0.875rem)',
            color: 'var(--nf-color-fg-muted)',
          }}
        >
          <Link to="/security">{t('profile.security_link')}</Link>
        </p>
      </form>
    </AuthCard>
  );
}

export const Route = createFileRoute('/_authenticated/profile')({
  component: ProfilePage,
});
