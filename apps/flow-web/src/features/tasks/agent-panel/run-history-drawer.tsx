/**
 * AgentRunHistoryDrawer — side drawer listing recent agent_runs for a
 * task. Hits GET /tasks/{id}/agent-runs through `useTaskAgentRunsQuery`
 * and renders one row per event. Each row decodes the run's payloadJson
 * for confidence, cost_cents, tool_calls, and error fields.
 */

import Drawer from '@nodate-flow/ui/primitives/drawer';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import { type AgentRunEvent, useTaskAgentRunsQuery } from '../api';
import styles from './agent-panel.module.css';

export interface AgentRunHistoryDrawerProps {
  taskId: string;
  locale: string;
  open: boolean;
  onClose: () => void;
}

interface DecodedPayload {
  confidence?: number;
  costCents?: number;
  toolCalls?: string[];
  error?: string;
}

const MAX_THOUGHT_LENGTH = 280;

function decodePayload(payloadJson: string | undefined): DecodedPayload {
  if (!payloadJson) return {};
  try {
    const raw = JSON.parse(payloadJson) as Record<string, unknown>;
    const out: DecodedPayload = {};
    const confidence = raw.confidence;
    if (typeof confidence === 'number') out.confidence = confidence;
    // biome-ignore lint/complexity/useLiteralKeys: payload uses snake_case keys from the orchestrator
    const costSnake = raw['cost_cents'];
    if (typeof costSnake === 'number') out.costCents = costSnake;
    const costCamel = raw.costCents;
    if (typeof costCamel === 'number') out.costCents = costCamel;
    // biome-ignore lint/complexity/useLiteralKeys: payload uses snake_case keys from the orchestrator
    const toolSnake = raw['tool_calls'];
    const toolCamel = raw.toolCalls;
    const toolList = Array.isArray(toolSnake)
      ? toolSnake
      : Array.isArray(toolCamel)
        ? toolCamel
        : null;
    if (toolList) {
      out.toolCalls = toolList
        .map((c: unknown) => (typeof c === 'string' ? c : JSON.stringify(c)))
        .slice(0, 6);
    }
    const errMsg = raw.error;
    if (typeof errMsg === 'string') out.error = errMsg;
    return out;
  } catch {
    return {};
  }
}

function formatRelative(unixSec: number, locale: string): string {
  try {
    const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto', style: 'short' });
    const rawDiff = unixSec - Math.floor(Date.now() / 1000);
    const diff = rawDiff > 0 ? 0 : rawDiff;
    const abs = Math.abs(diff);
    if (abs < 60) return rtf.format(Math.round(diff), 'second');
    if (abs < 3600) return rtf.format(Math.round(diff / 60), 'minute');
    if (abs < 86_400) return rtf.format(Math.round(diff / 3600), 'hour');
    if (abs < 2_592_000) return rtf.format(Math.round(diff / 86_400), 'day');
    return rtf.format(Math.round(diff / 2_592_000), 'month');
  } catch {
    return new Date(unixSec * 1000).toISOString();
  }
}

function truncate(text: string, max: number): string {
  if (text.length <= max) return text;
  return `${text.slice(0, max).trimEnd()}…`;
}

function RunRow({ run, locale }: { run: AgentRunEvent; locale: string }): ReactElement {
  const decoded = decodePayload(run.payloadJson);
  return (
    <li className={styles.runRow}>
      <div className={styles.runHeader}>
        <span className={styles.runType}>{run.type}</span>
        <span className={styles.runTime}>{formatRelative(run.occurredAt, locale)}</span>
      </div>
      <div className={styles.runMeta}>
        {decoded.confidence !== undefined ? (
          <span className={styles.metaItem}>conf {decoded.confidence.toFixed(2)}</span>
        ) : null}
        {decoded.costCents !== undefined ? (
          <span className={styles.metaItem}>${(decoded.costCents / 100).toFixed(2)}</span>
        ) : null}
      </div>
      {decoded.toolCalls && decoded.toolCalls.length > 0 ? (
        <ul className={styles.toolCalls}>
          {decoded.toolCalls.map((call, idx) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: tool-call strings can legitimately repeat within a run, so array position is required to keep sibling keys unique and stable.
            <li key={`${run.eventId}-tc-${idx}-${call.slice(0, 16)}`}>
              {truncate(call, MAX_THOUGHT_LENGTH)}
            </li>
          ))}
        </ul>
      ) : null}
      {decoded.error ? (
        <pre className={styles.runError}>{truncate(decoded.error, MAX_THOUGHT_LENGTH * 2)}</pre>
      ) : null}
    </li>
  );
}

function RunHistoryBody({ taskId, locale }: { taskId: string; locale: string }): ReactElement {
  const { t } = useTranslation('aiAgents');
  const { data: runs } = useTaskAgentRunsQuery(taskId);

  if (runs.length === 0) {
    return <div className={styles.runEmpty}>{t('empty.body')}</div>;
  }

  return (
    <ul className={styles.runList}>
      {runs.map((run) => (
        <RunRow key={run.eventId} run={run} locale={locale} />
      ))}
    </ul>
  );
}

export default function AgentRunHistoryDrawer({
  taskId,
  locale,
  open,
  onClose,
}: AgentRunHistoryDrawerProps): ReactElement {
  const { t } = useTranslation('aiAgents');
  return (
    <Drawer
      open={open}
      onClose={onClose}
      title={t('task_detail.agent.run_history')}
      side="inline-end"
    >
      <Suspense fallback={<Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />}>
        <RunHistoryBody taskId={taskId} locale={locale} />
      </Suspense>
    </Drawer>
  );
}
