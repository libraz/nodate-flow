/**
 * AgentPanel — task-detail panel rendered when an AI agent is assigned to
 * the task (`task.agentContext?.agent != null`).
 *
 * Surfaces the agent identity, current handoff status (running / handed
 * back / stuck), last thought preview, attempts counter, today's cost,
 * and two actions: hand the task back to the user, and open a side
 * drawer listing recent agent_runs.
 *
 * The panel uses a fixed min-block-size so the status pill fading between
 * states does not reflow the surrounding task detail layout.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Spinner from '@nodate-flow/ui/primitives/spinner';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Bot, History } from 'lucide-react';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import {
  type AgentHandoffStatus,
  deriveAgentStatus,
  type TaskAgentContext,
  useHandoffToUser,
} from '../api';
import styles from './agent-panel.module.css';
import AgentRunHistoryDrawer from './run-history-drawer';

export interface AgentPanelProps {
  taskId: string;
  agentContext: TaskAgentContext;
  locale: string;
}

function statusTone(status: AgentHandoffStatus): 'success' | 'neutral' | 'warning' {
  switch (status) {
    case 'running':
      return 'success';
    case 'handed_back':
      return 'neutral';
    case 'stuck':
      return 'warning';
  }
}

function statusKey(status: AgentHandoffStatus): string {
  switch (status) {
    case 'running':
      return 'task_detail.agent.status.running';
    case 'handed_back':
      return 'task_detail.agent.status.handed_back';
    case 'stuck':
      return 'task_detail.agent.status.stuck';
  }
}

function handoffReasonKey(reason: string | undefined): string | null {
  switch (reason) {
    case 'low_confidence':
      return 'handoff_reason.low_confidence';
    case 'cost_cap':
      return 'handoff_reason.cost_cap';
    case 'tool_error':
      return 'handoff_reason.tool_error';
    case 'constraint_conflict':
      return 'handoff_reason.constraint_conflict';
    case 'manual':
      return 'handoff_reason.manual';
    default:
      return null;
  }
}

function formatRelativeAgo(unixSec: number, locale: string): string {
  try {
    const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto', style: 'short' });
    const rawDiff = unixSec - Math.floor(Date.now() / 1000);
    const diffSec = rawDiff > 0 ? 0 : rawDiff;
    const abs = Math.abs(diffSec);
    if (abs < 60) return rtf.format(Math.round(diffSec), 'second');
    if (abs < 3600) return rtf.format(Math.round(diffSec / 60), 'minute');
    if (abs < 86_400) return rtf.format(Math.round(diffSec / 3600), 'hour');
    if (abs < 2_592_000) return rtf.format(Math.round(diffSec / 86_400), 'day');
    if (abs < 31_536_000) return rtf.format(Math.round(diffSec / 2_592_000), 'month');
    return rtf.format(Math.round(diffSec / 31_536_000), 'year');
  } catch {
    return new Date(unixSec * 1000).toISOString();
  }
}

export default function AgentPanel({
  taskId,
  agentContext,
  locale,
}: AgentPanelProps): ReactElement | null {
  const { t } = useTranslation('aiAgents');
  const agent = agentContext.agent;
  if (!agent) return null;

  const status = deriveAgentStatus(agentContext);
  const tone = statusTone(status);
  const statusLabel = t(statusKey(status));
  const reasonKey = handoffReasonKey(agentContext.handoffReason);

  const agentName = agent.name?.trim() || t('task_detail.agent.panel_title');
  const [historyOpen, setHistoryOpen] = useState(false);
  const handoffToUser = useHandoffToUser();

  const handleHandBack = (): void => {
    handoffToUser.mutate(
      { taskId, input: { reason: 'manual' } },
      {
        onError: (err) => {
          toaster.show({ tone: 'danger', message: err.message ?? t('error.fetchFailed') });
        },
      },
    );
  };

  const handleOpenHistory = (): void => {
    setHistoryOpen(true);
  };

  const handleCloseHistory = (): void => {
    setHistoryOpen(false);
  };

  return (
    <section
      className={styles.panel}
      aria-label={t('task_detail.agent.panel_title')}
      data-status={status}
    >
      <div className={styles.header}>
        <div className={styles.avatar} aria-hidden="true">
          <Bot size={20} strokeWidth={1.75} />
        </div>
        <div className={styles.identity}>
          <span className={styles.agentName}>{agentName}</span>
          <span className={styles.statusPill} data-tone={tone} aria-live="polite">
            <span
              className={styles.statusGlyph}
              data-pulse={status === 'running' ? 'true' : undefined}
              aria-hidden="true"
            />
            {statusLabel}
          </span>
        </div>
      </div>

      {reasonKey ? <span className={styles.reason}>{t(reasonKey)}</span> : null}

      {agentContext.lastThought ? (
        <p className={styles.thought}>{agentContext.lastThought}</p>
      ) : null}

      <div className={styles.meta}>
        {agentContext.lastRunAt ? (
          <span className={styles.metaItem}>
            {t('task_detail.agent.last_run_ago', {
              time: formatRelativeAgo(agentContext.lastRunAt, locale),
            })}
          </span>
        ) : null}
        <span className={styles.metaItem}>
          {t('task_detail.agent.attempts', { current: agentContext.attempts, max: '—' })}
        </span>
      </div>

      <div className={styles.actions}>
        <Button
          type="button"
          variant="default"
          size="sm"
          onClick={handleHandBack}
          disabled={handoffToUser.isPending}
        >
          {handoffToUser.isPending ? (
            <Spinner size="sm" label={t('task_detail.agent.hand_back')} />
          ) : (
            t('task_detail.agent.hand_back')
          )}
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={handleOpenHistory}>
          <History size={14} strokeWidth={1.75} aria-hidden="true" />
          {t('task_detail.agent.run_history')}
        </Button>
      </div>

      {historyOpen ? (
        <AgentRunHistoryDrawer
          taskId={taskId}
          locale={locale}
          open={historyOpen}
          onClose={handleCloseHistory}
        />
      ) : null}
    </section>
  );
}
