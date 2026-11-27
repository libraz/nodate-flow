/**
 * AgentRunNow — "Run now" for one assigned agent, with the reason inline
 * whenever it cannot run. The reason is visible text under the button, not
 * a tooltip, so keyboard and touch users read it too.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Spinner from '@nodate-flow/ui/primitives/spinner';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, useId } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../../lib/api-error';
import { type Task, type TaskAgentActor, useTaskQuery } from '../api';
import styles from './agent-work.module.css';
import { useRunAgentNow } from './agent-work-api';

type BlockedReason = 'no_permission' | 'paused' | 'no_grants' | 'not_workable' | 'private';

function blockedReasonKey(reason: BlockedReason): string {
  switch (reason) {
    case 'no_permission':
      return 'task_detail.agent.run_now.reasons.no_permission';
    case 'paused':
      return 'task_detail.agent.run_now.reasons.paused';
    case 'no_grants':
      return 'task_detail.agent.run_now.reasons.no_grants';
    case 'not_workable':
      return 'task_detail.agent.run_now.reasons.not_workable';
    case 'private':
      return 'task_detail.agent.run_now.reasons.private';
  }
}

/** First reason the agent cannot run on this task, in the order a person can act on them. */
export function blockedReason(
  actor: Pick<TaskAgentActor, 'paused' | 'hasGrants'>,
  task: Pick<Task, 'visibility' | 'derivedState' | 'archivedAt'>,
  canAct: boolean,
): BlockedReason | null {
  if (!canAct) return 'no_permission';
  if (actor.paused) return 'paused';
  if (!actor.hasGrants) return 'no_grants';
  if (task.visibility !== 'public') return 'private';
  if (
    task.archivedAt !== undefined ||
    task.derivedState === 'done' ||
    task.derivedState === 'cancelled'
  ) {
    return 'not_workable';
  }
  return null;
}

export interface AgentRunNowProps {
  taskId: string;
  actor: TaskAgentActor;
  canAct: boolean;
}

export default function AgentRunNow({ taskId, actor, canAct }: AgentRunNowProps): ReactElement {
  const { t } = useTranslation('aiAgents');
  const { data: task } = useTaskQuery(taskId);
  const run = useRunAgentNow();
  const reasonId = useId();
  const reason = blockedReason(actor, task, canAct);

  const handleRun = async (): Promise<void> => {
    try {
      await run.mutateAsync({ taskId, agentId: actor.agentId });
      toaster.show({ tone: 'success', message: t('task_detail.agent.run_now.requested') });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'task_detail.agent.run_now.failed'),
      });
    }
  };

  return (
    <div className={styles.runNow}>
      <Button
        type="button"
        size="sm"
        variant="default"
        disabled={reason !== null || run.isPending}
        aria-busy={run.isPending}
        aria-describedby={reason ? reasonId : undefined}
        onClick={() => {
          void handleRun();
        }}
      >
        {run.isPending ? (
          <Spinner size="sm" label={t('task_detail.agent.run_now.label')} />
        ) : (
          t('task_detail.agent.run_now.label')
        )}
      </Button>
      {reason ? (
        <p id={reasonId} className={styles.reason}>
          {t(blockedReasonKey(reason))}
        </p>
      ) : null}
    </div>
  );
}

/** One line above the agent rows when the task's visibility keeps agents off it. */
export function PrivateTaskNotice({ taskId }: { taskId: string }): ReactElement | null {
  const { t } = useTranslation('aiAgents');
  const { data: task } = useTaskQuery(taskId);
  if (task.visibility === 'public') return null;
  return <p className={styles.notice}>{t('task_detail.agent.private_task')}</p>;
}
