/**
 * LinkedEventsError — fallback rendered when the linked-events query
 * fails. The component is exported in the shape react-error-boundary
 * expects so callers can wire it up directly via the
 * `FallbackComponent` prop, but it's also usable as a plain element
 * (the `error` argument is the `Error` thrown by the suspense query).
 *
 * Thin wrapper around the shared {@link ErrorFallback} primitive. The
 * `resetErrorBoundary ?? onRetry` shim is preserved so direct callers
 * that bypass the boundary keep working.
 */

import ErrorFallback from '@nodate-flow/ui/primitives/error-fallback';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

export interface LinkedEventsErrorProps {
  error: Error;
  /**
   * Reset handler. Compatible with react-error-boundary's FallbackProps
   * (`resetErrorBoundary`). The legacy `onRetry` alias remains for
   * direct callers that bypass the boundary.
   */
  resetErrorBoundary?: () => void;
  onRetry?: () => void;
}

export default function LinkedEventsError({
  error,
  resetErrorBoundary,
  onRetry,
}: LinkedEventsErrorProps): ReactElement {
  const { t } = useTranslation('linkedEvents');
  const handleRetry = resetErrorBoundary ?? onRetry ?? ((): void => {});
  return (
    <ErrorFallback
      tone="inline"
      title={t('error.fetchFailed')}
      action={{ label: t('error.retry'), onClick: handleRetry }}
      error={error}
    />
  );
}
