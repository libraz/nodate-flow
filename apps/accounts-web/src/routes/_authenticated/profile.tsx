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
import { type SupportedLanguage, setLanguage } from '../../i18n';
import { apiRequest } from '../../lib/api-client';
import { type ProfileFormValues, profileSchema } from '../../lib/auth-schemas';
import { type ThemePreference, useTheme } from '../../providers/theme-provider';
import { type AuthUser, authStore, selectUser, useAuth } from '../../stores/auth-store';

interface MeResponse {
  id: string;
  email: string;
  displayName: string;
  locale: string;
  themePreference: string;
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
      const result = await apiRequest<MeResponse>('/auth/me', {
        method: 'PATCH',
        body: {
          displayName: values.displayName,
          locale: values.locale,
          themePreference: values.themePreference,
        },
      });
      if (result.error || !result.data) {
        setServerError(t('errors.generic'));
        return;
      }
      const updatedUser: AuthUser = {
        id: result.data.id,
        email: result.data.email,
        displayName: result.data.displayName,
        locale: result.data.locale,
        themePreference: result.data.themePreference,
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
          label={t('profile.displayName')}
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
                  border:
                    'var(--nf-space-px, 1px) solid var(--nf-color-border, var(--color-hairline))',
                  background: 'var(--nf-color-bg, var(--color-bg))',
                  color: 'var(--nf-color-fg, var(--color-fg))',
                  fontSize: 'var(--nf-text-sm, 0.875rem)',
                }}
              >
                <option value="en">English</option>
                <option value="ja">{t('profile.localeJa')}</option>
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
                  border:
                    'var(--nf-space-px, 1px) solid var(--nf-color-border, var(--color-hairline))',
                  background: 'var(--nf-color-bg, var(--color-bg))',
                  color: 'var(--nf-color-fg, var(--color-fg))',
                  fontSize: 'var(--nf-text-sm, 0.875rem)',
                }}
              >
                <option value="system">{t('profile.themeSystem')}</option>
                <option value="aurora-light">{t('profile.themeAuroraLight')}</option>
                <option value="aurora-dark">{t('profile.themeAuroraDark')}</option>
                <option value="dotline-light">{t('profile.themeDotlineLight')}</option>
                <option value="dotline-dark">{t('profile.themeDotlineDark')}</option>
              </select>
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
            {serverError}
          </p>
        ) : null}

        {success ? (
          <output
            style={{
              margin: 0,
              color: 'var(--nf-color-fg-success, var(--color-success, green))',
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
            color: 'var(--nf-color-fg-muted, var(--color-muted))',
          }}
        >
          <Link to="/security">{t('profile.securityLink')}</Link>
        </p>
      </form>
    </AuthCard>
  );
}

export const Route = createFileRoute('/_authenticated/profile')({
  component: ProfilePage,
});
