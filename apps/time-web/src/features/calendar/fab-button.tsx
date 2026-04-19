import { Plus } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { useCalendarUi } from '../../stores/calendar-ui-store';
import styles from './fab-button.module.css';

export default function FabButton(): ReactElement {
  const { t } = useTranslation();
  const openEventModal = useCalendarUi((s) => s.openEventModal);

  return (
    <button
      type="button"
      onClick={() => openEventModal()}
      className={styles.fab}
      aria-label={t('calendar.createNewEvent')}
    >
      <Plus size={24} />
    </button>
  );
}
