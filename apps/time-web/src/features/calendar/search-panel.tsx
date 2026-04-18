import { useQueryClient } from '@tanstack/react-query';
import { Search, X } from 'lucide-react';
import { DateTime } from 'luxon';
import { type ReactElement, useMemo, useRef } from 'react';
import { useTranslation } from 'react-i18next';

import { useCalendarUiStore } from '../../stores/calendar-ui-store';
import { calendarKeys } from './api';
import type { CalendarEvent } from './types';

function useSearchResults(query: string): CalendarEvent[] {
  const qc = useQueryClient();

  return useMemo(() => {
    if (!query.trim()) return [];

    const lowerQuery = query.toLowerCase();
    const results: CalendarEvent[] = [];
    const seen = new Set<string>();

    const cache = qc.getQueriesData<CalendarEvent[]>({
      queryKey: calendarKeys.all,
    });

    for (const [, data] of cache) {
      if (!Array.isArray(data)) continue;
      for (const event of data) {
        if (seen.has(event.id)) continue;
        if (
          event.title.toLowerCase().includes(lowerQuery) ||
          event.location?.toLowerCase().includes(lowerQuery) ||
          event.memo?.toLowerCase().includes(lowerQuery)
        ) {
          seen.add(event.id);
          results.push(event);
        }
      }
    }

    results.sort((a, b) => a.startAt.localeCompare(b.startAt));
    return results.slice(0, 20);
  }, [query, qc]);
}

export default function SearchPanel(): ReactElement | null {
  const { t } = useTranslation();
  const { showSearch, searchQuery, setSearchQuery, toggleSearch, setSelectedDate } =
    useCalendarUiStore();
  const inputRef = useRef<HTMLInputElement>(null);
  const results = useSearchResults(searchQuery);

  if (!showSearch) return null;

  const handleResultClick = (event: CalendarEvent) => {
    setSelectedDate(DateTime.fromISO(event.startAt));
    toggleSearch();
  };

  return (
    <>
      {/* Mobile: full-screen overlay */}
      <div
        className="fixed inset-0 z-50 flex flex-col sm:hidden"
        style={{ backgroundColor: 'var(--color-surface-elevated)' }}
      >
        <div className="flex items-center gap-2 border-b border-[var(--color-border)] px-4 py-3">
          <Search className="h-5 w-5 shrink-0" style={{ color: 'var(--color-text-tertiary)' }} />
          <input
            ref={inputRef}
            type="text"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder={t('search.placeholder')}
            className="flex-1 text-sm bg-transparent focus:outline-none"
            style={{ color: 'var(--color-text-primary)' }}
            // biome-ignore lint/a11y/noAutofocus: search panel auto-focus is expected
            autoFocus
          />
          <button
            type="button"
            onClick={toggleSearch}
            className="rounded-md p-1 hover:opacity-80"
            style={{ color: 'var(--color-text-tertiary)' }}
            aria-label={t('search.close')}
          >
            <X className="h-5 w-5" />
          </button>
        </div>
        <div className="flex-1 overflow-y-auto">
          <SearchResults results={results} onSelect={handleResultClick} />
        </div>
      </div>

      {/* Desktop: inline below header */}
      <div
        className="relative hidden border-b border-[var(--color-border)] sm:block"
        style={{ backgroundColor: 'var(--color-surface-elevated)' }}
      >
        <div className="mx-auto flex max-w-2xl items-center gap-2 px-4 py-2">
          <Search className="h-4 w-4 shrink-0" style={{ color: 'var(--color-text-tertiary)' }} />
          <input
            type="text"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder={t('search.placeholder')}
            className="flex-1 text-sm bg-transparent focus:outline-none"
            style={{ color: 'var(--color-text-primary)' }}
            // biome-ignore lint/a11y/noAutofocus: search panel auto-focus is expected
            autoFocus
          />
          <button
            type="button"
            onClick={toggleSearch}
            className="rounded-md p-1 hover:opacity-80"
            style={{ color: 'var(--color-text-tertiary)' }}
            aria-label={t('search.close')}
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        {searchQuery.trim() ? (
          <div
            className="absolute left-0 right-0 z-30 max-h-80 overflow-y-auto border-b border-[var(--color-border)]"
            style={{
              backgroundColor: 'var(--color-surface-elevated)',
              boxShadow: 'var(--shadow-lg)',
            }}
          >
            <div className="mx-auto max-w-2xl">
              <SearchResults results={results} onSelect={handleResultClick} />
            </div>
          </div>
        ) : null}
      </div>
    </>
  );
}

function SearchResults({
  results,
  onSelect,
}: {
  results: CalendarEvent[];
  onSelect: (event: CalendarEvent) => void;
}): ReactElement {
  const { t, i18n } = useTranslation();

  if (results.length === 0) {
    return (
      <p className="px-4 py-6 text-center text-sm" style={{ color: 'var(--color-text-tertiary)' }}>
        {t('search.noResults')}
      </p>
    );
  }

  return (
    <div className="divide-y divide-[var(--color-separator)]">
      {results.map((event) => {
        const start = DateTime.fromISO(event.startAt);
        return (
          <button
            key={event.id}
            type="button"
            onClick={() => onSelect(event)}
            className="flex w-full items-start gap-3 px-4 py-3 text-left hover:bg-[var(--color-hover)]"
          >
            <div className="mt-0.5 h-2.5 w-2.5 shrink-0 rounded-full bg-[var(--color-accent)]" />
            <div className="min-w-0 flex-1">
              <p
                className="truncate text-sm font-medium"
                style={{ color: 'var(--color-text-primary)' }}
              >
                {event.title}
              </p>
              <p className="text-xs" style={{ color: 'var(--color-text-secondary)' }}>
                {event.allDay
                  ? start.setLocale(i18n.language).toLocaleString(DateTime.DATE_MED)
                  : start.setLocale(i18n.language).toLocaleString(DateTime.DATETIME_MED)}
              </p>
              {event.location ? (
                <p className="truncate text-xs" style={{ color: 'var(--color-text-tertiary)' }}>
                  {event.location}
                </p>
              ) : null}
            </div>
          </button>
        );
      })}
    </div>
  );
}
