/**
 * ProfileForm — edit the authenticated user's display name, locale,
 * and theme preference. Parent must wrap this in `<Suspense>` because
 * it consumes `useMeQuery` (Suspense mode).
 */

import { zodResolver } from '@hookform/resolvers/zod';
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
import type { ReactElement } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';

import { type SupportedLanguage, setLanguage } from '../../i18n';
import { useTheme } from '../../providers/theme-provider';
import { type PatchMeInput, useMeQuery, useUpdateMe } from './api';

const LOCALES: readonly SupportedLanguage[] = ['en', 'ja'] as const;

const profileSchema = z.object({
  displayName: z.string().min(1, 'profile.validation.display_name_required'),
  locale: z.enum(['en', 'ja']),
});

type ProfileFormValues = z.infer<typeof profileSchema>;

function localeLabelKey(l: SupportedLanguage): string {
  return l === 'en' ? 'profile.locale_en' : 'profile.locale_ja';
}

export default function ProfileForm(): ReactElement {
  const { t } = useTranslation('settings');
  const { data: me } = useMeQuery();
  const update = useUpdateMe();
  const { preference, resolved, setPreference } = useTheme();

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<ProfileFormValues>({
    resolver: zodResolver(profileSchema),
    defaultValues: {
      displayName: me.displayName,
      locale: (me.locale as ProfileFormValues['locale']) ?? 'en',
    },
  });

  // Derive family and colorMode from the live theme preference.
  const family: ThemeFamily = splitThemeId(resolved).family;
  const colorMode: ColorMode =
    preference === 'system' ? 'system' : splitThemeId(preference as ThemeId).mode;

  const themeEntries: ThemeFamilyEntry[] = [
    { id: 'glass', label: t('profile.theme_glass'), preview: DEFAULT_THEME_PREVIEWS.glass },
    { id: 'aurora', label: t('profile.theme_aurora'), preview: DEFAULT_THEME_PREVIEWS.aurora },
    {
      id: 'dotline',
      label: t('profile.theme_dotline'),
      preview: DEFAULT_THEME_PREVIEWS.dotline,
    },
  ];

  const colorModeEntries = [
    { mode: 'light' as const, label: t('profile.color_mode_light') },
    { mode: 'dark' as const, label: t('profile.color_mode_dark') },
    { mode: 'system' as const, label: t('profile.color_mode_system') },
  ];

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

  const onSubmit = async (values: ProfileFormValues): Promise<void> => {
    const patch: PatchMeInput = {
      displayName: values.displayName,
      locale: values.locale,
    };
    try {
      await update.mutateAsync(patch);
      if ((LOCALES as readonly string[]).includes(values.locale)) {
        setLanguage(values.locale);
      }
      toaster.show({ tone: 'success', message: t('profile.saved') });
    } catch {
      toaster.show({ tone: 'danger', message: t('profile.errors.update_failed') });
    }
  };

  return (
    <div
      style={{ display: 'flex', flexDirection: 'column', gap: '1.5rem', maxInlineSize: '32rem' }}
    >
      <form
        onSubmit={(e) => {
          void handleSubmit(onSubmit)(e);
        }}
        noValidate
        style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}
      >
        <FormField
          label={t('profile.display_name')}
          required
          {...(errors.displayName?.message ? { error: t(errors.displayName.message) } : {})}
        >
          {(control) => {
            const { ref, ...field } = register('displayName');
            return <Input {...control} {...field} ref={ref} />;
          }}
        </FormField>

        <FormField label={t('profile.locale')}>
          {(control) => {
            const { ref, ...field } = register('locale');
            return (
              <Select {...control} {...field} ref={ref}>
                {LOCALES.map((l) => (
                  <option key={l} value={l}>
                    {t(localeLabelKey(l))}
                  </option>
                ))}
              </Select>
            );
          }}
        </FormField>

        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
          <Button type="submit" variant="primary" disabled={isSubmitting}>
            {isSubmitting ? t('profile.saving') : t('profile.save')}
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
