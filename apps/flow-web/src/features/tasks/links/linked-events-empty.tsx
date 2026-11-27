/**
 * LinkedEventsEmpty — the one muted line shown inside the section body
 * when no events are linked yet. The header already carries the
 * "Link event" button.
 */

import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';
import styles from './linked-events.module.css';

export default function LinkedEventsEmpty(): ReactElement {
  const { t } = useTranslation('linkedEvents');
  return <p className={styles.empty}>{t('empty.title')}</p>;
}
