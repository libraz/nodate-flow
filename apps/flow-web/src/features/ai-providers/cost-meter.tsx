/**
 * AiCostMeter — minimal topbar widget showing today's AI spend for the
 * currently-active workspace.
 *
 * Renders nothing when:
 *   - no workspace is resolvable from the current route matches, or
 *   - the cost-today query is loading / errored / empty.
 *
 * The query is non-suspense so AI-not-configured tenants degrade silently.
 *
 * Presentation: renders as a small "AI · $0.00" chip that links to the
 * workspace's AI activity settings when an active workspace id is
 * available, so the meter doubles as an affordance for drilling into
 * the full spend breakdown. Falls back to a plain `<span>` when no
 * active workspace id is resolvable (e.g. transient routing state).
 */

import { Link } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import { formatCurrency, formatDateOnly } from '../../lib/format';
import { useActiveWorkspaceId } from '../../lib/use-current-workspace';
import { useAiCostTodayQuery } from './cost-api';
import styles from './cost-meter.module.css';

function AiCostMeterImpl(): ReactElement | null {
  const { t, i18n } = useTranslation(['ai', 'common']);
  const locale = i18n.language;
  const workspaceId = useActiveWorkspaceId();
  const { data, isError, isLoading } = useAiCostTodayQuery(workspaceId ?? undefined);

  if (!workspaceId) return null;
  if (isLoading || isError) return null;
  if (!data) return null;

  const formatted = formatCurrency(data.costUsd, 'USD', locale);
  const formattedDate = formatDateOnly(data.date, locale);
  const label = t('ai:cost_today.label');
  const tooltip = t('ai:cost_today.tooltip');
  const title = `${tooltip} — ${formattedDate}`;
  const ariaLabel = `${label}: ${formatted} (${formattedDate})`;

  const chipContent = (
    <>
      <span className={styles.label}>{label}</span>
      <span className={styles.separator} aria-hidden="true">
        ·
      </span>
      <span className={styles.value}>{formatted}</span>
    </>
  );

  return (
    <Link
      to="/workspaces/$id/settings/ai-activity"
      params={{ id: workspaceId }}
      className={styles.meter}
      title={title}
      aria-label={ariaLabel}
    >
      {chipContent}
    </Link>
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
