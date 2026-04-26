/**
 * AIAgentsError — fallback rendered when the AI invocations suspense
 * query fails. The component is shaped to satisfy react-error-boundary's
 * `FallbackProps` (`error` + `resetErrorBoundary`) so the parent section
 * can wire it directly via the `FallbackComponent` prop. Clicking the
 * Retry button calls `resetErrorBoundary()` which the section wires to a
 * query invalidation, so the next render refetches the list.
 */

import Button from '@nodate-flow/ui/primitives/button';
import type { ReactElement } from 'react';
import type { FallbackProps } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import styles from './ai-agents.module.css';

export default function AIAgentsError({ error, resetErrorBoundary }: FallbackProps): ReactElement {
  const { t } = useTranslation('aiAgents');
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
