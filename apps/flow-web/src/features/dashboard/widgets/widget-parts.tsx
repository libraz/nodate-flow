/**
 * Shared pieces for dashboard widget bodies: the loading / error boundary,
 * the skeleton, and the empty / error message blocks.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { QueryErrorResetBoundary } from '@tanstack/react-query';
import { type ReactElement, type ReactNode, Suspense } from 'react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import styles from './widgets.module.css';

/** Maximum rows a list widget shows before pointing at the full page. */
export const WIDGET_MAX_ITEMS = 5;

/** Compact date + time for a unix-second timestamp. */
export function formatShortTime(epochSec: number, locale: string): string {
  return new Intl.DateTimeFormat(locale, {
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  }).format(new Date(epochSec * 1000));
}

export function WidgetSkeleton(): ReactElement {
  const { t } = useTranslation('dashboard');
  return (
    <div className={styles.body} role="status" aria-busy="true" aria-label={t('widgets.loading')}>
      <div className={styles.skeletonRows}>
        <Skeleton className={styles.skeletonLine} />
        <Skeleton className={styles.skeletonLine} />
        <Skeleton className={styles.skeletonLineShort} />
      </div>
    </div>
  );
}

export interface WidgetMessageProps {
  title: string;
  hint?: string | undefined;
  /** Renders a retry button when provided. */
  onRetry?: (() => void) | undefined;
  role?: 'alert' | undefined;
}

/** Empty, error or coming-soon block: a short title and what to expect. */
export function WidgetMessage({ title, hint, onRetry, role }: WidgetMessageProps): ReactElement {
  const { t } = useTranslation('dashboard');
  return (
    <div className={styles.body}>
      <div className={styles.message} role={role}>
        <p className={styles.messageTitle}>{title}</p>
        {hint ? <p className={styles.messageHint}>{hint}</p> : null}
        {onRetry ? (
          <Button type="button" size="sm" className={styles.messageAction} onClick={onRetry}>
            {t('widgets.retry')}
          </Button>
        ) : null}
      </div>
    </div>
  );
}

/** Error block with the shared wording and a retry action. */
export function WidgetError({ onRetry }: { onRetry: () => void }): ReactElement {
  const { t } = useTranslation('dashboard');
  return (
    <WidgetMessage
      role="alert"
      title={t('widgets.error_title')}
      hint={t('widgets.error_hint')}
      onRetry={onRetry}
    />
  );
}

/** Scopes loading and failure of a suspense-backed widget to its own card. */
export function WidgetBoundary({ children }: { children: ReactNode }): ReactElement {
  return (
    <QueryErrorResetBoundary>
      {({ reset }) => (
        <ErrorBoundary
          onReset={reset}
          fallbackRender={({ resetErrorBoundary }) => <WidgetError onRetry={resetErrorBoundary} />}
        >
          <Suspense fallback={<WidgetSkeleton />}>{children}</Suspense>
        </ErrorBoundary>
      )}
    </QueryErrorResetBoundary>
  );
}
