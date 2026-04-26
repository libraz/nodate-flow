/**
 * AIAgentsEmpty — placeholder shown when the task has no recorded AI
 * invocations yet. Kept intentionally quiet so the section recedes
 * until an agent actually does something against the task.
 */

import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './ai-agents.module.css';

export default function AIAgentsEmpty(): ReactElement {
  const { t } = useTranslation('aiAgents');
  return (
    <div className={styles.empty}>
      <p className={styles.emptyTitle}>{t('empty.title')}</p>
      <p className={styles.emptyBody}>{t('empty.body')}</p>
    </div>
  );
}
