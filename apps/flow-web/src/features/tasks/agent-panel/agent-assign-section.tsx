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
 */

import Button from '@nodate-flow/ui/primitives/button';
import Combobox from '@nodate-flow/ui/primitives/combobox';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../../lib/api-error';
import { useAgentsQuery } from '../../ai-providers/agents-api';
import { useAddTaskAgentActor, useRemoveTaskActor, useTaskAgentActorsQuery } from '../api';

export interface AgentAssignSectionProps {
  taskId: string;
  workspaceId: string;
}

export default function AgentAssignSection({
  taskId,
  workspaceId,
}: AgentAssignSectionProps): ReactElement {
  const { t } = useTranslation('aiAgents');
  const { data: assigned } = useTaskAgentActorsQuery(taskId);
  const { data: agentList } = useAgentsQuery(workspaceId);
  const addAgent = useAddTaskAgentActor();
  const removeActor = useRemoveTaskActor();
  const [picking, setPicking] = useState(false);

  const assignedAgentIds = new Set(assigned.map((a) => a.agentId));
  const available = agentList.agents.filter((a) => !assignedAgentIds.has(a.id));

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

  const handleRemove = async (actorId: string): Promise<void> => {
    try {
      await removeActor.mutateAsync({ taskId, actorId });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'task_detail.agent.assign.remove_failed'),
      });
    }
  };

  return (
    <>
      {assigned.length === 0 ? (
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>
          {t('task_detail.agent.assign.empty')}
        </p>
      ) : (
        <ul
          style={{
            listStyle: 'none',
            padding: 0,
            margin: 0,
            display: 'flex',
            flexDirection: 'column',
            gap: 'var(--nf-space-1)',
          }}
        >
          {assigned.map((actor) => (
            <li
              key={actor.id}
              style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}
            >
              <span>{actor.agentName}</span>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                disabled={removeActor.isPending}
                aria-label={t('task_detail.agent.assign.remove', { name: actor.agentName })}
                onClick={() => {
                  void handleRemove(actor.id);
                }}
              >
                ×
              </Button>
            </li>
          ))}
        </ul>
      )}
      {picking ? (
        available.length === 0 ? (
          <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>
            {t('task_detail.agent.assign.none_available')}
          </p>
        ) : (
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
        )
      ) : (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          disabled={addAgent.isPending}
          onClick={() => {
            setPicking(true);
          }}
        >
          {t('task_detail.agent.assign.add')}
        </Button>
      )}
    </>
  );
}
