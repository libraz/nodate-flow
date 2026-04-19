/**
 * ProfileForm — edit the authenticated user's display name, locale,
 * and theme preference. Parent must wrap this in `<Suspense>` because
 * it consumes `useMeQuery` (Suspense mode).
 */

import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import ThemePicker, {
  DEFAULT_THEME_PREVIEWS,
  type ThemeFamilyEntry,
} from '@nodate-flow/ui/primitives/theme-picker';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import {
  type ColorMode,
  type ThemeFamily,
  type ThemeId,
  joinThemeId,
  splitThemeId,
} from '@nodate-flow/ui/providers/theme-provider';
import { type FormEvent, type ReactElement, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { type SupportedLanguage, setLanguage } from '../../i18n';
import { useTheme } from '../../providers/theme-provider';
import { type PatchMeInput, useMeQuery, useUpdateMe } from './api';

const LOCALES: readonly SupportedLanguage[] = ['en', 'ja'] as const;

function localeLabelKey(l: SupportedLanguage): string {
  return l === 'en' ? 'profile.locale_en' : 'profile.locale_ja';
}

export default function ProfileForm(): ReactElement {
  const { t } = useTranslation('settings');
  const { data: me } = useMeQuery();
  const update = useUpdateMe();
  const { preference, resolved, setPreference } = useTheme();

  const [displayName, setDisplayName] = useState<string>(me.displayName);
  const [locale, setLocaleState] = useState<string>(me.locale);
  const [displayNameError, setDisplayNameError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // Derive family and colorMode from the live theme preference.
  const family: ThemeFamily = splitThemeId(resolved).family;
  const colorMode: ColorMode =
    preference === 'system' ? 'system' : splitThemeId(preference as ThemeId).mode;

  const themeEntries: ThemeFamilyEntry[] = useMemo(
    () => [
      { id: 'glass', label: t('profile.theme_glass'), preview: DEFAULT_THEME_PREVIEWS.glass },
      { id: 'aurora', label: t('profile.theme_aurora'), preview: DEFAULT_THEME_PREVIEWS.aurora },
      {
        id: 'dotline',
        label: t('profile.theme_dotline'),
        preview: DEFAULT_THEME_PREVIEWS.dotline,
      },
    ],
    [t],
  );

  const colorModeEntries = useMemo(
    () => [
      { mode: 'light' as const, label: t('profile.color_mode_light') },
      { mode: 'dark' as const, label: t('profile.color_mode_dark') },
      { mode: 'system' as const, label: t('profile.color_mode_system') },
    ],
    [t],
  );

  const handleFamilyChange = (f: ThemeFamily): void => {
    const currentMode = splitThemeId(resolved).mode;
    const next = joinThemeId(f, currentMode);
    if (next) setPreference(next);
  };

  const handleColorModeChange = (m: ColorMode): void => {
    if (m === 'system') {
      setPreference('system');
    } else {
      const next = joinThemeId(family, m);
      if (next) setPreference(next);
    }
  };

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
    };
    try {
      await update.mutateAsync(patch);
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
    <div
      style={{ display: 'flex', flexDirection: 'column', gap: '1.5rem', maxInlineSize: '32rem' }}
    >
      <form
        onSubmit={(e) => {
          void handleSubmit(e);
        }}
        style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}
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

        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
          <Button type="submit" variant="primary" disabled={submitting}>
            {submitting ? t('profile.saving') : t('profile.save')}
          </Button>
        </div>
      </form>

      {/* Theme picker — changes apply immediately (synced to server via ThemeProvider). */}
      <ThemePicker
        themes={themeEntries}
        selectedTheme={family}
        colorModes={colorModeEntries}
        selectedColorMode={colorMode}
        onThemeChange={handleFamilyChange}
        onColorModeChange={handleColorModeChange}
        themeLabel={t('profile.theme')}
        colorModeLabel={t('profile.color_mode')}
      />
    </div>
  );
}
