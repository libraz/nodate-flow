/**
 * /profile -- Edit profile page (displayName, locale, theme, avatar).
 * Authenticated-only, guarded by the _authenticated layout route.
 */

import { zodResolver } from '@hookform/resolvers/zod';
import {
  SUPPORTED_COUNTRIES,
  detectTimezone,
  formatTimezoneLabel,
  groupTimezonesByRegion,
} from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import Combobox, { type ComboboxOption } from '@nodate-flow/ui/primitives/combobox';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import SegmentedControl, {
  type SegmentedControlOption,
} from '@nodate-flow/ui/primitives/segmented-control';
import { Link, createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useMemo, useState } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';

import AuthCard from '../../components/auth-card';
import { type ProfileFormValues, profileSchema } from '../../features/auth/auth-schemas';
import { type AuthUser, authStore, selectUser, useAuth } from '../../features/auth/auth-store';
import { type SupportedLanguage, setLanguage } from '../../i18n';
import { sdk } from '../../lib/sdk';
import { type ThemePreference, useTheme } from '../../providers/theme-provider';

// Sensible default country per UI language. Used to gently cascade the
// country field when the user switches language and the country is empty.
// These are ISO 3166-1 alpha-2 codes, not user-facing strings, so they live
// in code rather than locale files.
const LANGUAGE_DEFAULT_COUNTRY: Record<SupportedLanguage, string> = {
  en: 'US',
  ja: 'JP',
};

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

function ProfilePage(): ReactElement {
  const { t, i18n } = useTranslation('auth');
  const user = useAuth(selectUser);
  const { setPreference } = useTheme();
  const [serverError, setServerError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);

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

  const localeOptions: SegmentedControlOption<'en' | 'ja'>[] = [
    { value: 'en', label: 'English' },
    { value: 'ja', label: '日本語' },
  ];

  // `values` keeps the form in sync with the auth store; essential because
  // the user profile may populate asynchronously after the form mounts.
  const {
    register,
    control,
    handleSubmit,
    getValues,
    setValue,
    formState: { errors, isSubmitting, isDirty },
  } = useForm<ProfileFormValues>({
    resolver: zodResolver(profileSchema),
    values: {
      displayName: user?.displayName ?? '',
      locale: (user?.locale as 'en' | 'ja') ?? 'en',
      timezone: user?.timezone || detectTimezone(),
      country: user?.country ?? '',
      themePreference: (user?.themePreference as ProfileFormValues['themePreference']) ?? 'system',
    },
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
      const nextTz = next === 'ja' ? 'Asia/Tokyo' : detectTimezone();
      setValue('timezone', nextTz, { shouldDirty: true, shouldValidate: true });
    }
  };

  const onSubmit = async (values: ProfileFormValues): Promise<void> => {
    setServerError(null);
    setSuccess(false);
    try {
      const { data, error } = await sdk.PATCH('/me', {
        body: {
          displayName: values.displayName,
          locale: values.locale,
          timezone: values.timezone,
          country: values.country,
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
        timezone: me.timezone,
        country: me.country,
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
    <AuthCard width="wide">
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
          {() => (
            <Controller
              name="locale"
              control={control}
              render={({ field }) => (
                <SegmentedControl<'en' | 'ja'>
                  fullWidth
                  value={field.value}
                  onChange={handleLocaleChange}
                  options={localeOptions}
                  ariaLabel={t('profile.locale')}
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
                <option value="glass-light">{t('profile.theme_glass_light')}</option>
                <option value="glass-dark">{t('profile.theme_glass_dark')}</option>
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

        <Button type="submit" variant="primary" disabled={isSubmitting || !isDirty}>
          {isSubmitting ? t('profile.saving') : t('profile.save')}
        </Button>

        <p
          style={{
            margin: 0,
            fontSize: 'var(--nf-text-sm, 0.875rem)',
            color: 'var(--nf-color-fg-muted)',
            display: 'flex',
            gap: 'var(--nf-space-4)',
          }}
        >
          <Link to="/security">{t('profile.security_link')}</Link>
          <Link to="/workspaces">{t('profile.workspaces_link')}</Link>
        </p>
      </form>
    </AuthCard>
  );
}

export const Route = createFileRoute('/_authenticated/profile')({
  component: ProfilePage,
});
