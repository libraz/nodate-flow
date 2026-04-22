import { getOrCreateProvider } from '@nodate-flow/holidays';
import { SUPPORTED_COUNTRIES } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import { Plus, X } from 'lucide-react';
import { type ReactElement, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import {
  useCalendarsQuery,
  useDeleteCalendarMutation,
  useSubscribeSystemCalendarMutation,
} from './api';
import type { Calendar } from './types';

const HOLIDAY_PREFIX = 'holidays.';

interface CountryEntry {
  code: string;
  name: string;
  calendar: Calendar;
}

function calendarCountryCode(cal: Calendar): string | null {
  if (cal.kind !== 'system' || !cal.systemSlug?.startsWith(HOLIDAY_PREFIX)) return null;
  const cc = cal.systemSlug.slice(HOLIDAY_PREFIX.length).toUpperCase();
  return /^[A-Z]{2}$/.test(cc) ? cc : null;
}

export default function HolidaySubscriptions(): ReactElement {
  const { t, i18n } = useTranslation();
  const { data: calendars } = useCalendarsQuery();
  const subscribe = useSubscribeSystemCalendarMutation();
  const unsubscribe = useDeleteCalendarMutation();
  const [selectedCountry, setSelectedCountry] = useState('');

  const subscribed: CountryEntry[] = useMemo(() => {
    const out: CountryEntry[] = [];
    for (const cal of calendars ?? []) {
      const code = calendarCountryCode(cal);
      if (!code) continue;
      let name = SUPPORTED_COUNTRIES[code] ?? code;
      try {
        const localized = getOrCreateProvider(code).displayName(i18n.language);
        if (localized) name = localized;
      } catch {
        // keep SUPPORTED_COUNTRIES fallback
      }
      out.push({ code, name, calendar: cal });
    }
    out.sort((a, b) => a.name.localeCompare(b.name));
    return out;
  }, [calendars, i18n.language]);

  const subscribedCodes = useMemo(() => new Set(subscribed.map((s) => s.code)), [subscribed]);

  const availableCountries = useMemo(
    () =>
      Object.entries(SUPPORTED_COUNTRIES)
        .filter(([code]) => !subscribedCodes.has(code))
        .sort(([, a], [, b]) => a.localeCompare(b)),
    [subscribedCodes],
  );

  const handleAdd = (): void => {
    if (!selectedCountry) return;
    subscribe.mutate(selectedCountry, {
      onSuccess: () => setSelectedCountry(''),
    });
  };

  return (
    <div>
      <span
        style={{
          display: 'block',
          marginBlockEnd: 'var(--nf-space-2)',
          fontSize: 'var(--nf-text-xs)',
          fontWeight: 'var(--nf-weight-medium)',
          textTransform: 'uppercase',
          letterSpacing: '0.05em',
          color: 'var(--nf-color-fg-subtle)',
        }}
      >
        {t('settings.holiday_subscriptions')}
      </span>

      <ul
        style={{
          listStyle: 'none',
          margin: 0,
          padding: 0,
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-1)',
          marginBlockEnd: 'var(--nf-space-3)',
        }}
      >
        {subscribed.length === 0 ? (
          <li
            style={{
              fontSize: 'var(--nf-text-sm)',
              color: 'var(--nf-color-fg-muted)',
            }}
          >
            {t('settings.holiday_subscriptions_empty')}
          </li>
        ) : (
          subscribed.map((entry) => (
            <li
              key={entry.code}
              style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                padding: 'var(--nf-space-2) var(--nf-space-3)',
                borderRadius: 'var(--nf-radius-md)',
                border: '1px solid var(--nf-color-border)',
                background: 'var(--nf-color-bg-elevated)',
                fontSize: 'var(--nf-text-sm)',
              }}
            >
              <span>
                <span
                  style={{
                    display: 'inline-block',
                    minInlineSize: '2rem',
                    fontWeight: 'var(--nf-weight-semibold)',
                    color: 'var(--nf-color-fg-subtle)',
                  }}
                >
                  {entry.code}
                </span>
                <span style={{ color: 'var(--nf-color-fg)' }}>{entry.name}</span>
              </span>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => unsubscribe.mutate(entry.calendar.id)}
                aria-label={t('settings.holiday_unsubscribe_aria', { country: entry.name })}
                disabled={unsubscribe.isPending}
              >
                <X size={14} />
              </Button>
            </li>
          ))
        )}
      </ul>

      <div style={{ display: 'flex', gap: 'var(--nf-space-2)' }}>
        <select
          value={selectedCountry}
          onChange={(e) => setSelectedCountry(e.target.value)}
          style={{
            flex: 1,
            padding: 'var(--nf-space-2) var(--nf-space-3)',
            borderRadius: 'var(--nf-radius-md)',
            border: '1px solid var(--nf-color-border)',
            background: 'var(--nf-color-bg)',
            color: 'var(--nf-color-fg)',
            fontSize: 'var(--nf-text-sm)',
          }}
        >
          <option value="">{t('settings.holiday_add_placeholder')}</option>
          {availableCountries.map(([code, name]) => (
            <option key={code} value={code}>
              {code} — {name}
            </option>
          ))}
        </select>
        <Button
          variant="primary"
          size="sm"
          onClick={handleAdd}
          disabled={!selectedCountry || subscribe.isPending}
        >
          <Plus size={14} />
          {t('settings.holiday_add')}
        </Button>
      </div>

      {subscribe.isError ? (
        <p
          role="alert"
          style={{
            marginBlockStart: 'var(--nf-space-2)',
            marginBlockEnd: 0,
            fontSize: 'var(--nf-text-xs)',
            color: 'var(--nf-color-danger)',
          }}
        >
          {t('settings.holiday_add_failed')}
        </p>
      ) : null}
    </div>
  );
}
