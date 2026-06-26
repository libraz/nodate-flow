/**
 * ProfileForm — edit the authenticated user's display name, locale,
 * timezone, country, and theme preference. Parent must wrap this in
 * `<Suspense>` because it consumes `useMeQuery` (Suspense mode).
 */

import {
  detectTimezone,
  formatTimezoneLabel,
  groupTimezonesByRegion,
  SUPPORTED_COUNTRIES,
} from '@nodate-flow/sdk';
import { useZodForm } from '@nodate-flow/ui/hooks/use-zod-form';
import Button from '@nodate-flow/ui/primitives/button';
import Combobox, { type ComboboxOption } from '@nodate-flow/ui/primitives/combobox';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import SegmentedControl, {
  type SegmentedControlOption,
} from '@nodate-flow/ui/primitives/segmented-control';
import ThemePicker, {
  DEFAULT_THEME_PREVIEWS,
  type ThemeFamilyEntry,
} from '@nodate-flow/ui/primitives/theme-picker';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import {
  type ColorMode,
  joinThemeId,
  splitThemeId,
  type ThemeFamily,
  type ThemeId,
} from '@nodate-flow/ui/providers/theme-provider';
import { type ReactElement, useMemo } from 'react';
import { Controller } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';

import { type SupportedLanguage, setLanguage } from '../../i18n';
import { useTheme } from '../../providers/theme-provider';
import { type Me, type PatchMeInput, useMeQuery, useUpdateMe } from './api';
import AvatarUpload from './avatar-upload';

const LOCALES: readonly SupportedLanguage[] = ['en', 'ja', 'zh'] as const;

/**
 * Allowed `weekStart` values, mirrored from the SDK `MeBody.weekStart` enum.
 * The form accepts these three plus the empty string (untouched / not yet
 * loaded), and the empty case is filtered out before PATCH.
 */
type WeekStart = NonNullable<Me['weekStart']>;

// Sensible default country per UI language. Used to gently cascade the
// country field when the user switches language and the country is empty.
// These are ISO 3166-1 alpha-2 codes, not user-facing strings, so they live
// in code rather than locale files.
const LANGUAGE_DEFAULT_COUNTRY: Record<SupportedLanguage, string> = {
  en: 'US',
  ja: 'JP',
  zh: 'CN',
};

const profileSchema = z.object({
  displayName: z.string().min(1, 'profile.validation.display_name_required'),
  locale: z.enum(['en', 'ja', 'zh']),
  timezone: z.string().min(1, 'profile.validation.timezone_required'),
  country: z
    .string()
    .regex(/^([A-Z]{2})?$/, 'profile.validation.country_invalid')
    .or(z.literal('')),
  // Mirrors `MeBody.weekStart`. Asserted via the `WeekStart` alias above so a
  // future SDK enum widening surfaces here as a type error.
  weekStart: z.enum(['mon', 'sun', 'sat']),
});

type ProfileFormValues = z.infer<typeof profileSchema>;

// Compile-time assertion: keep the form's `weekStart` enum aligned with the
// SDK `MeBody.weekStart` enum. If the OpenAPI source widens or narrows the
// union, this line fails first.
const WeekStartCheck: ProfileFormValues['weekStart'] extends WeekStart
  ? WeekStart extends ProfileFormValues['weekStart']
    ? true
    : false
  : false = true;
void WeekStartCheck;

/**
 * Convert a 2-letter ISO 3166-1 alpha-2 country code into its regional
 * indicator emoji sequence (e.g. `JP` → `🇯🇵`). Returns an empty string for
 * malformed codes so callers can safely concatenate the result.
 */
function countryFlag(code: string): string {
  if (!/^[A-Z]{2}$/.test(code)) return '';
  const A = 0x1f1e6;
  const base = 'A'.charCodeAt(0);
  return String.fromCodePoint(A + code.charCodeAt(0) - base, A + code.charCodeAt(1) - base);
}

