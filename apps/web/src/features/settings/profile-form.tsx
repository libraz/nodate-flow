/**
 * ProfileForm — edit the authenticated user's display name, locale,
 * and theme preference. Parent must wrap this in `<Suspense>` because
 * it consumes `useMeQuery` (Suspense mode).
 */

import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { type SupportedLanguage, setLanguage } from '../../i18n';
import { type ThemePreference, useTheme } from '../../providers/theme-provider';
import { type PatchMeInput, useMeQuery, useUpdateMe } from './api';

const LOCALES: readonly SupportedLanguage[] = ['en', 'ja'] as const;
const THEME_OPTIONS: readonly ThemePreference[] = [
  'system',
  'aurora-light',
  'aurora-dark',
  'dotline-light',
  'dotline-dark',
] as const;

function themeLabelKey(t: ThemePreference): string {
  switch (t) {
    case 'system':
      return 'profile.theme_system';
    case 'aurora-light':
      return 'profile.theme_aurora_light';
    case 'aurora-dark':
      return 'profile.theme_aurora_dark';
    case 'dotline-light':
      return 'profile.theme_dotline_light';
    case 'dotline-dark':
      return 'profile.theme_dotline_dark';
  }
}

function localeLabelKey(l: SupportedLanguage): string {
  return l === 'en' ? 'profile.locale_en' : 'profile.locale_ja';
}

export default function ProfileForm(): ReactElement {
  const { t } = useTranslation('settings');
  const { data: me } = useMeQuery();
  const update = useUpdateMe();
  const { setPreference } = useTheme();

  const [displayName, setDisplayName] = useState<string>(me.displayName);
  const [locale, setLocaleState] = useState<string>(me.locale);
  const [themePreference, setThemePreferenceState] = useState<ThemePreference>(me.themePreference);
  const [displayNameError, setDisplayNameError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    const trimmed = displayName.trim();
    if (trimmed === '') {
      setDisplayNameError(t('profile.validation.display_name_required'));
      return;
    }
    setDisplayNameError(null);
    setSubmitting(true);
    const patch: PatchMeInput = {
      displayName: trimmed,
      locale,
      themePreference,
    };
    try {
      await update.mutateAsync(patch);
      // Sync local UI side-effects with the new server state.
      setPreference(themePreference);
      if ((LOCALES as readonly string[]).includes(locale)) {
        setLanguage(locale as SupportedLanguage);
      }
      toaster.show({ tone: 'success', message: t('profile.saved') });
    } catch {
      toaster.show({ tone: 'danger', message: t('profile.errors.update_failed') });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <form
      onSubmit={(e) => {
        void handleSubmit(e);
      }}
      style={{ display: 'flex', flexDirection: 'column', gap: '1rem', maxInlineSize: '32rem' }}
    >
      <FormField
        label={t('profile.display_name')}
        required
        {...(displayNameError ? { error: displayNameError } : {})}
      >
        {(control) => (
          <Input
            {...control}
            value={displayName}
            onChange={(e) => {
              setDisplayName(e.target.value);
            }}
          />
        )}
      </FormField>

      <FormField label={t('profile.locale')}>
        {(control) => (
          <Select
            {...control}
            value={locale}
            onChange={(e) => {
              setLocaleState(e.target.value);
            }}
          >
            {LOCALES.map((l) => (
              <option key={l} value={l}>
                {t(localeLabelKey(l))}
              </option>
            ))}
          </Select>
        )}
      </FormField>

      <FormField label={t('profile.theme')}>
        {(control) => (
          <Select
            {...control}
            value={themePreference}
            onChange={(e) => {
              setThemePreferenceState(e.target.value as ThemePreference);
            }}
          >
            {THEME_OPTIONS.map((th) => (
              <option key={th} value={th}>
                {t(themeLabelKey(th))}
              </option>
            ))}
          </Select>
        )}
      </FormField>

      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <Button type="submit" variant="primary" disabled={submitting}>
          {submitting ? t('profile.saving') : t('profile.save')}
        </Button>
      </div>
    </form>
  );
}
