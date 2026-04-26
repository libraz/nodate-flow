/**
 * PriorityError — fallback rendered when the AI priority suggestions
 * suspense query fails. Shaped to satisfy react-error-boundary's
 * `FallbackProps` so the page wrapper can wire it via `FallbackComponent`.
 * Clicking Retry calls `resetErrorBoundary()`, which the wrapper bridges
 * to a query invalidation so the next render refetches the suggestion
 * list transparently.
 *
 * Thin wrapper around the shared {@link ErrorFallback} primitive. The
 * `tone="inline"` variant fits the priority page surface which already
 * provides its own page-level chrome.
 */

import ErrorFallback from '@nodate-flow/ui/primitives/error-fallback';
import type { ReactElement } from 'react';
import type { FallbackProps } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

export default function PriorityError({ error, resetErrorBoundary }: FallbackProps): ReactElement {
  const { t } = useTranslation('aiPriority');
  return (
    <ErrorFallback
      tone="inline"
      title={t('error.fetchFailed')}
      action={{ label: t('error.retry'), onClick: resetErrorBoundary }}
      error={error}
    />
  );
}
