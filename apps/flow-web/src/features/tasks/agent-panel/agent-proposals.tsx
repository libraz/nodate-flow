/**
 * AgentProposals — what agents asked to do that a person has not yet decided.
 *
 * One compact card per pending proposal: the operation, when it lapses, the
 * change as "before → after", and Approve / Reject. Comment bodies and
 * subtask titles are plain text, never Markdown, because they are the
 * agent's words and are shown before anyone has accepted them. A proposal
 * the viewer cannot act on gets a note instead of buttons; a refusal from
 * the server (stale, expired, decided elsewhere, paused) becomes a toast and
 * the list is refetched by the mutations.
 */

import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, type ReactNode, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../../lib/api-error';
import { formatDate } from '../../../lib/format';
import type { TaskDerivedState, TaskPriority, TransitionName } from '../api';
import { PRIORITY_KEY, STATE_KEY, TRANSITION_KEY } from '../constants';
import styles from './agent-proposals.module.css';
import {
  type AgentProposal,
  useAgentProposalsQuery,
  useApproveAgentProposal,
  useRejectAgentProposal,
} from './agent-work-api';
import { formatAbsoluteTime, formatRelativeTime } from './relative-time';

const MAX_REASON_LENGTH = 500;
/** Text longer than this (or multi-line) gets an expand toggle; shorter text always fits two lines. */
const CLAMP_HINT_LENGTH = 90;

function operationKey(kind: AgentProposal['kind']): string {
  switch (kind) {
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
  }
}

function isPriority(value: number | undefined): value is TaskPriority {
  return value !== undefined && Number.isInteger(value) && value >= 0 && value <= 4;
}

function isState(value: string | undefined): value is TaskDerivedState {
  return value !== undefined && Object.hasOwn(STATE_KEY, value);
}

function isTransition(value: string | undefined): value is TransitionName {
  return value !== undefined && Object.hasOwn(TRANSITION_KEY, value);
}

/** Long agent-written text: two lines, with a disclosure for the rest. */
function ClampedText({ text }: { text: string }): ReactElement {
  const { t } = useTranslation('aiAgents');
  const [expanded, setExpanded] = useState(false);
  const bodyId = useId();
  const expandable = text.length > CLAMP_HINT_LENGTH || text.includes('\n');
  return (
    <div className={styles.textBlock}>
      <p id={bodyId} className={styles.text} data-expanded={expanded || !expandable}>
        {text}
      </p>
      {expandable ? (
        <button
          type="button"
          className={styles.toggle}
          aria-expanded={expanded}
          aria-controls={bodyId}
          onClick={() => {
            setExpanded((v) => !v);
          }}
        >
          {expanded
            ? t('task_detail.agent.proposals.show_less')
            : t('task_detail.agent.proposals.show_more')}
        </button>
      ) : null}
    </div>
  );
}

function ChangeLine({
  before,
  after,
}: {
  before: ReactNode | null;
  after: ReactNode | null;
}): ReactElement {
  const { t } = useTranslation('aiAgents');
  const empty = (
    <span
      className={styles.none}
      role="img"
      aria-label={t('task_detail.agent.proposals.empty_value')}
    >
      —
    </span>
  );
  return (
    <p className={styles.change}>
      <span>{before ?? empty}</span>
      <span
        role="img"
        aria-label={t('task_detail.agent.proposals.changes_to')}
        className={styles.arrow}
      >
        →
      </span>
      <span>{after ?? empty}</span>
    </p>
  );
}

function ProposalChange({
  proposal,
  locale,
}: {
  proposal: AgentProposal;
  locale: string;
}): ReactElement | null {
  const { t: tc } = useTranslation('common');
  const { args, before } = proposal;
  switch (proposal.kind) {
    case 'add_comment':
      return args.body ? <ClampedText text={args.body} /> : null;
    case 'create_subtask':
      return args.title ? <ClampedText text={args.title} /> : null;
    case 'set_priority':
      return (
        <ChangeLine
          before={isPriority(before.priority) ? tc(PRIORITY_KEY[before.priority]) : null}
          after={isPriority(args.priority) ? tc(PRIORITY_KEY[args.priority]) : null}
        />
      );
    case 'set_due_on':
      return (
        <ChangeLine
          before={before.dueOn ? formatDate(before.dueOn, locale) : null}
          after={args.dueOn ? formatDate(args.dueOn, locale) : null}
        />
      );
    case 'transition_task':
      return (
        <ChangeLine
          before={isState(before.derivedState) ? tc(STATE_KEY[before.derivedState]) : null}
          after={isTransition(args.transition) ? tc(TRANSITION_KEY[args.transition]) : null}
        />
      );
  }
}

