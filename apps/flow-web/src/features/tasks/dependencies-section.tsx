/**
 * DependenciesSection — task detail "Dependencies" panel.
 *
 * Renders groups for every TaskDependencyKind (Blocked by / Blocks /
 * Relates to / Duplicate of / Duplicated by)
 * backed by `GET /tasks/{id}/dependencies`. The picker uses
 * `GET /tasks?q=...` scoped to the current workspace; results are
 * debounced client-side.
 */

import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import Select from '@nodate-flow/ui/primitives/select';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { Plus, X } from 'lucide-react';
import { type ReactElement, useId, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { formatApiError } from '../../lib/api-error';
import {
  type TaskDependencyEdge,
  type TaskDependencyKind,
  type TaskDerivedState,
  tasksKeys,
  useAddTaskDependency,
  useRemoveTaskDependency,
  useTaskDependenciesQuery,
} from './api';
import { STATE_KEY, STATE_TONE } from './constants';
import styles from './dependencies-section.module.css';
import TaskSearchPicker from './task-search-picker';
import searchStyles from './task-search-picker.module.css';
import TaskSectionHeading from './task-section-heading';

interface DependenciesSectionProps {
  taskId: string;
  workspaceId: string;
}

/**
 * DependenciesSection renders incoming/outgoing dependency edges and
 * an inline picker to add new ones.
 */
export default function DependenciesSection({
  taskId,
  workspaceId,
}: DependenciesSectionProps): ReactElement {
  const { t } = useTranslation('common');
  const { data } = useTaskDependenciesQuery(taskId);
  const remove = useRemoveTaskDependency();
  const add = useAddTaskDependency();
  const qc = useQueryClient();
  const [picking, setPicking] = useState(false);
  const [removingId, setRemovingId] = useState<string | null>(null);

  const blocks = useMemo(() => data.outgoing.filter((e) => e.kind === 'blocks'), [data.outgoing]);
  const blockedBy = useMemo(
    () => data.incoming.filter((e) => e.kind === 'blocks'),
    [data.incoming],
  );
  const relates = useMemo(
    () => [
      ...data.outgoing.filter((e) => e.kind === 'relates'),
      ...data.incoming.filter((e) => e.kind === 'relates'),
    ],
    [data.outgoing, data.incoming],
  );
  const duplicateOf = useMemo(
    () => data.outgoing.filter((e) => e.kind === 'duplicates'),
    [data.outgoing],
  );
  const duplicatedBy = useMemo(
    () => data.incoming.filter((e) => e.kind === 'duplicates'),
    [data.incoming],
  );

  const isEmpty =
    blocks.length === 0 &&
    blockedBy.length === 0 &&
    relates.length === 0 &&
    duplicateOf.length === 0 &&
    duplicatedBy.length === 0;

  const handleRemove = async (edge: TaskDependencyEdge): Promise<void> => {
    const outgoing = data.outgoing.some((e) => e.id === edge.id);
    const kind = edge.kind as TaskDependencyKind;
    setRemovingId(edge.id);
    try {
      await remove.mutateAsync({ taskId, depId: edge.id });
      toaster.show({
        tone: 'info',
        message: t('tasks.detail.dependencies.removed', { title: edge.otherTaskTitle }),
        action: {
          label: t('common.undo'),
          onAction: () => {
            // The edge keeps its direction: an incoming edge is owned by the other task.
            const args = outgoing
              ? { taskId, toTaskId: edge.otherTaskId, kind }
              : { taskId: edge.otherTaskId, toTaskId: taskId, kind };
            add.mutateAsync(args).then(
              () => qc.invalidateQueries({ queryKey: tasksKeys.dependencies(taskId) }),
              (err: unknown) => {
                toaster.show({
                  tone: 'danger',
                  message: formatApiError(err, t, 'tasks.detail.dependencies.addError'),
                });
              },
            );
          },
        },
      });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.detail.dependencies.removeError'),
      });
    } finally {
      setRemovingId(null);
    }
  };

  return (
    <section aria-label={t('tasks.detail.dependencies.title')} className={styles.section}>
      <TaskSectionHeading
        title={t('tasks.detail.dependencies.title')}
        trailing={
          !picking ? (
            <Button
              type="button"
              size="sm"
              onClick={() => {
                setPicking(true);
              }}
            >
              <Plus size={14} aria-hidden />
              {t('tasks.detail.dependencies.add')}
            </Button>
          ) : null
        }
      />

      {picking ? (
        <DependencyPicker
          taskId={taskId}
          workspaceId={workspaceId}
          excludeIds={
            new Set([
              taskId,
              ...data.outgoing.map((e) => e.otherTaskId),
              ...data.incoming.map((e) => e.otherTaskId),
            ])
          }
          onClose={() => {
            setPicking(false);
          }}
        />
      ) : null}

      {isEmpty && !picking ? (
        <p className={styles.empty}>{t('tasks.detail.dependencies.empty')}</p>
      ) : null}

      <DependencyGroup
        label={t('tasks.detail.dependencies.blockedBy')}
        items={blockedBy}
        tone="danger"
        onRemove={handleRemove}
        removingId={removingId}
      />
      <DependencyGroup
        label={t('tasks.detail.dependencies.blocks')}
        items={blocks}
        tone="warning"
        onRemove={handleRemove}
        removingId={removingId}
      />
      <DependencyGroup
        label={t('tasks.detail.dependencies.relates')}
        items={relates}
        tone="neutral"
        onRemove={handleRemove}
        removingId={removingId}
      />
      <DependencyGroup
        label={t('tasks.detail.dependencies.duplicateOf')}
        items={duplicateOf}
        tone="neutral"
        onRemove={handleRemove}
        removingId={removingId}
      />
      <DependencyGroup
        label={t('tasks.detail.dependencies.duplicatedBy')}
        items={duplicatedBy}
        tone="neutral"
        onRemove={handleRemove}
        removingId={removingId}
      />
    </section>
  );
}

