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
import { useTranslation } from 'react-i18next';

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

export default function AiCostMeter(): ReactElement | null {
  const { t } = useTranslation('common');
  const workspaceId = useActiveWorkspaceId();
  const { data, isError, isLoading } = useAiCostTodayQuery(workspaceId);

  if (!workspaceId) return null;
  if (isLoading || isError) return null;
  if (!data) return null;

  const formatted = `$${data.costUsd.toFixed(2)}`;
  const title = `${t('topbar.ai.cost_today.label')} — ${data.date}`;

  return (
    <span
      className={styles.meter}
      title={title}
      aria-label={`${t('topbar.ai.cost_today.label')}: ${formatted}`}
    >
      <Icon icon={DollarSign} decorative className={styles.icon} />
      <span className={styles.value}>{formatted}</span>
    </span>
  );
}
