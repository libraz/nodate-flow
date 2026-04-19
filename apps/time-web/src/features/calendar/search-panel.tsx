import { useQueryClient } from '@tanstack/react-query';
import { Search, X } from 'lucide-react';
import { DateTime } from 'luxon';
import { type ReactElement, useMemo, useRef } from 'react';
import { useTranslation } from 'react-i18next';

import { useCalendarUi } from '../../stores/calendar-ui-store';
import { calendarKeys } from './api';
import styles from './search-panel.module.css';
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
  const showSearch = useCalendarUi((s) => s.showSearch);
  const searchQuery = useCalendarUi((s) => s.searchQuery);
  const setSearchQuery = useCalendarUi((s) => s.setSearchQuery);
  const toggleSearch = useCalendarUi((s) => s.toggleSearch);
  const setSelectedDate = useCalendarUi((s) => s.setSelectedDate);
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
      <div className={styles.mobileOverlay}>
        <div className={styles.searchBar}>
          <Search size={20} className={styles.searchIcon} />
          <input
            ref={inputRef}
            type="text"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder={t('search.placeholder')}
            className={styles.searchInput}
            // biome-ignore lint/a11y/noAutofocus: search panel auto-focus is expected
            autoFocus
          />
          <button
            type="button"
            onClick={toggleSearch}
            className={styles.closeBtn}
            aria-label={t('search.close')}
          >
            <X size={20} />
          </button>
        </div>
        <div className={styles.scrollArea}>
          <SearchResults results={results} onSelect={handleResultClick} />
        </div>
      </div>

      {/* Desktop: inline below header */}
      <div className={styles.desktopWrapper}>
        <div className={styles.desktopInner}>
          <Search size={16} className={styles.searchIcon} />
          <input
            type="text"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder={t('search.placeholder')}
            className={styles.searchInput}
            // biome-ignore lint/a11y/noAutofocus: search panel auto-focus is expected
            autoFocus
          />
          <button
            type="button"
            onClick={toggleSearch}
            className={styles.closeBtn}
            aria-label={t('search.close')}
          >
            <X size={16} />
          </button>
        </div>
        {searchQuery.trim() ? (
          <div className={styles.dropdown}>
            <div className={styles.dropdownInner}>
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
    return <p className={styles.noResults}>{t('search.noResults')}</p>;
  }

  return (
    <div className={styles.resultList}>
      {results.map((event) => {
        const start = DateTime.fromISO(event.startAt);
        return (
          <button
            key={event.id}
            type="button"
            onClick={() => onSelect(event)}
            className={styles.resultItem}
          >
            <div className={styles.resultDot} />
            <div className={styles.resultContent}>
              <p className={styles.resultTitle}>{event.title}</p>
              <p className={styles.resultMeta}>
                {event.allDay
                  ? start.setLocale(i18n.language).toLocaleString(DateTime.DATE_MED)
                  : start.setLocale(i18n.language).toLocaleString(DateTime.DATETIME_MED)}
              </p>
              {event.location ? <p className={styles.resultLocation}>{event.location}</p> : null}
            </div>
          </button>
        );
      })}
    </div>
  );
}
