/**
 * /workspaces/$id/settings/ai/usage — AI usage tab (lazy): the recent
 * invocation audit followed by the proposal / acceptance metrics.
 *
 * The active metrics window is reflected in the `?windowDays=` search
 * param so the view is shareable / refreshable. Each section has its own
 * boundary: a 401 / 403 from either replaces the whole tab with the
 * restricted panel (the outer boundary), anything else becomes an inline
 * error with a retry that leaves the other section untouched.
 */

import { createLazyFileRoute, getRouteApi, useNavigate } from '@tanstack/react-router';
import { type ReactElement, type ReactNode, Suspense } from 'react';
import { ErrorBoundary, type FallbackProps } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import SectionHeader from '../components/section-header';
import styles from '../features/ai-metrics/ai-metrics.module.css';
import { type AiMetricsWindow, coerceWindowDays, DEFAULT_WINDOW } from '../features/ai-metrics/api';
import MetricsDashboard from '../features/ai-metrics/metrics-dashboard';
import MetricsError from '../features/ai-metrics/metrics-error';
import MetricsSkeleton from '../features/ai-metrics/metrics-skeleton';
import WindowSelector from '../features/ai-metrics/window-selector';
import InvocationsList from '../features/ai-providers/invocations-list';
import SettingsListSkeleton from '../features/settings/settings-list-skeleton';
import ui from '../features/settings/settings-ui.module.css';
import AccessRestricted from '../features/workspaces/access-restricted';
import { ApiError } from '../lib/api-error';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/ai/usage');

function isAuthError(error: unknown): boolean {
  return error instanceof ApiError && (error.httpStatus === 401 || error.httpStatus === 403);
}

/** Outermost: only 401 / 403 are handled here. */
function TabErrorFallback({ error }: FallbackProps): ReactElement {
  if (isAuthError(error)) return <AccessRestricted sectionTitleKey="nav.ai" variant="tab" />;
  throw error;
}

/** Per section: 401 / 403 bubble to the tab boundary, the rest retry inline. */
function SectionErrorFallback({ error, resetErrorBoundary }: FallbackProps): ReactNode {
  if (isAuthError(error)) throw error;
  return <MetricsError onRetry={resetErrorBoundary} />;
}

function AiUsageRoute(): ReactElement {
  const { t } = useTranslation('aiMetrics');
  const { id } = routeApi.useParams();
  const search = routeApi.useSearch();
  const navigate = useNavigate();

  const windowDays: AiMetricsWindow = coerceWindowDays(search.windowDays);

  const handleWindowChange = (next: AiMetricsWindow): void => {
    void navigate({
      to: '/workspaces/$id/settings/ai/usage',
      params: { id },
      search: { windowDays: next === DEFAULT_WINDOW ? undefined : next },
      replace: true,
    });
  };

  return (
    <ErrorBoundary FallbackComponent={TabErrorFallback}>
      <div className={ui.page}>
        <ErrorBoundary FallbackComponent={SectionErrorFallback}>
          <Suspense fallback={<SettingsListSkeleton />}>
            <InvocationsList workspaceId={id} />
          </Suspense>
        </ErrorBoundary>
        <section className={styles.root}>
          <SectionHeader
            title={t('page.title')}
            description={t('page.description')}
            help="workspace-settings-ai-metrics"
            actions={<WindowSelector value={windowDays} onChange={handleWindowChange} />}
          />
          <ErrorBoundary FallbackComponent={SectionErrorFallback} resetKeys={[windowDays]}>
            <Suspense fallback={<MetricsSkeleton />}>
              <MetricsDashboard workspaceId={id} windowDays={windowDays} />
            </Suspense>
          </ErrorBoundary>
        </section>
      </div>
    </ErrorBoundary>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/ai/usage')({
  component: AiUsageRoute,
});