function ProposalCard({
  proposal,
  locale,
  canDecide,
  busy,
  onApprove,
  onReject,
}: {
  proposal: AgentProposal;
  locale: string;
  canDecide: boolean;
  busy: boolean;
  onApprove: (proposal: AgentProposal) => void;
  onReject: (proposal: AgentProposal) => void;
}): ReactElement {
  const { t } = useTranslation('aiAgents');
  const expired = proposal.status === 'expired';
  const absolute = formatAbsoluteTime(proposal.expiresAt, locale);
  const agentName = proposal.agent.name ?? '';
  return (
    <li id={`agent-proposal-${proposal.id}`} className={styles.card} aria-busy={busy}>
      <div className={styles.cardHeader}>
        <Badge tone="accent">{t(operationKey(proposal.kind))}</Badge>
        {agentName ? <span className={styles.agent}>{agentName}</span> : null}
        <time
          className={styles.expiry}
          dateTime={new Date(proposal.expiresAt * 1000).toISOString()}
          title={absolute}
        >
          {expired
            ? t('task_detail.agent.proposals.expired')
            : t('task_detail.agent.proposals.expires', {
                time: formatRelativeTime(proposal.expiresAt, locale),
              })}
        </time>
      </div>
      <ProposalChange proposal={proposal} locale={locale} />
      {expired ? null : canDecide ? (
        <div className={styles.actions}>
          <Button
            type="button"
            size="sm"
            variant="primary"
            disabled={busy}
            onClick={() => {
              onApprove(proposal);
            }}
          >
            {t('task_detail.agent.proposals.approve')}
          </Button>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            disabled={busy}
            onClick={() => {
              onReject(proposal);
            }}
          >
            {t('task_detail.agent.proposals.reject')}
          </Button>
        </div>
      ) : (
        <p className={styles.note}>{t('task_detail.agent.proposals.cannot_decide')}</p>
      )}
    </li>
  );
}

function RejectDialog({
  proposal,
  pending,
  onCancel,
  onSubmit,
}: {
  proposal: AgentProposal | null;
  pending: boolean;
  onCancel: () => void;
  onSubmit: (reason: string) => void;
}): ReactElement {
  const { t } = useTranslation('aiAgents');
  const [reason, setReason] = useState('');
  const handleSubmit = (e: FormEvent<HTMLFormElement>): void => {
    e.preventDefault();
    onSubmit(reason.trim());
  };
  return (
    <Dialog
      open={proposal !== null}
      onClose={onCancel}
      title={t('task_detail.agent.proposals.reject_title')}
      size="sm"
    >
      <form className={styles.rejectForm} onSubmit={handleSubmit}>
        <FormField
          label={t('task_detail.agent.proposals.reject_reason')}
          description={t('task_detail.agent.proposals.reject_reason_help')}
        >
          {(control) => (
            <Textarea
              {...control}
              value={reason}
              maxLength={MAX_REASON_LENGTH}
              rows={3}
              onChange={(e) => {
                setReason(e.target.value);
              }}
            />
          )}
        </FormField>
        <div className={styles.dialogActions}>
          <Button type="button" variant="ghost" onClick={onCancel}>
            {t('task_detail.agent.proposals.reject_cancel')}
          </Button>
          <Button type="submit" variant="primary" disabled={pending} aria-busy={pending}>
            {t('task_detail.agent.proposals.reject_confirm')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

export interface AgentProposalsProps {
  taskId: string;
  locale: string;
  /** False for viewers who cannot perform agent operations; they get a note instead of buttons. */
  canDecide: boolean;
}

export default function AgentProposals({
  taskId,
  locale,
  canDecide,
}: AgentProposalsProps): ReactElement | null {
  const { t } = useTranslation('aiAgents');
  const { data: proposals } = useAgentProposalsQuery(taskId);
  const approve = useApproveAgentProposal();
  const reject = useRejectAgentProposal();
  const [rejecting, setRejecting] = useState<AgentProposal | null>(null);

  const busyId = approve.isPending
    ? approve.variables?.proposalId
    : reject.isPending
      ? reject.variables?.proposalId
      : undefined;

  const handleApprove = async (proposal: AgentProposal): Promise<void> => {
    try {
      await approve.mutateAsync({ taskId, proposalId: proposal.id });
      toaster.show({ tone: 'success', message: t('task_detail.agent.proposals.approved') });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'task_detail.agent.proposals.decide_failed'),
      });
    }
  };

  const handleReject = async (reason: string): Promise<void> => {
    if (!rejecting) return;
    const proposal = rejecting;
    try {
      await reject.mutateAsync({
        taskId,
        proposalId: proposal.id,
        ...(reason ? { reason } : {}),
      });
      toaster.show({ tone: 'success', message: t('task_detail.agent.proposals.rejected') });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'task_detail.agent.proposals.decide_failed'),
      });
    } finally {
      setRejecting(null);
    }
  };

  if (proposals.length === 0) return null;

  return (
    <section className={styles.root} aria-label={t('task_detail.agent.proposals.title')}>
      <h4 className={styles.heading}>
        {t('task_detail.agent.proposals.title')}
        <span className={styles.count}>{proposals.length}</span>
      </h4>
      <ul className={styles.list}>
        {proposals.map((proposal) => (
          <ProposalCard
            key={proposal.id}
            proposal={proposal}
            locale={locale}
            canDecide={canDecide}
            busy={busyId === proposal.id}
            onApprove={(p) => {
              void handleApprove(p);
            }}
            onReject={setRejecting}
          />
        ))}
      </ul>
      <RejectDialog
        key={rejecting?.id ?? 'none'}
        proposal={rejecting}
        pending={reject.isPending}
        onCancel={() => {
          setRejecting(null);
        }}
        onSubmit={(reason) => {
          void handleReject(reason);
        }}
      />
    </section>
  );
}
