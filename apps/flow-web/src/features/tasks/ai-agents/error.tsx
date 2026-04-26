/**
 * AIAgentsError — fallback rendered when the AI invocations suspense
 * query fails. The component is shaped to satisfy react-error-boundary's
 * `FallbackProps` (`error` + `resetErrorBoundary`) so the parent section
 * can wire it directly via the `FallbackComponent` prop. Clicking the
 * Retry button calls `resetErrorBoundary()` which the section wires to a
 * query invalidation, so the next render refetches the list.
 *
 * Thin wrapper around the shared {@link ErrorFallback} primitive — the
 * raw `error.message` is forwarded to the primitive's hidden diagnostic
 * span so the translated copy is the only thing announced to users. The
 * `tone="inline"` variant fits the agents section body which already
 * sits inside a card-like surface.
 */

import ErrorFallback from '@nodate-flow/ui/primitives/error-fallback';
import type { ReactElement } from 'react';
import type { FallbackProps } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

export default function AIAgentsError({ error, resetErrorBoundary }: FallbackProps): ReactElement {
  const { t } = useTranslation('aiAgents');
  return (
    <ErrorFallback
      tone="inline"
      title={t('error.fetchFailed')}
      action={{ label: t('error.retry'), onClick: resetErrorBoundary }}
      error={error}
    />
  );
}
