/**
 * MetricsError — small error panel rendered by the local
 * ErrorBoundary when the AI metrics query fails for a non-permission
 * reason (network, 5xx, ...). 401 / 403 should be filtered out by the
 * caller and routed to <AccessRestricted> instead so this surface is
 * only ever shown for transient / retryable failures.
 *
 * Thin wrapper around the shared {@link ErrorFallback} primitive. Uses
 * the `card` tone because the metrics dashboard places this surface as
 * a standalone panel, not inside an already-bordered container.
 */

import ErrorFallback from '@nodate-flow/ui/primitives/error-fallback';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

export interface MetricsErrorProps {
  onRetry: () => void;
}

export default function MetricsError({ onRetry }: MetricsErrorProps): ReactElement {
  const { t } = useTranslation('aiMetrics');
  return (
    <ErrorFallback
      tone="card"
      title={t('error.fetchFailed')}
      action={{ label: t('error.retry'), onClick: onRetry }}
    />
  );
}
