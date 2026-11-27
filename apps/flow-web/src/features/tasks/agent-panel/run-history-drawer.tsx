/**
 * AgentRunHistoryDrawer — side drawer listing an agent's recent runs on a
 * task, newest first.
 *
 * Runs that carry a `runKey` are one disclosure each: the steps (tool
 * calls and outcomes) load on first open, and the run's end reason is its
 * summary line. Runs from before run keys existed have no step log, so
 * their start and end events are paired into one static row.
 *
 * Everything an agent wrote (thought, argument summary) is plain text.
 */

import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Drawer from '@nodate-flow/ui/primitives/drawer';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { Link } from '@tanstack/react-router';
import { ChevronDown } from 'lucide-react';
import { type ReactElement, Suspense, useId, useState } from 'react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import { type AgentRunEvent, useTaskAgentRunsQuery } from '../api';
import styles from './agent-panel.module.css';
import { type AgentStep, useAgentRunStepsQuery } from './agent-work-api';
import { formatAbsoluteTime, formatRelativeTime } from './relative-time';

export interface AgentRunHistoryDrawerProps {
  taskId: string;
  locale: string;
  open: boolean;
  onClose: () => void;
}

const RUN_STARTED = 'ai.agent.run.started';
const RUN_COMPLETED = 'ai.agent.run.completed';
const RUN_FAILED = 'ai.agent.run.failed';

interface DecodedPayload {
  confidence?: number;
  costCents?: number;
  toolCalls?: string[];
  error?: string;
  runKey?: string;
  endedReason?: string;
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
    if (typeof raw.error === 'string') out.error = raw.error;
    if (typeof raw.runKey === 'string' && raw.runKey) out.runKey = raw.runKey;
    if (typeof raw.endedReason === 'string' && raw.endedReason) out.endedReason = raw.endedReason;
    return out;
  } catch {
    return {};
  }
}

function truncate(text: string, max: number): string {
  if (text.length <= max) return text;
  return `${text.slice(0, max).trimEnd()}…`;
}

/** One run: its start and end events, joined by run key or (for old rows) by adjacency. */
interface RunGroup {
  id: string;
  runKey?: string;
  started?: AgentRunEvent;
  ended?: AgentRunEvent;
  latest: number;
}

function isTerminal(type: string): boolean {
  return type === RUN_COMPLETED || type === RUN_FAILED;
}

/** `runs` arrives newest first; the result is newest first too. */
export function groupRuns(runs: readonly AgentRunEvent[]): RunGroup[] {
  const keyed = new Map<string, RunGroup>();
  const legacy: AgentRunEvent[] = [];
  for (const ev of runs) {
    const runKey = decodePayload(ev.payloadJson).runKey;
    if (!runKey) {
      legacy.push(ev);
      continue;
    }
    const group = keyed.get(runKey) ?? { id: runKey, runKey, latest: 0 };
    if (ev.type === RUN_STARTED) group.started = ev;
    else if (isTerminal(ev.type)) group.ended = ev;
    group.latest = Math.max(group.latest, ev.occurredAt);
    keyed.set(runKey, group);
  }

  const groups: RunGroup[] = [...keyed.values()];
  const open = new Map<string, RunGroup>();
  for (const ev of legacy.reverse()) {
    if (ev.type === RUN_STARTED) {
      const group: RunGroup = { id: ev.eventId, started: ev, latest: ev.occurredAt };
      open.set(ev.agent.id, group);
      groups.push(group);
      continue;
    }
    const waiting = isTerminal(ev.type) ? open.get(ev.agent.id) : undefined;
    if (waiting) {
      waiting.ended = ev;
      waiting.latest = ev.occurredAt;
      open.delete(ev.agent.id);
    } else {
      groups.push({ id: ev.eventId, ended: ev, latest: ev.occurredAt });
    }
  }
  return groups.sort((a, b) => b.latest - a.latest);
}

