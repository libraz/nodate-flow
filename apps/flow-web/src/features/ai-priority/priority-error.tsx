/**
 * PriorityError — fallback rendered when the AI priority suggestions
 * suspense query fails. Shaped to satisfy react-error-boundary's
 * `FallbackProps` so the page wrapper can wire it via `FallbackComponent`.
 * Clicking Retry calls `resetErrorBoundary()`, which the wrapper bridges
 * to a query invalidation so the next render refetches the suggestion
 * list transparently.
 */

import Button from '@nodate-flow/ui/primitives/button';
import type { ReactElement } from 'react';
import type { FallbackProps } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import styles from './priority-page.module.css';

export default function PriorityError({ error, resetErrorBoundary }: FallbackProps): ReactElement {
  const { t } = useTranslation('aiPriority');
  return (
    <div className={styles.errorState} role="alert">
      <p className={styles.errorMessage}>{t('error.fetchFailed')}</p>
      <Button type="button" variant="ghost" size="sm" onClick={resetErrorBoundary}>
        {t('error.retry')}
      </Button>
      {/*
        The raw `error.message` is intentionally not surfaced — the
        translated copy is enough for the user, and the SDK already
        forwards the structured error to the route-level boundary for
        diagnostics.
      */}
      <span hidden>{error instanceof Error ? error.message : ''}</span>
    </div>
  );
}
