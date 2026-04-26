/**
 * /workspaces/$id/settings/ai/metrics — workspace AI metrics dashboard
 * (lazy).
 *
 * Surfaces proposal / acceptance counters and per-provider egress
 * rate-limit stats for the workspace AI surfaces over a configurable
 * rolling window (7 / 30 / 90 days). Read-only.
 *
 * The active window is reflected in the `?windowDays=` URL search
 * param so the view is shareable / refreshable. The view is a
 * suspense boundary that swaps in a skeleton matching the final
 * layout while the metrics query resolves.
 */

import { createLazyFileRoute, getRouteApi, useNavigate } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { ErrorBoundary, type FallbackProps } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import styles from '../features/ai-metrics/ai-metrics.module.css';
import { type AiMetricsWindow, DEFAULT_WINDOW, coerceWindowDays } from '../features/ai-metrics/api';
import MetricsDashboard from '../features/ai-metrics/metrics-dashboard';
import MetricsError from '../features/ai-metrics/metrics-error';
import MetricsSkeleton from '../features/ai-metrics/metrics-skeleton';
import WindowSelector from '../features/ai-metrics/window-selector';
import AccessRestricted from '../features/workspaces/access-restricted';
import { ApiError } from '../lib/api-error';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/ai/metrics');

/**
 * Routes 401 / 403 to <AccessRestricted>; everything else is rendered
 * inline as a small retryable error panel so transient failures stay
 * scoped to the metrics card and do not collapse the whole settings
 * shell to the root FatalFallback.
 */
function MetricsErrorFallback({ error, resetErrorBoundary }: FallbackProps): ReactElement {
  if (error instanceof ApiError && (error.httpStatus === 401 || error.httpStatus === 403)) {
    return <AccessRestricted sectionTitleKey="nav.ai_metrics" />;
  }
  return <MetricsError onRetry={resetErrorBoundary} />;
}

function AiMetricsRoute(): ReactElement {
  const { t } = useTranslation('aiMetrics');
  const { id } = routeApi.useParams();
  const search = routeApi.useSearch();
  const navigate = useNavigate();

  const windowDays: AiMetricsWindow = coerceWindowDays(search.windowDays);

  const handleWindowChange = (next: AiMetricsWindow): void => {
    void navigate({
      to: '/workspaces/$id/settings/ai/metrics',
      params: { id },
      search: { windowDays: next === DEFAULT_WINDOW ? undefined : next },
      replace: true,
    });
  };

  return (
    <section className={styles.root}>
      <header className={styles.header}>
        <h1 className={styles.title}>{t('page.title')}</h1>
        <p className={styles.description}>{t('page.description')}</p>
      </header>
      <WindowSelector value={windowDays} onChange={handleWindowChange} />
      <ErrorBoundary FallbackComponent={MetricsErrorFallback} resetKeys={[windowDays]}>
        <Suspense fallback={<MetricsSkeleton />}>
          <MetricsDashboard workspaceId={id} windowDays={windowDays} />
        </Suspense>
      </ErrorBoundary>
    </section>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/ai/metrics')({
  component: AiMetricsRoute,
});
