import { ChevronLeft, ChevronRight, Menu, Plus, Search } from 'lucide-react';
import { DateTime } from 'luxon';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import Button from '@nodate-flow/ui/primitives/button';

import { useCalendarUi } from '../../stores/calendar-ui-store';
import styles from './calendar-header.module.css';

export default function CalendarHeader(): ReactElement {
  const { t, i18n } = useTranslation();
  const selectedDate = useCalendarUi((s) => s.selectedDate);
  const displayMonth = useCalendarUi((s) => s.displayMonth);
  const currentView = useCalendarUi((s) => s.currentView);
  const setCurrentView = useCalendarUi((s) => s.setCurrentView);
  const goToPrevious = useCalendarUi((s) => s.goToPrevious);
  const goToNext = useCalendarUi((s) => s.goToNext);
  const goToToday = useCalendarUi((s) => s.goToToday);
  const toggleSidebar = useCalendarUi((s) => s.toggleSidebar);
  const toggleSearch = useCalendarUi((s) => s.toggleSearch);
  const openEventModal = useCalendarUi((s) => s.openEventModal);

  const title =
    currentView === 'week'
      ? `${selectedDate.startOf('week', { useLocaleWeeks: true }).setLocale(i18n.language).toLocaleString({ month: 'short', day: 'numeric' })} - ${selectedDate.endOf('week', { useLocaleWeeks: true }).setLocale(i18n.language).toLocaleString(DateTime.DATE_MED)}`
      : displayMonth.toLocaleString({ month: 'long', year: 'numeric' });

  const prevLabel =
    currentView === 'week' ? t('calendar.previous_week') : t('calendar.previous_month');
  const nextLabel = currentView === 'week' ? t('calendar.next_week') : t('calendar.next_month');

  return (
    <header className={`glass-surface-heavy ${styles.header}`}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--nf-space-1)' }}>
        <span className={styles.mobileOnly}>
          <Button
            variant="ghost"
            size="sm"
            onClick={toggleSidebar}
            aria-label={t('calendar.toggle_sidebar')}
          >
            <Menu size={20} />
          </Button>
        </span>
        <Button variant="ghost" size="sm" onClick={goToPrevious} aria-label={prevLabel}>
          <ChevronLeft size={20} />
        </Button>
        <Button variant="ghost" size="sm" onClick={goToNext} aria-label={nextLabel}>
          <ChevronRight size={20} />
        </Button>
        <Button
          variant="ghost"
          size="sm"
          onClick={goToToday}
          style={{
            marginInlineStart: 'var(--nf-space-1)',
            borderRadius: 'var(--nf-radius-pill)',
            backgroundColor: 'var(--nf-color-accent-subtle)',
            color: 'var(--nf-color-accent)',
            fontWeight: 'var(--nf-weight-medium)',
          }}
        >
          {t('calendar.today')}
        </Button>
      </div>

      <h1
        style={{
          fontSize: 'var(--nf-text-base)',
          fontWeight: 'var(--nf-weight-semibold)',
          color: 'var(--nf-color-fg)',
        }}
      >
        {title}
      </h1>

      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--nf-space-2)' }}>
        <span className={styles.desktopOnly}>
          <Button
            variant="primary"
            size="sm"
            onClick={() => openEventModal()}
            style={{ borderRadius: 'var(--nf-radius-pill)' }}
          >
            <Plus size={16} />
            {t('calendar.create_new_event')}
          </Button>
        </span>
        <Button
          variant="ghost"
          size="sm"
          onClick={toggleSearch}
          aria-label={t('search.search_events')}
        >
          <Search size={20} />
        </Button>
        <div className={`segmented-control ${styles.segmented}`}>
          {(['month', 'week'] as const).map((view) => (
            <button
              key={view}
              type="button"
              data-active={currentView === view}
              onClick={() => setCurrentView(view)}
            >
              {t(`calendar.${view}` as const)}
            </button>
          ))}
        </div>
      </div>
    </header>
  );
}
