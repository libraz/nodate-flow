/**
 * SubtasksSection — task detail "Subtasks" panel: progress, the direct
 * children, and a continuous inline add. Created titles show as muted rows
 * until the refetch carrying them lands, so typing the next one never waits.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import Avatar from '@nodate-flow/ui/primitives/avatar';
import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Input from '@nodate-flow/ui/primitives/input';
import Spinner from '@nodate-flow/ui/primitives/spinner';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Link } from '@tanstack/react-router';
import { Plus, Search, X } from 'lucide-react';
import {
  type CSSProperties,
  type KeyboardEvent,
  type ReactElement,
  useId,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';
import { formatApiError } from '../../lib/api-error';
import { formatDate, isOverdue } from '../../lib/format';
import { useEffectiveZone } from '../../lib/use-effective-timezone';
import {
  type TaskDerivedState,
  type TaskListItem,
  useCreateSubtask,
  useSetTaskParent,
  useSubtasksQuery,
} from './api';
import CompleteTaskButton from './complete-task-button';
import { STATE_COLOR } from './constants';
import css from './subtasks-section.module.css';
import TaskSearchPicker from './task-search-picker';
import TaskSectionHeading from './task-section-heading';

/** Avatars stacked before the rest collapse into "+N". */
const MAX_AVATARS = 2;

export interface SubtasksSectionProps {
  taskId: string;
  projectId: string;
  workspaceId: string;
  /** The parent's visibility, inherited by every subtask created here. */
  visibility: string;
  /** This task's own parent, kept out of the "add existing" results. */
  parentTaskId?: string | undefined;
}

interface PendingSubtask {
  key: string;
  title: string;
}

function SubtaskRow({
  task,
  detaching,
  onDetach,
}: {
  task: TaskListItem;
  detaching: boolean;
  onDetach: (task: TaskListItem) => void;
}): ReactElement {
  const { t, i18n } = useTranslation('common');
  const zone = useEffectiveZone();
  const locale = i18n.resolvedLanguage ?? 'en';
  const state = task.derivedState as TaskDerivedState;
  const closed = state === 'done' || state === 'cancelled';
  const assignees = (task.assignees ?? []).slice(0, MAX_AVATARS);
  const moreAssignees = task.assigneeCount - assignees.length;

  return (
    <li
      className={css.row}
      data-closed={closed}
      style={{ '--state-tone': STATE_COLOR[state] } as CSSProperties}
    >
      <CompleteTaskButton
        task={{ id: task.id, title: task.title, projectId: task.projectId, derivedState: state }}
      />
      <Link
        to="/tasks/$taskId"
        params={{ taskId: task.id }}
        className={cx(css.title, 'nf-focus-ring')}
        title={task.title}
      >
        {task.title}
      </Link>
      {task.assigneeCount > 0 ? (
        <span
          className={css.assignees}
          title={(task.assignees ?? []).map((a) => a.displayName).join(', ')}
        >
          {assignees.map((a) => (
            <Avatar
              key={a.id}
              size="sm"
              alt={a.displayName}
              initials={a.displayName.slice(0, 1)}
              className={css.avatar}
            />
          ))}
          {moreAssignees > 0 ? (
            <span className={css.more}>{t('tasks.assignee.plus_n', { n: moreAssignees })}</span>
          ) : null}
        </span>
      ) : null}
      {task.dueOn ? (
        <Badge
          tone={isOverdue(task.dueOn, zone) && !closed ? 'danger' : 'neutral'}
          aria-label={`${t('tasks.columns.due')}: ${formatDate(task.dueOn, locale)}`}
        >
          {formatDate(task.dueOn, locale)}
        </Badge>
      ) : null}
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className={css.detach}
        aria-label={t('tasks.detail.subtasks.detach_named', { title: task.title })}
        title={t('tasks.detail.subtasks.detach')}
        aria-disabled={detaching ? true : undefined}
        aria-busy={detaching ? true : undefined}
        onClick={() => {
          if (!detaching) onDetach(task);
        }}
      >
        <X aria-hidden="true" />
      </Button>
    </li>
  );
}

