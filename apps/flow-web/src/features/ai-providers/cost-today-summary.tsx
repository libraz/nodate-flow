/**
 * CostTodaySummary — today's AI spend for a workspace, shown at the top of
 * the AI activity settings page to workspace owners and admins only.
 *
 * The query is non-suspense and silent on error, so AI-not-configured
 * workspaces render nothing instead of an error state.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import type { ReactElement } from 'react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import { formatCurrency, formatDateOnly } from '../../lib/format';
import { isWorkspaceAdmin, useCurrentWorkspaceRole } from '../../lib/use-current-workspace-role';
import { useAiCostTodayQuery } from './cost-api';
import styles from './cost-today-summary.module.css';

type CostTodaySummaryProps = {
  workspaceId: string;
};

function CostTodaySummaryImpl({ workspaceId }: CostTodaySummaryProps): ReactElement | null {
  const { t, i18n } = useTranslation('ai');
  const locale = i18n.language;
  const isAdmin = isWorkspaceAdmin(useCurrentWorkspaceRole());
  const { data, isError, isLoading } = useAiCostTodayQuery(isAdmin ? workspaceId : undefined);

  if (!isAdmin) return null;
  if (isLoading) return <Skeleton className={styles.skeleton} />;
  if (isError || !data) return null;

  const label = t('ai:cost_today.tooltip');

  return (
    <section aria-label={label} className={styles.summary}>
      <div className={styles.meta}>
        <span className={styles.label}>{label}</span>
        <span className={styles.date}>{formatDateOnly(data.date, locale)}</span>
      </div>
      <span className={styles.amount}>{formatCurrency(data.costUsd, 'USD', locale)}</span>
    </section>
  );
}

/** Wraps the implementation in a local boundary so a failure never takes down the page. */
export default function CostTodaySummary(props: CostTodaySummaryProps): ReactElement {
  return (
    <ErrorBoundary fallback={null}>
      <CostTodaySummaryImpl {...props} />
    </ErrorBoundary>
  );
}
