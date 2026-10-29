/**
 * DependenciesSection — task detail "Dependencies" panel.
 *
 * Renders groups for every TaskDependencyKind (Parent / Subtasks /
 * Blocked by / Blocks / Relates to / Duplicate of / Duplicated by)
 * backed by `GET /tasks/{id}/dependencies`. The picker uses
 * `GET /tasks?q=...` scoped to the current workspace; results are
 * debounced client-side.
 */

import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Input from '@nodate-flow/ui/primitives/input';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import Select from '@nodate-flow/ui/primitives/select';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { X } from 'lucide-react';
import { type ReactElement, useEffect, useMemo, useState } from 'react';
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
  useTaskSearch,
} from './api';
import { STATE_KEY, STATE_TONE } from './constants';
import styles from './dependencies-section.module.css';

interface DependenciesSectionProps {
  taskId: string;
  workspaceId: string;
}

type AddableKind = Extract<TaskDependencyKind, 'blocks' | 'relates' | 'subtask_of' | 'duplicates'>;

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
  const parent = useMemo(
    () => data.outgoing.filter((e) => e.kind === 'subtask_of'),
    [data.outgoing],
  );
  const subtasks = useMemo(
    () => data.incoming.filter((e) => e.kind === 'subtask_of'),
    [data.incoming],
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
    parent.length === 0 &&
    subtasks.length === 0 &&
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
      <header className={styles.header}>
        <h2 className={styles.headerTitle}>{t('tasks.detail.dependencies.title')}</h2>
        {!picking ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => {
              setPicking(true);
            }}
          >
            {t('tasks.detail.dependencies.add')}
          </Button>
        ) : null}
      </header>

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
        label={t('tasks.detail.dependencies.parent')}
        items={parent}
        tone="info"
        onRemove={handleRemove}
        removingId={removingId}
      />
      <DependencyGroup
        label={t('tasks.detail.dependencies.subtasks')}
        items={subtasks}
        tone="info"
        onRemove={handleRemove}
        removingId={removingId}
      />
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
  const [kind, setKind] = useState<AddableKind>('blocks');
  const [input, setInput] = useState('');
  const [debounced, setDebounced] = useState('');
  const add = useAddTaskDependency();

  useEffect(() => {
    const id = window.setTimeout(() => {
      setDebounced(input.trim());
    }, 200);
    return () => {
      window.clearTimeout(id);
    };
  }, [input]);

  const search = useTaskSearch(workspaceId, debounced, debounced.length > 0);
  const results = (search.data ?? []).filter((task) => !excludeIds.has(task.id));
  const stage =
    debounced.length === 0
      ? 'hint'
      : search.isFetching
        ? 'loading'
        : results.length === 0
          ? 'empty'
          : 'results';

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
    <div className={styles.picker}>
      <div className={styles.pickerControls}>
        <Select
          aria-label={t('tasks.detail.dependencies.kindLabel')}
          value={kind}
          onChange={(e) => {
            setKind(e.target.value as AddableKind);
          }}
          className={styles.pickerKindSelect}
        >
          <option value="blocks">{t('tasks.detail.dependencies.kind.blocks')}</option>
          <option value="relates">{t('tasks.detail.dependencies.kind.relates')}</option>
          <option value="subtask_of">{t('tasks.detail.dependencies.kind.subtask_of')}</option>
          <option value="duplicates">{t('tasks.detail.dependencies.kind.duplicates')}</option>
        </Select>
        <Input
          value={input}
          onChange={(e) => {
            setInput(e.target.value);
          }}
          placeholder={t('tasks.detail.dependencies.search')}
          autoFocus
          aria-label={t('tasks.detail.dependencies.search')}
          className={styles.pickerSearch}
        />
        <Button type="button" variant="ghost" size="sm" onClick={onClose}>
          {t('tasks.detail.dependencies.addCancel')}
        </Button>
      </div>
      <div key={stage} className={styles.pickerBody}>
        {debounced.length === 0 ? (
          <p className={styles.pickerHint}>{t('tasks.detail.dependencies.searchHint')}</p>
        ) : search.isFetching ? (
          <div className={styles.pickerLoadingRow} role="status" aria-label={t('common.loading')}>
            <Skeleton className={styles.pickerSkeletonRow} />
            <Skeleton className={styles.pickerSkeletonRow} />
            <Skeleton className={styles.pickerSkeletonRow} />
          </div>
        ) : results.length === 0 ? (
          <p className={styles.pickerHint}>{t('tasks.detail.dependencies.searchEmpty')}</p>
        ) : (
          <ul className={styles.pickerResults}>
            {results.map((task) => (
              <li key={task.id}>
                <button
                  type="button"
                  onClick={() => {
                    void handlePick(task.id);
                  }}
                  className={styles.pickerResultButton}
                >
                  <Badge tone={STATE_TONE[task.derivedState as TaskDerivedState] ?? 'neutral'}>
                    {t(STATE_KEY[task.derivedState as TaskDerivedState] ?? 'tasks.status.open')}
                  </Badge>
                  <span className={styles.pickerResultTitle} title={task.title}>
                    {task.title}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
