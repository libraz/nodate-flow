/**
 * Combobox options for the timezone and country pickers shared by the
 * profile and workspace settings forms.
 */

import { formatTimezoneLabel, groupTimezonesByRegion, SUPPORTED_COUNTRIES } from '@nodate-flow/sdk';
import type { ComboboxOption } from '@nodate-flow/ui/primitives/combobox';
import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';

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

export function useRegionOptions(): {
  timezoneOptions: ComboboxOption[];
  countryOptions: ComboboxOption[];
} {
  const { t, i18n } = useTranslation('auth');

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

  return { timezoneOptions, countryOptions };
}
