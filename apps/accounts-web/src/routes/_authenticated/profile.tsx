/**
 * /profile -- Edit profile page (displayName, locale, theme, avatar).
 * Authenticated-only, guarded by the _authenticated layout route.
 */

import {
  type components,
  detectTimezone,
  formatTimezoneLabel,
  groupTimezonesByRegion,
  SUPPORTED_COUNTRIES,
} from '@nodate-flow/sdk';
import { useZodForm } from '@nodate-flow/ui/hooks/use-zod-form';
import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import Combobox, { type ComboboxOption } from '@nodate-flow/ui/primitives/combobox';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import SegmentedControl, {
  type SegmentedControlOption,
} from '@nodate-flow/ui/primitives/segmented-control';
import { createFileRoute, Link } from '@tanstack/react-router';
import { type ReactElement, useEffect, useMemo, useState } from 'react';
import { Controller } from 'react-hook-form';
import { useTranslation } from 'react-i18next';

import AuthCard from '../../components/auth-card';
import { type ProfileFormValues, profileSchema } from '../../features/auth/auth-schemas';
import { type AuthUser, authStore, selectUser, useAuth } from '../../features/auth/auth-store';
import { userFromMe } from '../../features/auth/user-from-me';
import { type SupportedLanguage, setLanguage } from '../../i18n';
import type { ProblemJson } from '../../lib/api-error';
import { mapAuthError } from '../../lib/auth-errors';
import { sdk } from '../../lib/sdk';
import { useSubmitGuard } from '../../lib/use-submit-guard';
import { type ThemePreference, useTheme } from '../../providers/theme-provider';

// Sensible default country per UI language. Used to gently cascade the
// country field when the user switches language and the country is empty.
// These are ISO 3166-1 alpha-2 codes, not user-facing strings, so they live
// in code rather than locale files.
const LANGUAGE_DEFAULT_COUNTRY: Record<SupportedLanguage, string> = {
  en: 'US',
  ja: 'JP',
  zh: 'CN',
};

/**
 * The auth-api `/me` response, whole.
 *
 * It used to be narrowed to the fields this form reads, which made it
 * easy to rebuild the session from a subset and lose the rest. The
 * session is rebuilt through `userFromMe`, so the full shape is what
 * belongs here.
 */
type MeResponse = components['schemas']['MeBody'];

/** Allowed `weekStart` values, mirrored from the SDK `MeBody` enum. */
type WeekStart = components['schemas']['MeBody']['weekStart'];

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

/**
 * Resolves the count of workspaces the user belongs to so the page can
 * surface a first-run CTA when the value is zero.
 *
 * The auth store does not currently track workspaces, so we honour an
 * optional `workspaces` field if the caller (or a test) populated it
 * inline on the user record, and otherwise lazily fetch GET /workspaces
 * once. Returns `undefined` while loading so the caller can treat the
 * "unknown" state as "do not flash the CTA".
 */
function useWorkspaceCount(user: AuthUser | null): number | undefined {
  // Honour an inline override on the user object first. This keeps the
  // contract documented in the task (`user.workspaces.length === 0`)
  // working even when the auth store has not been extended with the
  // field, and makes the CTA trivially testable by mounting the page
  // with a stubbed user.
  const inline = (user as Partial<{ workspaces: readonly unknown[] }> | null)?.workspaces;
  const inlineLen = Array.isArray(inline) ? inline.length : null;

  const [fetched, setFetched] = useState<number | undefined>(undefined);
  useEffect(() => {
    // Skip the network call when the answer is already on the user
    // record or when there is no authenticated user yet.
    if (inlineLen != null) return;
    if (!user) return;
    let cancelled = false;
    void sdk
      .GET('/workspaces', { params: { query: { limit: 1, offset: 0 } } })
      .then((result) => {
        if (cancelled) return;
        if (result.error || !result.data) {
          // Treat fetch errors as "unknown" so we never falsely promote
          // the empty-state CTA when the user already has workspaces
          // but the request happened to fail.
          setFetched(undefined);
          return;
        }
        // Field name follows GET /workspaces -> WorkspacesListOutputBody:
        // `workspaces`, NOT `items`. We still tolerate `total` first because
        // the endpoint always populates it, even when the array is null.
        const body = result.data as components['schemas']['WorkspacesListOutputBody'];
        const total =
          typeof body.total === 'number'
            ? body.total
            : Array.isArray(body.workspaces)
              ? body.workspaces.length
              : 0;
        setFetched(total);
      })
      .catch(() => {
        if (!cancelled) setFetched(undefined);
      });
    return () => {
      cancelled = true;
    };
  }, [inlineLen, user]);

  return inlineLen ?? fetched;
}

/**
 * Origin of the flow-web app where /setup lives. Configurable via the
 * `VITE_FLOW_WEB_URL` env var; defaults to a same-origin path so the
 * common mono-deploy case "just works".
 */
function flowWebSetupUrl(): string {
  const base = import.meta.env.VITE_FLOW_WEB_URL as string | undefined;
  if (typeof base === 'string' && base.length > 0) {
    return `${base.replace(/\/$/, '')}/setup`;
  }
  return '/setup';
}