function DependencyGroup({
  label,
  items,
  tone,
  onRemove,
  removingId,
}: {
  label: string;
  items: TaskDependencyEdge[];
  tone: BadgeTone;
  onRemove: (edge: TaskDependencyEdge) => void | Promise<void>;
  removingId: string | null;
}): ReactElement | null {
  const { t } = useTranslation('common');
  const { entries, onExited } = usePresenceList(items, (edge) => edge.id);
  if (entries.length === 0) return null;
  return (
    <div className={styles.group}>
      <div className={styles.groupHeader}>
        <Badge tone={tone}>{label}</Badge>
        <span className={styles.groupCount}>({items.length})</span>
      </div>
      <ul className={styles.groupList}>
        {entries.map(({ key, item: edge, present }) => {
          const state = edge.otherTaskDerivedState as TaskDerivedState;
          return (
            <PresenceItem
              key={key}
              as="li"
              present={present}
              onExited={() => onExited(key)}
              className={motion.enter}
            >
              <div className={styles.groupItem}>
                <Badge tone={STATE_TONE[state] ?? 'neutral'}>
                  {t(STATE_KEY[state] ?? 'tasks.status.open')}
                </Badge>
                <Link
                  to="/tasks/$taskId"
                  params={{ taskId: edge.otherTaskId }}
                  className={styles.groupLink}
                  title={edge.otherTaskTitle}
                >
                  {edge.otherTaskTitle}
                </Link>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  aria-label={t('tasks.detail.dependencies.removeNamed', {
                    title: edge.otherTaskTitle,
                  })}
                  disabled={removingId === edge.id}
                  aria-busy={removingId === edge.id}
                  onClick={() => {
                    void onRemove(edge);
                  }}
                >
                  <X size={14} aria-hidden />
                </Button>
              </div>
            </PresenceItem>
          );
        })}
      </ul>
    </div>
  );
}

function DependencyPicker({
  taskId,
  workspaceId,
  excludeIds,
  onClose,
}: {
  taskId: string;
  workspaceId: string;
  excludeIds: Set<string>;
  onClose: () => void;
}): ReactElement {
  const { t } = useTranslation('common');
  const [kind, setKind] = useState<TaskDependencyKind>('blocks');
  const kindHintId = useId();
  const add = useAddTaskDependency();

  const handlePick = async (toTaskId: string): Promise<void> => {
    try {
      await add.mutateAsync({ taskId, toTaskId, kind });
      onClose();
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.detail.dependencies.addError'),
      });
    }
  };

  return (
    <TaskSearchPicker
      workspaceId={workspaceId}
      excludeIds={excludeIds}
      placeholder={t('tasks.detail.dependencies.search')}
      hint={t('tasks.detail.dependencies.searchHint')}
      emptyText={t('tasks.detail.dependencies.searchEmpty')}
      cancelText={t('tasks.detail.dependencies.addCancel')}
      leading={
        <Select
          aria-label={t('tasks.detail.dependencies.kindLabel')}
          aria-describedby={kindHintId}
          value={kind}
          onChange={(e) => {
            setKind(e.target.value as TaskDependencyKind);
          }}
          className={styles.kindSelect}
        >
          <option value="blocks">{t('tasks.detail.dependencies.kind.blocks')}</option>
          <option value="relates">{t('tasks.detail.dependencies.kind.relates')}</option>
          <option value="duplicates">{t('tasks.detail.dependencies.kind.duplicates')}</option>
        </Select>
      }
      description={
        <p id={kindHintId} className={searchStyles.pickerHint}>
          {t('tasks.detail.dependencies.kindHint')}
        </p>
      }
      onPick={handlePick}
      onClose={onClose}
    />
  );
}