export default function SubtasksSection({
  taskId,
  projectId,
  workspaceId,
  visibility,
  parentTaskId,
}: SubtasksSectionProps): ReactElement {
  const { t } = useTranslation('common');
  const { data } = useSubtasksQuery(taskId, workspaceId);
  const create = useCreateSubtask();
  const setParent = useSetTaskParent();
  const headingId = useId();
  const hintId = useId();
  const [adding, setAdding] = useState(false);
  const [attaching, setAttaching] = useState(false);
  const [text, setText] = useState('');
  const [pending, setPending] = useState<PendingSubtask[]>([]);
  // Titles with a request in flight, kept outside state so a repeated Enter sees them at once.
  const inFlight = useRef(new Set<string>());
  const nextKey = useRef(0);
  // Set when Esc closes the input, so the add button takes focus back as it remounts.
  const restoreFocus = useRef(false);

  const total = data.total;
  const done = data.tasks.filter((task) => task.derivedState === 'done').length;
  const percent = total > 0 ? Math.round((done / total) * 100) : 0;

  const handleClose = (): void => {
    setAdding(false);
    setText('');
  };

  const handleSubmit = (): void => {
    const title = text.trim();
    if (title === '' || inFlight.current.has(title)) return;
    inFlight.current.add(title);
    const key = `pending-${nextKey.current++}`;
    setPending((rows) => [...rows, { key, title }]);
    setText('');
    create.mutateAsync({ parentTaskId: taskId, projectId, title, visibility }).then(
      () => {
        inFlight.current.delete(title);
        setPending((rows) => rows.filter((row) => row.key !== key));
      },
      (err: unknown) => {
        inFlight.current.delete(title);
        setPending((rows) => rows.filter((row) => row.key !== key));
        toaster.show({
          tone: 'danger',
          message: formatApiError(err, t, 'tasks.detail.subtasks.add_error'),
        });
        // Give the typed text back unless the user has already started the next one.
        setText((current) => (current === '' ? title : current));
        setAdding(true);
      },
    );
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLInputElement>): void => {
    if (e.key === 'Enter') {
      // Enter that confirms an IME candidate must not submit.
      if (e.nativeEvent.isComposing || e.keyCode === 229) return;
      e.preventDefault();
      handleSubmit();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      restoreFocus.current = true;
      handleClose();
    }
  };

  const handleDetach = (task: TaskListItem): void => {
    setParent.mutate(
      { id: task.id, parentTaskId: null },
      {
        onError: (err) => {
          toaster.show({
            tone: 'danger',
            message: formatApiError(err, t, 'tasks.detail.subtasks.detach_error'),
          });
        },
      },
    );
  };

  const handleAttach = async (childId: string): Promise<void> => {
    try {
      await setParent.mutateAsync({ id: childId, parentTaskId: taskId });
      setAttaching(false);
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.detail.subtasks.attach_error'),
      });
    }
  };

  const handleBlur = (): void => {
    if (text.trim() === '') setAdding(false);
  };

  return (
    <section className={css.section} aria-labelledby={headingId}>
      <TaskSectionHeading
        id={headingId}
        title={t('tasks.detail.subtasks.title')}
        trailing={
          total > 0 ? (
            <span className={css.count}>{t('tasks.detail.subtasks.count', { done, total })}</span>
          ) : null
        }
      />
      {total > 0 ? (
        <div
          className={css.track}
          role="progressbar"
          aria-valuemin={0}
          aria-valuenow={done}
          aria-valuemax={total}
          aria-label={t('tasks.detail.subtasks.progress_aria', { done, total })}
        >
          <div className={css.fill} style={{ '--progress': percent } as CSSProperties} />
        </div>
      ) : null}

      {data.tasks.length > 0 || pending.length > 0 ? (
        <ul className={css.list}>
          {data.tasks.map((task) => (
            <SubtaskRow
              key={task.id}
              task={task}
              detaching={setParent.isPending && setParent.variables?.id === task.id}
              onDetach={handleDetach}
            />
          ))}
          {pending.map((row) => (
            <li key={row.key} className={cx(css.row, css.pendingRow)} aria-busy="true">
              <span className={css.pendingMark}>
                <Spinner size="sm" label={t('tasks.detail.subtasks.creating')} />
              </span>
              <span className={css.pendingTitle}>{row.title}</span>
            </li>
          ))}
        </ul>
      ) : null}

      {adding ? (
        <div className={css.adder}>
          <Input
            autoFocus
            value={text}
            placeholder={t('tasks.detail.subtasks.placeholder')}
            aria-label={t('tasks.detail.subtasks.input_label')}
            aria-describedby={hintId}
            onChange={(e) => {
              setText(e.target.value);
            }}
            onKeyDown={handleKeyDown}
            onBlur={handleBlur}
          />
          <p id={hintId} className={css.hint}>
            {t('tasks.detail.subtasks.hint')}
          </p>
        </div>
      ) : attaching ? null : (
        <div className={css.actions}>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className={css.addButton}
            ref={(node: HTMLElement | null) => {
              if (node && restoreFocus.current) {
                restoreFocus.current = false;
                node.focus();
              }
            }}
            onClick={() => {
              setAdding(true);
            }}
          >
            <Plus aria-hidden="true" />
            {t('tasks.detail.subtasks.add')}
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className={css.addButton}
            onClick={() => {
              setAttaching(true);
            }}
          >
            <Search aria-hidden="true" />
            {t('tasks.detail.subtasks.add_existing')}
          </Button>
        </div>
      )}
      {attaching ? (
        <TaskSearchPicker
          workspaceId={workspaceId}
          excludeIds={
            new Set([
              taskId,
              ...(parentTaskId ? [parentTaskId] : []),
              ...data.tasks.map((task) => task.id),
            ])
          }
          accept={(task) => task.projectId === projectId}
          placeholder={t('tasks.detail.subtasks.existing_placeholder')}
          hint={t('tasks.detail.subtasks.existing_hint')}
          emptyText={t('tasks.detail.subtasks.existing_empty')}
          cancelText={t('common.cancel')}
          onPick={handleAttach}
          onClose={() => {
            setAttaching(false);
          }}
        />
      ) : null}
    </section>
  );
}
