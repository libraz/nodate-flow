/**
 * AiCostMeter — minimal topbar widget showing today's AI spend for the
 * currently-active workspace.
 *
 * Renders nothing when:
 *   - no workspace is resolvable from the current route matches, or
 *   - the cost-today query is loading / errored / empty.
 *
 * The query is non-suspense so AI-not-configured tenants degrade silently.
 */

import Icon from '@nodate-flow/ui/icon';
import { useMatches } from '@tanstack/react-router';
import { DollarSign } from 'lucide-react';
import type { ReactElement } from 'react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import { formatCurrency, formatDateOnly } from '../../lib/format';
import { useAiCostTodayQuery } from './cost-api';
import styles from './cost-meter.module.css';

function useActiveWorkspaceId(): string | undefined {
  const matches = useMatches();
  for (let i = matches.length - 1; i >= 0; i -= 1) {
    const params = matches[i]?.params as Record<string, string> | undefined;
    if (!params) continue;
    const id = params.id ?? params.wsId;
    if (typeof id === 'string' && id.length > 0) return id;
  }
  return undefined;
}

function AiCostMeterImpl(): ReactElement | null {
  const { t, i18n } = useTranslation('common');
  const locale = i18n.language;
  const workspaceId = useActiveWorkspaceId();
  const { data, isError, isLoading } = useAiCostTodayQuery(workspaceId);

  if (!workspaceId) return null;
  if (isLoading || isError) return null;
  if (!data) return null;

  const formatted = formatCurrency(data.costUsd, 'USD', locale);
  const formattedDate = formatDateOnly(data.date, locale);
  const label = t('topbar.ai.cost_today.label');
  const title = `${label} — ${formattedDate}`;

  return (
    <span
      className={styles.meter}
      title={title}
      aria-label={`${label}: ${formatted} (${formattedDate})`}
    >
      <Icon icon={DollarSign} decorative className={styles.icon} />
      <span className={styles.value}>{formatted}</span>
    </span>
  );
}

/**
 * AiCostMeter — default export wraps the real implementation in a local
 * ErrorBoundary. The meter is decorative; if anything inside throws
 * synchronously (a sibling hook blows up, a query escalates past the
 * per-query `throwOnError: false` opt-out, etc.) the meter silently
 * disappears instead of collapsing the entire authenticated route to
 * the root FatalFallback.
 */
export default function AiCostMeter(): ReactElement {
  return (
    <ErrorBoundary fallback={null}>
      <AiCostMeterImpl />
    </ErrorBoundary>
  );
}
