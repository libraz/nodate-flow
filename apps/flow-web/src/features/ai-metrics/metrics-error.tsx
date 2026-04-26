/**
 * MetricsError — small error panel rendered by the local
 * ErrorBoundary when the AI metrics query fails for a non-permission
 * reason (network, 5xx, ...). 401 / 403 should be filtered out by the
 * caller and routed to <AccessRestricted> instead so this surface is
 * only ever shown for transient / retryable failures.
 */

import Button from '@nodate-flow/ui/primitives/button';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './ai-metrics.module.css';

export interface MetricsErrorProps {
  onRetry: () => void;
}

export default function MetricsError({ onRetry }: MetricsErrorProps): ReactElement {
  const { t } = useTranslation('aiMetrics');
  return (
    <div className={styles.errorPanel} role="alert">
      <p className={styles.errorMessage}>{t('error.fetchFailed')}</p>
      <div>
        <Button variant="default" onClick={onRetry} type="button">
          {t('error.retry')}
        </Button>
      </div>
    </div>
  );
}
