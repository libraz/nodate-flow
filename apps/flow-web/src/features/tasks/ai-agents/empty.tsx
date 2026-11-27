/**
 * AIAgentsEmpty — the one muted line shown when the task has no recorded
 * AI invocations yet, so the section recedes until an agent acts.
 */

import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';
import styles from './ai-agents.module.css';

export default function AIAgentsEmpty(): ReactElement {
  const { t } = useTranslation('aiAgents');
  return <p className={styles.empty}>{t('empty.title')}</p>;
}
