import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Combobox from '@nodate-flow/ui/primitives/combobox';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { formatApiError } from '../../../lib/api-error';
import { useWorkspaceMembersQuery } from '../../workspaces/api';
import { useAddTaskActor, useRemoveTaskActor, useTaskActorsQuery } from '../api';
import { useWorkflowQuery } from './api';
import css from './task-role-assignments.module.css';

type ReviewRole = 'reviewer' | 'approver';

export interface TaskRoleAssignmentsProps {
  taskId: string;
  workspaceId: string;
}

export default function TaskRoleAssignments({
  taskId,
  workspaceId,
}: TaskRoleAssignmentsProps): ReactElement {
  const { t } = useTranslation('common');
  const { data: actors } = useTaskActorsQuery(taskId);
  const { data: members } = useWorkspaceMembersQuery(workspaceId);
  const workflow = useWorkflowQuery(taskId);
  const addActor = useAddTaskActor();
  const removeActor = useRemoveTaskActor();
  const [pickingRole, setPickingRole] = useState<ReviewRole | null>(null);
  const [error, setError] = useState<string | null>(null);
  const canManageRoles = workflow.data?.capabilities.canSetPolicy === true;

  // biome-ignore lint/correctness/useExhaustiveDependencies: Reset task-specific role drafts when navigating to another task.
  useEffect(() => {
    setPickingRole(null);
    setError(null);
  }, [taskId]);

  const reviewActors = actors.filter(
    (actor) => actor.role === 'reviewer' || actor.role === 'approver',
  );
  const assignedIds = new Set(
    reviewActors.filter((actor) => actor.role === pickingRole).map((actor) => actor.userId),
  );
  const availableMembers = members.filter((member) => !assignedIds.has(member.userId));

  const startPicking = (role: ReviewRole): void => {
    setError(null);
    setPickingRole(role);
  };

  const handleAdd = async (userId: string): Promise<void> => {
    if (!canManageRoles || !pickingRole || !userId) return;
    setError(null);
    try {
      await addActor.mutateAsync({ taskId, input: { role: pickingRole, userId } });
      setPickingRole(null);
    } catch (err) {
      setError(formatApiError(err, t, 'taskWorkflow.roleUpdateFailed'));
    }
  };

  const handleRemove = async (actorId: string): Promise<void> => {
    if (!canManageRoles) return;
    setError(null);
    try {
      await removeActor.mutateAsync({ taskId, actorId });
    } catch (err) {
      setError(formatApiError(err, t, 'taskWorkflow.roleUpdateFailed'));
    }
  };

  const roleLabel = (role: string): string =>
    t(role === 'approver' ? 'taskWorkflow.approverRole' : 'taskWorkflow.reviewerRole');
  const pickerLabel = t(
    pickingRole === 'approver' ? 'taskWorkflow.addApprover' : 'taskWorkflow.addReviewer',
  );

  return (
    <div className={css.root}>
      {reviewActors.length === 0 ? (
        <p className={css.muted}>{t('taskWorkflow.noReviewers')}</p>
      ) : (
        <ul className={css.list}>
          {reviewActors.map((actor) => (
            <li key={actor.id} className={css.row}>
              <span className={css.name}>{actor.displayName}</span>
              <Badge tone="neutral">{roleLabel(actor.role)}</Badge>
              {canManageRoles ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  disabled={removeActor.isPending}
                  aria-busy={
                    (removeActor.isPending && removeActor.variables.actorId === actor.id) ||
                    undefined
                  }
                  aria-label={t('taskWorkflow.removeRole', {
                    name: actor.displayName,
                    role: roleLabel(actor.role),
                  })}
                  onClick={() => {
                    void handleRemove(actor.id);
                  }}
                >
                  {t('taskWorkflow.remove')}
                </Button>
              ) : null}
            </li>
          ))}
        </ul>
      )}

      {!canManageRoles ? null : pickingRole ? (
        <div className={css.picker}>
          {availableMembers.length === 0 ? (
            <p className={css.pickerEmpty}>{t('taskWorkflow.noAvailableMembers')}</p>
          ) : (
            <div className={css.pickerInput}>
              <Combobox
                aria-label={pickerLabel}
                placeholder={pickerLabel}
                disabled={addActor.isPending}
                options={availableMembers.map((member) => ({
                  value: member.userId,
                  label: member.displayName,
                }))}
                onChange={(userId) => {
                  void handleAdd(userId);
                }}
              />
            </div>
          )}
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={addActor.isPending}
            onClick={() => setPickingRole(null)}
          >
            {t('taskWorkflow.cancel')}
          </Button>
        </div>
      ) : (
        <div className={css.addRow}>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={addActor.isPending}
            onClick={() => startPicking('reviewer')}
          >
            {t('taskWorkflow.addReviewer')}
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={addActor.isPending}
            onClick={() => startPicking('approver')}
          >
            {t('taskWorkflow.addApprover')}
          </Button>
        </div>
      )}

      {error ? (
        <p role="alert" className={css.error}>
          {error}
        </p>
      ) : null}
    </div>
  );
}
