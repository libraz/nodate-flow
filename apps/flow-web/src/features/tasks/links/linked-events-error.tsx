/**
 * LinkedEventsError — fallback rendered when the linked-events query
 * fails. The component is exported in the shape react-error-boundary
 * expects so callers can wire it up directly via the
 * `FallbackComponent` prop, but it's also usable as a plain element
 * (the `error` argument is the `Error` thrown by the suspense query).
 */

import Button from '@nodate-flow/ui/primitives/button';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './linked-events.module.css';

export interface LinkedEventsErrorProps {
  error: Error;
  onRetry: () => void;
}

export default function LinkedEventsError({
  error,
  onRetry,
}: LinkedEventsErrorProps): ReactElement {
  const { t } = useTranslation('linkedEvents');
  return (
    <div className={styles.errorState} role="alert">
      <p className={styles.errorMessage}>{t('error.fetchFailed')}</p>
      <Button type="button" variant="ghost" size="sm" onClick={onRetry}>
        {t('error.retry')}
      </Button>
      {/*
        The raw `error.message` is intentionally not surfaced — the
        translated copy is enough for the user, and the SDK already
        forwards the structured error to the route-level boundary for
        diagnostics.
      */}
      <span hidden>{error.message}</span>
    </div>
  );
}