function endedReasonKey(reason: string): string | null {
  switch (reason) {
    case 'done':
      return 'task_detail.agent.history.ended.done';
    case 'max_steps':
      return 'task_detail.agent.history.ended.max_steps';
    case 'write_cap':
      return 'task_detail.agent.history.ended.write_cap';
    case 'repeat':
      return 'task_detail.agent.history.ended.repeat';
    case 'timeout':
      return 'task_detail.agent.history.ended.timeout';
    case 'budget':
      return 'task_detail.agent.history.ended.budget';
    case 'invalid_output':
      return 'task_detail.agent.history.ended.invalid_output';
    case 'tool_error':
      return 'task_detail.agent.history.ended.tool_error';
    case 'stopped':
      return 'task_detail.agent.history.ended.stopped';
    case 'coalesced':
      return 'task_detail.agent.history.ended.coalesced';
    case 'rate_limited':
      return 'task_detail.agent.history.ended.rate_limited';
    default:
      return null;
  }
}

function outcomeKey(outcome: AgentStep['outcome']): string {
  switch (outcome) {
    case 'applied':
      return 'task_detail.agent.history.outcome.applied';
    case 'proposed':
      return 'task_detail.agent.history.outcome.proposed';
    case 'noop':
      return 'task_detail.agent.history.outcome.noop';
    case 'skipped':
      return 'task_detail.agent.history.outcome.skipped';
    case 'not_permitted':
      return 'task_detail.agent.history.outcome.not_permitted';
    case 'invalid':
      return 'task_detail.agent.history.outcome.invalid';
    case 'failed':
      return 'task_detail.agent.history.outcome.failed';
    case 'stopped':
      return 'task_detail.agent.history.outcome.stopped';
  }
}

function outcomeTone(outcome: AgentStep['outcome']): BadgeTone {
  switch (outcome) {
    case 'applied':
      return 'success';
    case 'proposed':
      return 'accent';
    case 'failed':
    case 'invalid':
      return 'danger';
    case 'not_permitted':
    case 'stopped':
      return 'warning';
    case 'noop':
    case 'skipped':
      return 'neutral';
  }
}

/** Operation names the agent can call; an unknown tool shows as sent. */
function toolKey(tool: string): string | null {
  switch (tool) {
    case 'add_comment':
      return 'task_detail.agent.proposals.ops.add_comment';
    case 'set_priority':
      return 'task_detail.agent.proposals.ops.set_priority';
    case 'set_due_on':
      return 'task_detail.agent.proposals.ops.set_due_on';
    case 'transition_task':
      return 'task_detail.agent.proposals.ops.transition_task';
    case 'create_subtask':
      return 'task_detail.agent.proposals.ops.create_subtask';
    case 'finish':
      return 'task_detail.agent.history.tool_finish';
    default:
      return null;
  }
}

function RunSteps({
  taskId,
  runKey,
  onNavigate,
}: {
  taskId: string;
  runKey: string;
  onNavigate: () => void;
}): ReactElement {
  const { t } = useTranslation('aiAgents');
  const { data: steps } = useAgentRunStepsQuery(taskId, runKey);
  if (steps.length === 0) {
    return <p className={styles.stepsEmpty}>{t('task_detail.agent.history.no_steps')}</p>;
  }
  return (
    <ol className={styles.steps}>
      {steps.map((step) => {
        const key = toolKey(step.tool);
        return (
          <li key={step.step} className={styles.step}>
            <div className={styles.stepLine}>
              <span className={styles.stepNumber}>{step.step}</span>
              <span className={styles.stepTool}>{key ? t(key) : step.tool}</span>
              <Badge tone={outcomeTone(step.outcome)}>{t(outcomeKey(step.outcome))}</Badge>
              {step.proposalId ? (
                <Link
                  to="."
                  hash={`agent-proposal-${step.proposalId}`}
                  className={styles.stepLink}
                  onClick={onNavigate}
                >
                  {t('task_detail.agent.history.view_proposal')}
                </Link>
              ) : null}
            </div>
            {step.thought ? <p className={styles.stepText}>{step.thought}</p> : null}
            {step.argsSummary ? <p className={styles.stepMeta}>{step.argsSummary}</p> : null}
          </li>
        );
      })}
    </ol>
  );
}

function runStatus(group: RunGroup): { key: string; tone: BadgeTone } {
  if (!group.ended) return { key: 'task_detail.agent.history.status.running', tone: 'info' };
  return group.ended.type === RUN_FAILED
    ? { key: 'task_detail.agent.history.status.failed', tone: 'danger' }
    : { key: 'task_detail.agent.history.status.completed', tone: 'success' };
}

