/**
 * HolidaysList — inline body that lets the actor subscribe their
 * workspace to a national holiday feed by picking an ISO 3166-1 alpha-2
 * country code.
 *
 * Rendered inside {@link CalendarsRail} when its section is in
 * "holidays" mode, mirroring the discovery view's morph pattern. The
 * section header (back arrow + title) is owned by the parent so this
 * component renders only the body.
 *
 * The country select defaults to the workspace's own `country` if
 * present; the user can override before submitting. On success a single
 * toast announces the subscription and the rail returns to list mode
 * via {@link onClose}, where the new holiday calendar surfaces in the
 * subscribed list.
 */

import { SUPPORTED_COUNTRIES } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import Combobox, { type ComboboxOption } from '@nodate-flow/ui/primitives/combobox';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { useSubscribeSystemCalendarMutation } from './api';
import styles from './calendars-rail.module.css';

interface HolidaysListProps {
  /** Workspace whose holiday subscription is being added. */
  workspaceId: string;
  /**
   * Workspace's own configured country code, used as the default
   * pre-selection. Empty / missing means the picker starts unset and
   * the user must explicitly choose before submitting.
   */
  defaultCountry?: string;
  /**
   * Invoked after a successful subscription so the rail can return to
   * list mode. Failed submits leave the picker mounted so the user can
   * retry without re-opening the section.
   */
  onClose: () => void;
}

/**
 * Convert a 2-letter ISO 3166-1 alpha-2 code into its regional indicator
 * emoji sequence (e.g. `JP` → `🇯🇵`). Returns an empty string for
 * malformed codes so callers can safely concatenate the result.
 *
 * Mirrors the helper in `features/settings/profile-form.tsx`. Inlined
 * here rather than extracted because the helper is two lines and a
 * cross-feature import (settings → calendars-rail) reads worse than
 * the duplication.
 */
function countryFlag(code: string): string {
  if (!/^[A-Z]{2}$/.test(code)) return '';
  const A = 0x1f1e6;
  const base = 'A'.charCodeAt(0);
  return String.fromCodePoint(A + code.charCodeAt(0) - base, A + code.charCodeAt(1) - base);
}

export default function HolidaysList({
  workspaceId,
  defaultCountry,
  onClose,
}: HolidaysListProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const subscribe = useSubscribeSystemCalendarMutation();

  const [country, setCountry] = useState<string>(defaultCountry ?? '');

  // Build a localized, alphabetised country list. Each option carries a
  // flag emoji + the locale-aware display name + the alpha-2 code in
  // parentheses so users can filter by typing any of them.
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
    return entries.map((e) => ({ value: e.value, label: e.label }));
  }, [i18n.language]);

  const selectedLabel = useMemo<string | undefined>(() => {
    if (!country) return undefined;
    let displayNames: Intl.DisplayNames | undefined;
    try {
      displayNames = new Intl.DisplayNames([i18n.language], { type: 'region' });
    } catch {
      displayNames = undefined;
    }
    return displayNames?.of(country) ?? SUPPORTED_COUNTRIES[country] ?? country;
  }, [country, i18n.language]);

  const handleSubscribe = (): void => {
    if (country.length === 0) return;
    subscribe.mutate(
      { wsId: workspaceId, country },
      {
        onSuccess: () => {
          toaster.show({
            tone: 'success',
            message: t('calendars_rail.holidays.subscribed_toast', {
              name: selectedLabel ?? country,
            }),
          });
          onClose();
        },
        onError: (err) => {
          toaster.show({
            tone: 'danger',
            message: formatApiError(err, t, 'calendars_rail.holidays.error'),
          });
        },
      },
    );
  };

  const submitDisabled = country.length === 0 || subscribe.isPending;

  return (
    <div className={styles.discoverBody}>
      <div className={styles.holidaysField}>
        <label className={styles.holidaysLabel} htmlFor="holidays-country-picker">
          {t('calendars_rail.holidays.country_label')}
        </label>
        <Combobox
          id="holidays-country-picker"
          value={country}
          onChange={setCountry}
          options={countryOptions}
          placeholder={t('calendars_rail.holidays.country_placeholder')}
          aria-label={t('calendars_rail.holidays.country_label')}
        />
        {defaultCountry && country === defaultCountry ? (
          <p className={styles.holidaysHint}>
            {t('calendars_rail.holidays.default_hint', {
              flag: countryFlag(defaultCountry),
              code: defaultCountry,
            })}
          </p>
        ) : null}
      </div>
      <div className={styles.holidaysActions}>
        <Button
          type="button"
          variant="primary"
          size="sm"
          onClick={handleSubscribe}
          disabled={submitDisabled}
        >
          {t('calendars_rail.holidays.subscribe')}
        </Button>
      </div>
    </div>
  );
}
