/**
 * AgentAssignSection — assign and unassign AI agents on a task.
 *
 * The agent panel next door only appears once an agent is already
 * attached, and the only wired mutation was removal, so from the web UI
 * agents could be taken off a task but never put on one. That makes the
 * product's central idea — the model as part of how work runs, not as a
 * side feature — reachable through the API alone.
 *
 * Assignment uses the `assignee` role: attaching an agent means handing
 * it the task, which is the same thing the human picker above expresses.
 * Removal goes through the shared actor endpoint, since agent rows are
 * actor rows and `TaskAgentActor.id` is that row's id.
 *
 * A workspace with no agents gets a link to the agent settings instead of
 * an add button; the button only shows while an unassigned agent exists.
 */

import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Combobox from '@nodate-flow/ui/primitives/combobox';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { Link } from '@tanstack/react-router';
import { History, Plus } from 'lucide-react';
import { type ReactElement, Suspense, useState } from 'react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../../lib/api-error';
import { useAgentsQuery } from '../../ai-providers/agents-api';
import {
  type TaskAgentActor,
  useAddTaskAgentActor,
  useRemoveTaskActor,
  useTaskAgentActorsQuery,
} from '../api';
import TaskActionButton from '../task-action-button';
import css from './agent-assign-section.module.css';
import AgentProposals from './agent-proposals';
import AgentRunNow, { PrivateTaskNotice } from './agent-run-now';
import work from './agent-work.module.css';
import { useCanActOnAgentWork } from './agent-work-api';
import AgentRunHistoryDrawer from './run-history-drawer';

export interface AgentAssignSectionProps {
  taskId: string;
  workspaceId: string;
}

export default function AgentAssignSection({
  taskId,
  workspaceId,
}: AgentAssignSectionProps): ReactElement {
  const { t, i18n } = useTranslation('aiAgents');
  const locale = i18n?.resolvedLanguage ?? 'en';
  const canAct = useCanActOnAgentWork(workspaceId);
  const [historyOpen, setHistoryOpen] = useState(false);
  const { data: assigned } = useTaskAgentActorsQuery(taskId);
  const { data: agentList } = useAgentsQuery(workspaceId);
  const addAgent = useAddTaskAgentActor();
  const removeActor = useRemoveTaskActor();
  const [picking, setPicking] = useState(false);
  const [removingId, setRemovingId] = useState<string | null>(null);
  const { entries, onExited } = usePresenceList(assigned, (actor) => actor.id);

  const assignedAgentIds = new Set(assigned.map((a) => a.agentId));
  const available = agentList.agents.filter((a) => !assignedAgentIds.has(a.id));
  const noAgentsYet = agentList.agents.length === 0 && entries.length === 0;

  const handleAssign = async (agentId: string): Promise<void> => {
    if (!agentId) return;
    try {
      await addAgent.mutateAsync({ taskId, input: { agentId, role: 'assignee' } });
      setPicking(false);
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'task_detail.agent.assign.failed'),
      });
    }
  };

  const handleRemove = async (actor: TaskAgentActor): Promise<void> => {
    setRemovingId(actor.id);
    try {
      await removeActor.mutateAsync({ taskId, actorId: actor.id });
      toaster.show({
        tone: 'info',
        message: t('task_detail.agent.assign.removed', { name: actor.agentName }),
        action: {
          label: t('common:common.undo'),
          onAction: () => {
            addAgent
              .mutateAsync({ taskId, input: { agentId: actor.agentId, role: 'assignee' } })
              .catch((err: unknown) => {
                toaster.show({
                  tone: 'danger',
                  message: formatApiError(err, t, 'task_detail.agent.assign.failed'),
                });
              });
          },
        },
      });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'task_detail.agent.assign.remove_failed'),
      });
    } finally {
      setRemovingId(null);
    }
  };

  return (
    <>
      {noAgentsYet ? (
        <p className={css.muted}>
          {t('task_detail.agent.assign.none_available')}{' '}
          <Link
            to="/workspaces/$id/settings/ai/agents"
            params={{ id: workspaceId }}
            className={css.link}
          >
            {t('task_detail.agent.assign.create_link')}
          </Link>
        </p>
      ) : entries.length === 0 ? (
        <p className={css.muted}>{t('task_detail.agent.assign.empty')}</p>
      ) : (
        <ul className={css.list}>
          {entries.map(({ key, item: actor, present }) => (
            <PresenceItem
              key={key}
              as="li"
              present={present}
              onExited={() => onExited(key)}
              className={motion.enter}
            >
              <div className={work.agent}>
                <div className={css.item}>
                  <span className={work.identity}>
                    <span>{actor.agentName}</span>
                    {actor.paused ? (
                      <Badge tone="warning">{t('task_detail.agent.run_now.paused_badge')}</Badge>
                    ) : null}
                  </span>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    disabled={removingId === actor.id}
                    aria-busy={removingId === actor.id}
                    aria-label={t('task_detail.agent.assign.remove', { name: actor.agentName })}
                    onClick={() => {
                      void handleRemove(actor);
                    }}
                  >
                    ×
                  </Button>
                </div>
                {actor.kind === 'signal_judge' ? null : (
                  <ErrorBoundary fallback={null}>
                    <Suspense fallback={null}>
                      <AgentRunNow taskId={taskId} actor={actor} canAct={canAct} />
                    </Suspense>
                  </ErrorBoundary>
                )}
              </div>
            </PresenceItem>
          ))}
        </ul>
      )}
      {available.length === 0 ? null : picking ? (
        <Combobox
          aria-label={t('task_detail.agent.assign.add')}
          placeholder={t('task_detail.agent.assign.add')}
          options={available.map((agent) => ({
            value: agent.id,
            // A paused agent can still be attached — the assignment is
            // what the runtime reads when it resumes — but saying so up
            // front avoids "I assigned it and nothing happened".
            label: agent.paused
              ? t('task_detail.agent.assign.paused_option', { name: agent.name })
              : agent.name,
          }))}
          onChange={(v) => {
            void handleAssign(v);
          }}
        />
      ) : (
        <TaskActionButton
          icon={<Plus size={14} aria-hidden />}
          className={css.startAligned}
          disabled={addAgent.isPending}
          aria-busy={addAgent.isPending}
          onClick={() => {
            setPicking(true);
          }}
        >
          {t('task_detail.agent.assign.add')}
        </TaskActionButton>
      )}
      <div className={work.work}>
        {entries.length === 0 ? null : (
          <ErrorBoundary fallback={null}>
            <Suspense fallback={null}>
              <PrivateTaskNotice taskId={taskId} />
            </Suspense>
          </ErrorBoundary>
        )}
        <ErrorBoundary fallback={null}>
          <Suspense fallback={null}>
            <AgentProposals taskId={taskId} locale={locale} canDecide={canAct} />
          </Suspense>
        </ErrorBoundary>
        {entries.length === 0 ? null : (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className={work.historyButton}
            onClick={() => {
              setHistoryOpen(true);
            }}
          >
            <History size={14} strokeWidth={1.75} aria-hidden="true" />
            {t('task_detail.agent.run_history')}
          </Button>
        )}
      </div>
      {historyOpen ? (
        <AgentRunHistoryDrawer
          taskId={taskId}
          locale={locale}
          open={historyOpen}
          onClose={() => {
            setHistoryOpen(false);
          }}
        />
      ) : null}
    </>
  );
}