function RunItem({
  group,
  taskId,
  locale,
  onNavigate,
}: {
  group: RunGroup;
  taskId: string;
  locale: string;
  onNavigate: () => void;
}): ReactElement {
  const { t } = useTranslation('aiAgents');
  const [open, setOpen] = useState(false);
  const stepsId = useId();
  const anchor = group.started ?? group.ended;
  const decoded = decodePayload((group.ended ?? group.started)?.payloadJson);
  const status = runStatus(group);
  const reasonKey = decoded.endedReason ? endedReasonKey(decoded.endedReason) : null;
  const summary = reasonKey ? t(reasonKey) : null;

  const header = (
    <>
      <span className={styles.runTitle}>
        <Badge tone={status.tone}>{t(status.key)}</Badge>
        {anchor ? (
          <time
            className={styles.runTime}
            dateTime={new Date(anchor.occurredAt * 1000).toISOString()}
            title={formatAbsoluteTime(anchor.occurredAt, locale)}
          >
            {formatRelativeTime(anchor.occurredAt, locale)}
          </time>
        ) : null}
      </span>
      {summary ? <span className={styles.runSummary}>{summary}</span> : null}
    </>
  );

  if (!group.runKey) {
    return (
      <li className={styles.runRow}>
        <div className={styles.runHeaderStatic}>{header}</div>
        <div className={styles.runMeta}>
          {decoded.confidence !== undefined ? (
            <span className={styles.metaItem}>
              {t('task_detail.agent.history.confidence', { value: decoded.confidence.toFixed(2) })}
            </span>
          ) : null}
          {decoded.costCents !== undefined ? (
            <span className={styles.metaItem}>${(decoded.costCents / 100).toFixed(2)}</span>
          ) : null}
        </div>
        {decoded.toolCalls && decoded.toolCalls.length > 0 ? (
          <ul className={styles.toolCalls}>
            {decoded.toolCalls.map((call, idx) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: tool-call strings can legitimately repeat within a run, so array position is required to keep sibling keys unique and stable.
              <li key={`${group.id}-tc-${idx}-${call.slice(0, 16)}`}>
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

  return (
    <li className={styles.runRow}>
      <button
        type="button"
        className={styles.runHeader}
        aria-expanded={open}
        aria-controls={stepsId}
        onClick={() => {
          setOpen((v) => !v);
        }}
      >
        <ChevronDown size={14} aria-hidden className={styles.chevron} data-open={open} />
        <span className={styles.runHeaderBody}>{header}</span>
      </button>
      {decoded.error ? (
        <pre className={styles.runError}>{truncate(decoded.error, MAX_THOUGHT_LENGTH * 2)}</pre>
      ) : null}
      {open ? (
        <div id={stepsId}>
          <ErrorBoundary
            fallback={
              <p className={styles.stepsEmpty}>{t('task_detail.agent.history.steps_failed')}</p>
            }
          >
            <Suspense
              fallback={
                // nf-token-override: placeholder sized to the content it stands in for, not a spacing step
                <Skeleton style={{ blockSize: '2rem', inlineSize: '100%' }} />
              }
            >
              <RunSteps taskId={taskId} runKey={group.runKey} onNavigate={onNavigate} />
            </Suspense>
          </ErrorBoundary>
        </div>
      ) : null}
    </li>
  );
}

function RunHistoryBody({
  taskId,
  locale,
  onNavigate,
}: {
  taskId: string;
  locale: string;
  onNavigate: () => void;
}): ReactElement {
  const { t } = useTranslation('aiAgents');
  const { data: runs } = useTaskAgentRunsQuery(taskId);
  const groups = groupRuns(runs);

  if (groups.length === 0) {
    return <div className={styles.runEmpty}>{t('empty.body')}</div>;
  }

  return (
    <ul className={styles.runList}>
      {groups.map((group) => (
        <RunItem
          key={group.id}
          group={group}
          taskId={taskId}
          locale={locale}
          onNavigate={onNavigate}
        />
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
      {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
      <Suspense fallback={<Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />}>
        <RunHistoryBody taskId={taskId} locale={locale} onNavigate={onClose} />
      </Suspense>
    </Drawer>
  );
}
