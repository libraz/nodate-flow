/**
 * ArchivedErrorState — fallback shown when the archived-tasks query
 * fails. Delegates to the shared {@link ErrorFallback} primitive so the
 * surface degrades per-section (commit 5bf9eca: compact intent), the
 * same way every other section-level error uses the primitive.
 *
 * The bespoke centred SVG illustration that lived here previously was
 * dropped in favour of the primitive — the page already owns its own
 * chrome and the priority is consistent rendering across themes, not a
 * dedicated illustration for the failure path.
 */

import ErrorFallback from '@nodate-flow/ui/primitives/error-fallback';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

export interface ArchivedErrorStateProps {
  onRetry: () => void;
}

export default function ArchivedErrorState({ onRetry }: ArchivedErrorStateProps): ReactElement {
  const { t } = useTranslation('archive');
  return (
    <ErrorFallback
      tone="card"
      title={t('error.fetchFailed')}
      action={{ label: t('error.retry'), onClick: onRetry }}
    />
  );
}
