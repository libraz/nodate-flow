import { Plus } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { useCalendarUiStore } from '../../stores/calendar-ui-store';

export default function FabButton(): ReactElement {
  const { t } = useTranslation();
  const openEventModal = useCalendarUiStore((s) => s.openEventModal);

  return (
    <button
      type="button"
      onClick={() => openEventModal()}
      className="fab-button fixed z-20 flex h-14 w-14 items-center justify-center transition-transform hover:scale-105 active:scale-90 sm:hidden"
      style={{
        bottom: 'calc(60px + env(safe-area-inset-bottom))',
        right: '16px',
        borderRadius: 'var(--radius-lg, 20px)',
        background: 'var(--color-accent)',
        color: '#ffffff',
        boxShadow: 'var(--shadow-elevated)',
      }}
      aria-label={t('calendar.createNewEvent')}
    >
      <Plus className="h-6 w-6" />
    </button>
  );
}