export function ProfilePage(): ReactElement {
  const { t, i18n } = useTranslation('auth');
  const user = useAuth(selectUser);
  const { setPreference } = useTheme();
  const [serverError, setServerError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);
  const submitGuard = useSubmitGuard();
  const workspaceCount = useWorkspaceCount(user);
  // Only show the CTA once we know the count is exactly zero. While the
  // count is `undefined` we render nothing so the page does not flash
  // the empty-state for users who actually have workspaces.
  const showEmptyWorkspacesCta = workspaceCount === 0;

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

  // The session carries `weekStart`, so the form starts from what the
  // server actually holds. The locale default below only applies when the
  // account has never had one — reaching for it while a stored value
  // exists is what let "Saturday" turn back into "Sunday" on reload and
  // then overwrite the real setting on the next save.
  const persistedWeekStart = user?.weekStart as WeekStart | undefined;
  const userLocale = (user?.locale ?? 'en') as SupportedLanguage | string;
  const fallbackWeekStart: WeekStart = userLocale === 'ja' || userLocale === 'zh' ? 'mon' : 'sun';

  // `values` keeps the form in sync with the auth store; essential because
  // the user profile may populate asynchronously after the form mounts.
  const {
    register,
    control,
    handleSubmit,
    getValues,
    setValue,
    formState: { errors, isSubmitting, isDirty },
  } = useZodForm<ProfileFormValues>(
    profileSchema,
    {
      displayName: user?.displayName ?? '',
      locale: (user?.locale as ProfileFormValues['locale']) ?? 'en',
      timezone: user?.timezone || detectTimezone(),
      country: user?.country ?? '',
      themePreference: (user?.themePreference as ProfileFormValues['themePreference']) ?? 'system',
      weekStart: persistedWeekStart ?? fallbackWeekStart,
    },
    {
      values: {
        displayName: user?.displayName ?? '',
        locale: (user?.locale as ProfileFormValues['locale']) ?? 'en',
        timezone: user?.timezone || detectTimezone(),
        country: user?.country ?? '',
        themePreference:
          (user?.themePreference as ProfileFormValues['themePreference']) ?? 'system',
        weekStart: persistedWeekStart ?? fallbackWeekStart,
      },
    },
  );

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
    // round-tripping the profile does not silently override their choice.
    const currentWeekStart = getValues('weekStart');
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

  const onSubmit = async (values: ProfileFormValues): Promise<void> => {
    if (submitGuard.guard()) return;
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
          weekStart: values.weekStart,
        },
      });
      if (error || !data) {
        // Route the server error through the shared auth-error mapper so
        // validation / session-revoked / specific codes surface their
        // localized messages; mapAuthError falls back to `errors.unknown`
        // for anything it cannot classify.
        setServerError(t(mapAuthError(error as ProblemJson | undefined)));
        return;
      }
      const me = data as MeResponse;
      // Rebuild through the shared mapper rather than copying fields by
      // hand. The hand-written version silently dropped every field it
      // did not list — `weekStart` and `avatarUrl` among them — so saving
      // any part of the profile erased them from the session, and the
      // next save wrote the erased value back to the server.
      authStore.getState().setSession(authStore.getState().accessToken ?? '', userFromMe(me));
      setPreference(values.themePreference as ThemePreference);
      setLanguage(values.locale as SupportedLanguage);
      setSuccess(true);
    } catch {
      setServerError(t('errors.unknown'));
    } finally {
      submitGuard.end();
    }
  };

  return (
    <AuthCard width="wide">
      <form
        onSubmit={(e) => {
          void handleSubmit(onSubmit)(e);
        }}
        noValidate
        style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-5)' }}
      >
        <h1
          style={{
            fontFamily: 'var(--nf-font-sans)',
            fontSize: 'var(--nf-text-2xl)',
            margin: 0,
          }}
        >
          {t('profile.title')}
        </h1>

        {showEmptyWorkspacesCta ? (
          <Card
            data-testid="empty-workspaces-cta"
            elevated
            style={{
              padding: 'var(--nf-space-5)',
              display: 'flex',
              flexDirection: 'column',
              gap: 'var(--nf-space-3)',
            }}
          >
            <h2
              style={{
                margin: 0,
                fontFamily: 'var(--nf-font-sans)',
                fontSize: 'var(--nf-text-lg)',
              }}
            >
              {t('profile.empty_workspaces.title')}
            </h2>
            <output
              style={{
                margin: 0,
                color: 'var(--nf-color-fg-muted)',
                fontSize: 'var(--nf-text-sm)',
              }}
            >
              {t('profile.empty_workspaces.body')}
            </output>
            <Button
              type="button"
              variant="primary"
              onClick={() => {
                window.location.href = flowWebSetupUrl();
              }}
            >
              {t('profile.empty_workspaces.cta')}
            </Button>
          </Card>
        ) : null}

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
              <select {...control} {...field} ref={ref} className="aw-select">
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
          <p role="alert" className="aw-error">
            {serverError}
          </p>
        ) : null}

        {success ? (
          <output
            style={{
              margin: 0,
              color: 'var(--nf-color-success-fg)',
              fontSize: 'var(--nf-text-sm)',
            }}
          >
            {t('profile.saved')}
          </output>
        ) : null}

        <Button
          type="submit"
          variant="primary"
          disabled={isSubmitting || submitGuard.submitting || !isDirty}
        >
          {isSubmitting || submitGuard.submitting ? t('profile.saving') : t('profile.save')}
        </Button>

        <p
          style={{
            margin: 0,
            fontSize: 'var(--nf-text-sm)',
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