export default function ProfileForm(): ReactElement {
  const { t, i18n } = useTranslation('settings');
  const { data: me } = useMeQuery();
  const update = useUpdateMe();
  const { preference, resolved, setPreference } = useTheme();

  // Flatten the IANA-region-grouped timezone list into Combobox options so
  // users can search by region (`Asia`), city (`Tokyo`), or full id
  // (`Asia/Tokyo`) — all match the same label substring.
  const timezoneOptions = useMemo<ComboboxOption[]>(() => {
    const groups = groupTimezonesByRegion();
    const out: ComboboxOption[] = [];
    for (const { region, zones } of groups) {
      for (const tz of zones) {
        out.push({
          value: tz,
          label: region === 'Global' ? tz : `${region} / ${formatTimezoneLabel(tz)}`,
        });
      }
    }
    return out;
  }, []);

  // Country options carry a flag emoji + localized name so users can scan
  // visually and type either the localized name, the English fallback, or
  // the alpha-2 code (which is appended in parentheses) to filter.
  const countryOptions = useMemo<ComboboxOption[]>(() => {
    let displayNames: Intl.DisplayNames | undefined;
    try {
      displayNames = new Intl.DisplayNames([i18n.language], { type: 'region' });
    } catch {
      displayNames = undefined;
    }
    const entries = Object.keys(SUPPORTED_COUNTRIES).map((code) => {
      const localName = displayNames?.of(code) ?? SUPPORTED_COUNTRIES[code] ?? code;
      const flag = countryFlag(code);
      return {
        value: code,
        label: `${flag} ${localName} (${code})`,
        sortKey: localName,
      };
    });
    entries.sort((a, b) => a.sortKey.localeCompare(b.sortKey, i18n.language));
    const list: ComboboxOption[] = [{ value: '', label: t('profile.country_unset') }];
    for (const e of entries) list.push({ value: e.value, label: e.label });
    return list;
  }, [i18n.language, t]);

  // The label for each segment is the language's *own* native name. Even
  // though we route the strings through `t()` so every visible token
  // remains explicit i18n-compliant, the values match across en/ja/zh
  // locales: a language picker should always read "English / 日本語 / 中文",
  // never "English / Japanese / Chinese".
  const localeOptions: SegmentedControlOption<SupportedLanguage>[] = [
    { value: 'en', label: t('profile.locale.en') },
    { value: 'ja', label: t('profile.locale.ja') },
    { value: 'zh', label: t('profile.locale.zh') },
  ];

  const weekStartOptions: SegmentedControlOption<WeekStart>[] = [
    { value: 'mon', label: t('profile.week_start.mon') },
    { value: 'sun', label: t('profile.week_start.sun') },
    { value: 'sat', label: t('profile.week_start.sat') },
  ];

  const {
    register,
    control,
    handleSubmit,
    getValues,
    setValue,
    formState: { errors, isSubmitting },
  } = useZodForm<ProfileFormValues>(profileSchema, {
    displayName: me.displayName,
    locale: (me.locale as ProfileFormValues['locale']) ?? 'en',
    timezone: me.timezone || detectTimezone(),
    country: me.country ?? '',
    weekStart: (me.weekStart as WeekStart) ?? (me.locale === 'ja' ? 'mon' : 'sun'),
  });

  // Apply a locale change immediately so the rest of the form (country
  // names via Intl.DisplayNames, validation messages, etc.) reflects the
  // new UI language without waiting for save. Also cascades the country
  // default when the country field is still empty -- if the user has
  // explicitly chosen a country, we leave it alone.
  const handleLocaleChange = (next: SupportedLanguage): void => {
    setValue('locale', next, { shouldDirty: true });
    setLanguage(next);
    if (!getValues('country')) {
      setValue('country', LANGUAGE_DEFAULT_COUNTRY[next], {
        shouldDirty: true,
        shouldValidate: true,
      });
    }
    if (getValues('timezone') === 'UTC') {
      const tzMap: Record<SupportedLanguage, string | null> = {
        en: null,
        ja: 'Asia/Tokyo',
        zh: 'Asia/Shanghai',
      };
      const nextTz = tzMap[next] ?? detectTimezone();
      setValue('timezone', nextTz, { shouldDirty: true, shouldValidate: true });
    }
    // Cascade the week-start preference only when the user has not made an
    // explicit pick. We treat the server's persisted value as "explicit" so
    // round-tripping `me` data does not silently override their choice.
    const currentWeekStart = getValues('weekStart');
    const persistedWeekStart = me.weekStart as WeekStart | undefined;
    if (!currentWeekStart || currentWeekStart === persistedWeekStart) {
      const weekStartMap: Record<SupportedLanguage, WeekStart> = {
        en: 'sun',
        ja: 'mon',
        zh: 'mon',
      };
      const nextWeekStart: WeekStart = weekStartMap[next];
      if (currentWeekStart !== nextWeekStart) {
        setValue('weekStart', nextWeekStart, { shouldDirty: true, shouldValidate: true });
      }
    }
  };

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
      timezone: values.timezone,
      country: values.country,
      weekStart: values.weekStart,
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
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-6)',
        // Cap the form's reading-line at the narrow single-column measure
        // (32rem ~ 512px). `--nf-space-16` is 4rem (step 16 in the spacing
        // scale, not 16rem), so the previous `calc(var(--nf-space-16) * 2)`
        // resolved to 8rem and crushed the form to 128px.
        maxInlineSize: 'var(--nf-measure-narrow)',
      }}
    >
      <AvatarUpload user={me} />
      <form
        onSubmit={(e) => {
          void handleSubmit(onSubmit)(e);
        }}
        noValidate
        style={{
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-4)',
        }}
      >
        <FormField
          label={t('profile.display_name')}
          required
          {...(errors.displayName?.message ? { error: t(errors.displayName.message) } : {})}
        >
          {(control2) => {
            const { ref, ...field } = register('displayName');
            return <Input {...control2} {...field} ref={ref} />;
          }}
        </FormField>

        <FormField label={t('profile.locale.label')}>
          {() => (
            <Controller
              name="locale"
              control={control}
              render={({ field }) => (
                <SegmentedControl<SupportedLanguage>
                  fullWidth
                  value={field.value}
                  onChange={handleLocaleChange}
                  options={localeOptions}
                  ariaLabel={t('profile.locale.label')}
                />
              )}
            />
          )}
        </FormField>

        <FormField label={t('profile.week_start.label')} description={t('profile.week_start.help')}>
          {() => (
            <Controller
              name="weekStart"
              control={control}
              render={({ field }) => (
                <SegmentedControl<WeekStart>
                  fullWidth
                  value={field.value}
                  onChange={field.onChange}
                  options={weekStartOptions}
                  ariaLabel={t('profile.week_start.label')}
                />
              )}
            />
          )}
        </FormField>

        <FormField
          label={t('profile.timezone')}
          {...(errors.timezone?.message ? { error: t(errors.timezone.message) } : {})}
        >
          {(control2) => (
            <Controller
              name="timezone"
              control={control}
              render={({ field }) => (
                <Combobox
                  id={control2.id}
                  options={timezoneOptions}
                  value={field.value}
                  onChange={field.onChange}
                  aria-label={t('profile.timezone')}
                />
              )}
            />
          )}
        </FormField>

        <FormField
          label={t('profile.country')}
          description={t('profile.country_help')}
          {...(errors.country?.message ? { error: t(errors.country.message) } : {})}
        >
          {(control2) => (
            <Controller
              name="country"
              control={control}
              render={({ field }) => (
                <Combobox
                  id={control2.id}
                  options={countryOptions}
                  value={field.value}
                  onChange={field.onChange}
                  aria-label={t('profile.country')}
                />
              )}
            />
          )}
        </FormField>

        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
          <Button type="submit" variant="primary" disabled={isSubmitting || update.isPending}>
            {isSubmitting || update.isPending ? t('profile.saving') : t('profile.save')}
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
