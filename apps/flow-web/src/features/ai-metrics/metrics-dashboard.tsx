/**
 * MetricsDashboard — body of the workspace AI metrics page.
 *
 * Pulls the metrics for `(workspaceId, windowDays)` via the suspense
 * query and lays out:
 *
 * - three headline KPI cards (Proposed / Applied / Dismissed)
 * - one acceptance-rate card with a thin progress bar
 * - the per-provider outbound rate-limit table
 *
 * The query suspends, so this component is meant to live inside a
 * `<Suspense>` boundary that renders {@link MetricsSkeleton}.
 */

import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import AcceptanceCard from './acceptance-card';
import styles from './ai-metrics.module.css';
import { type AiMetricsWindow, useAiMetricsQuery } from './api';
import KpiCard from './kpi-card';
import OutboundTable from './outbound-table';

export interface MetricsDashboardProps {
  workspaceId: string;
  windowDays: AiMetricsWindow;
}

export default function MetricsDashboard({
  workspaceId,
  windowDays,
}: MetricsDashboardProps): ReactElement {
  const { t } = useTranslation('aiMetrics');
  const { data } = useAiMetricsQuery(workspaceId, windowDays);
  const outboundRows = data.outboundLimits ?? [];

  return (
    <>
      <div className={styles.kpiGrid}>
        <KpiCard label={t('kpi.proposed')} value={data.proposed} />
        <KpiCard label={t('kpi.applied')} value={data.applied} />
        <KpiCard label={t('kpi.dismissed')} value={data.dismissed} />
      </div>
      <AcceptanceCard
        rate={data.acceptanceRate}
        applied={data.applied}
        dismissed={data.dismissed}
      />
      <OutboundTable rows={outboundRows} />
    </>
  );
}
