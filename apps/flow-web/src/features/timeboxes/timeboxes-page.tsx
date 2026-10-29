/**
 * @file `/workspaces/$id/timeboxes` route page. Lists every timebox
 * in the workspace grouped by status — Active first (prominent),
 * Planned second, Completed / Cancelled folded under a collapsible
 * "Archive" section at the bottom.
 *
 * Each card carries the contextual action buttons that are legal for
 * its status:
 *   - planned   -> Start | Edit | Cancel | Delete
 *   - active    -> Stop  | Cancel | (expand task list)
 *   - completed -> Delete
 *   - cancelled -> Delete
 *
 * Plan said `/start` `/stop`. Reality is `POST /status` with a
 * `{status}` payload, so the verbs map to:
 *   Start  = planned -> active
 *   Stop   = active -> completed
 *   Cancel = * -> cancelled
 *
 * Workspace task pickers do not yet have a clean per-workspace tasks
 * list endpoint surfaced through the SDK in a way that matches the
 * "ID-by-paste" requirement, so the inline "add task" affordance is a
 * paste-UUID input. The translation hint string flags this as a
 * temporary state.
 */

import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Collapse from '@nodate-flow/ui/primitives/collapse';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import Input from '@nodate-flow/ui/primitives/input';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { getRouteApi } from '@tanstack/react-router';
import { ChevronRight } from 'lucide-react';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import type { TaskDerivedState } from '../tasks/api';
import { STATE_KEY, STATE_TONE } from '../tasks/constants';
import {
  asTimeboxStatus,
  type Timebox,
  type TimeboxStatus,
  type TimeboxTask,
  useAddTimeboxTaskMutation,
  useDeleteTimeboxMutation,
  useRemoveTimeboxTaskMutation,
  useTimeboxesQuery,
  useTimeboxTasksQuery,
  useUpdateTimeboxStatusMutation,
} from './api';
import TimeboxDialog from './timebox-dialog';
import styles from './timeboxes-page.module.css';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/timeboxes');

/** Map a timebox status to a Badge tone. */
function toneForStatus(status: TimeboxStatus): BadgeTone {
  switch (status) {
    case 'active':
      return 'accent';
    case 'completed':
      return 'success';
    case 'cancelled':
      return 'danger';
    default:
      return 'neutral';
  }
}

/** i18n key for each timebox status. */
const STATUS_KEY: Record<TimeboxStatus, string> = {
  planned: 'timeboxes.status.planned',
  active: 'timeboxes.status.active',
  completed: 'timeboxes.status.completed',
  cancelled: 'timeboxes.status.cancelled',
};

/**
 * The task's state as a known one, or null for a value this build does
 * not name — drawn as a neutral badge with the raw value rather than
 * guessed at.
 */
function knownTaskState(state: string): TaskDerivedState | null {
  return state in STATE_KEY ? (state as TaskDerivedState) : null;
}

/** Format a YYYY-MM-DD date with the active locale's medium style. */
function formatYmd(ymd: string, locale: string): string {
  if (!ymd) return '';
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(
      new Date(`${ymd}T00:00:00`),
    );
  } catch {
    return ymd;
  }
}

interface TaskListProps {
  workspaceId: string;
  timeboxId: string;
}

function TaskList({ workspaceId, timeboxId }: TaskListProps): ReactElement {
  const { t } = useTranslation('common');
  const tasksQuery = useTimeboxTasksQuery(workspaceId, timeboxId);
  const tasks: TimeboxTask[] = tasksQuery.data ?? [];
  const removeTask = useRemoveTimeboxTaskMutation();
  const addTask = useAddTimeboxTaskMutation();
  const [taskIdInput, setTaskIdInput] = useState('');
  const taskPresence = usePresenceList(tasks, (task) => task.id);

  const handleRemove = async (taskId: string): Promise<void> => {
    try {
      await removeTask.mutateAsync({ wsId: workspaceId, timeboxId, taskId });
      toaster.show({ tone: 'success', message: t('timeboxes.remove_task.success') });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'timeboxes.remove_task.error'),
      });
    }
  };

  const handleAdd = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    const trimmed = taskIdInput.trim();
    if (trimmed.length === 0) return;
    try {
      await addTask.mutateAsync({ wsId: workspaceId, timeboxId, taskId: trimmed });
      setTaskIdInput('');
      toaster.show({ tone: 'success', message: t('timeboxes.add_task.success') });
    } catch (err) {
      toaster.show({ tone: 'danger', message: formatApiError(err, t, 'timeboxes.add_task.error') });
    }
  };

  return (
    <div className={styles.tasks}>
      <h4 className={styles.tasksTitle}>{t('timeboxes.add_task.label')}</h4>
      {taskPresence.entries.length === 0 ? (
        <p className={styles.tasksEmpty}>{t('timeboxes.no_tasks')}</p>
      ) : (
        taskPresence.entries.map((entry) => {
          const task = entry.item;
          const state = knownTaskState(task.derivedState);
          return (
            <PresenceItem
              key={entry.key}
              present={entry.present}
              onExited={() => taskPresence.onExited(entry.key)}
            >
              <div className={styles.taskRow}>
                <span className={styles.taskTitle} title={task.title}>
                  {task.title}
                </span>
                <Badge tone={state ? STATE_TONE[state] : 'neutral'}>
                  {state ? t(STATE_KEY[state]) : task.derivedState}
                </Badge>
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  disabled={removeTask.isPending}
                  aria-busy={
                    (removeTask.isPending && removeTask.variables?.taskId === task.id) || undefined
                  }
                  onClick={() => {
                    void handleRemove(task.id);
                  }}
                >
                  {t('timeboxes.remove_task.label')}
                </Button>
              </div>
            </PresenceItem>
          );
        })
      )}
      <form
        onSubmit={(e) => {
          void handleAdd(e);
        }}
        className={styles.addTaskRow}
      >
        <Input
          className={styles.addTaskInput}
          aria-label={t('timeboxes.add_task_id.label')}
          placeholder={t('timeboxes.add_task_id.label')}
          value={taskIdInput}
          onChange={(e) => {
            setTaskIdInput(e.target.value);
          }}
        />
        <Button
          type="submit"
          variant="default"
          size="sm"
          disabled={taskIdInput.trim().length === 0 || addTask.isPending}
          aria-busy={addTask.isPending || undefined}
        >
          {t('timeboxes.add_task.label')}
        </Button>
      </form>
      <p className={styles.addTaskHint}>{t('timeboxes.add_task_id.hint')}</p>
    </div>
  );
}

interface TimeboxCardProps {
  timebox: Timebox;
  workspaceId: string;
  locale: string;
  onEdit: (timebox: Timebox) => void;
}

function TimeboxCard({ timebox, workspaceId, locale, onEdit }: TimeboxCardProps): ReactElement {
  const { t } = useTranslation('common');
  const status = asTimeboxStatus(timebox.status);
  const updateStatus = useUpdateTimeboxStatusMutation();
  const deleteMutation = useDeleteTimeboxMutation();
  const tasksQuery = useTimeboxTasksQuery(workspaceId, timebox.id, status === 'active');
  const tasks = tasksQuery.data ?? [];
  const totalTasks = tasks.length;
  const completedTasks = tasks.filter((tk) => tk.derivedState === 'done').length;
  const progressPct = totalTasks > 0 ? Math.round((completedTasks / totalTasks) * 100) : 0;
  const [tasksOpen, setTasksOpen] = useState(status === 'active');

  const transition = async (next: TimeboxStatus, errorKey: string): Promise<void> => {
    try {
      await updateStatus.mutateAsync({
        wsId: workspaceId,
        timeboxId: timebox.id,
        status: next,
      });
    } catch (err) {
      toaster.show({ tone: 'danger', message: formatApiError(err, t, errorKey) });
    }
  };

  const handleDelete = async (): Promise<void> => {
    const ok = await confirmAction({
      message: t('timeboxes.delete.confirm'),
      tone: 'danger',
    });
    if (!ok) return;
    try {
      await deleteMutation.mutateAsync({ wsId: workspaceId, timeboxId: timebox.id });
      toaster.show({ tone: 'success', message: t('timeboxes.delete.success') });
    } catch (err) {
      toaster.show({ tone: 'danger', message: formatApiError(err, t, 'timeboxes.delete.error') });
    }
  };

  const cardClass = [
    styles.card,
    status === 'active' ? styles.cardActive : '',
    status === 'completed' || status === 'cancelled' ? styles.cardArchived : '',
  ]
    .filter(Boolean)
    .join(' ');

  const showTasks = status === 'active';
  const busy = updateStatus.isPending || deleteMutation.isPending;
  /** Props for the button that moves the card to `next`: locked while any write runs, busy for its own. */
  const transitionProps = (
    next: TimeboxStatus,
  ): { disabled: boolean; 'aria-busy': true | undefined } => ({
    disabled: busy,
    'aria-busy': (updateStatus.isPending && updateStatus.variables?.status === next) || undefined,
  });
  const deleteProps = {
    disabled: busy,
    'aria-busy': deleteMutation.isPending || undefined,
  };

  return (
    <article className={cardClass}>
      <div className={styles.cardTop}>
        <h3 className={styles.cardName} title={timebox.name}>
          {timebox.name}
        </h3>
        <Badge tone={toneForStatus(status)}>{t(STATUS_KEY[status])}</Badge>
      </div>
      <div className={styles.cardMeta}>
        <span className={styles.dateRange}>
          {formatYmd(timebox.startsOn, locale)} – {formatYmd(timebox.endsOn, locale)}
        </span>
        {timebox.projectName ? (
          <span className={styles.projectName}>{timebox.projectName}</span>
        ) : null}
      </div>
      {timebox.description ? <p className={styles.description}>{timebox.description}</p> : null}
      {showTasks ? (
        <div className={styles.progressRow}>
          <div
            className={styles.progressBar}
            role="progressbar"
            aria-valuenow={progressPct}
            aria-valuemin={0}
            aria-valuemax={100}
          >
            <div className={styles.progressFill} style={{ scale: `${progressPct / 100} 1` }} />
          </div>
          <span className={styles.progressLabel}>
            {t('timeboxes.progress', { done: completedTasks, total: totalTasks })}
          </span>
        </div>
      ) : null}
      <div className={styles.actions}>
        {status === 'planned' ? (
          <>
            <Button
              type="button"
              size="sm"
              variant="primary"
              {...transitionProps('active')}
              onClick={() => {
                void transition('active', 'timeboxes.transition.error');
              }}
            >
              {t('timeboxes.action.start')}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="default"
              onClick={() => {
                onEdit(timebox);
              }}
            >
              {t('timeboxes.action.edit')}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              {...transitionProps('cancelled')}
              onClick={() => {
                void transition('cancelled', 'timeboxes.transition.error');
              }}
            >
              {t('timeboxes.action.cancel')}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="danger"
              {...deleteProps}
              onClick={() => {
                void handleDelete();
              }}
            >
              {t('timeboxes.action.delete')}
            </Button>
          </>
        ) : null}
        {status === 'active' ? (
          <>
            <Button
              type="button"
              size="sm"
              variant="primary"
              {...transitionProps('completed')}
              onClick={() => {
                void transition('completed', 'timeboxes.transition.error');
              }}
            >
              {t('timeboxes.action.stop')}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              {...transitionProps('cancelled')}
              onClick={() => {
                void transition('cancelled', 'timeboxes.transition.error');
              }}
            >
              {t('timeboxes.action.cancel')}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={() => {
                setTasksOpen((v) => !v);
              }}
              aria-expanded={tasksOpen}
            >
              <ChevronRight size={14} aria-hidden className={styles.disclosureIcon} />
              <span>{t('timeboxes.add_task.label')}</span>
            </Button>
          </>
        ) : null}
        {status === 'completed' || status === 'cancelled' ? (
          <Button
            type="button"
            size="sm"
            variant="danger"
            {...deleteProps}
            onClick={() => {
              void handleDelete();
            }}
          >
            {t('timeboxes.action.delete')}
          </Button>
        ) : null}
      </div>
      <Collapse open={showTasks && tasksOpen} className={styles.tasksCollapse}>
        <TaskList workspaceId={workspaceId} timeboxId={timebox.id} />
      </Collapse>
    </article>
  );
}

interface TimeboxCardListProps {
  timeboxes: readonly Timebox[];
  workspaceId: string;
  locale: string;
  onEdit: (timebox: Timebox) => void;
}

/** Cards of one section; a card that leaves (status change, delete) plays its exit. */
function TimeboxCardList({
  timeboxes,
  workspaceId,
  locale,
  onEdit,
}: TimeboxCardListProps): ReactElement {
  const presence = usePresenceList(timeboxes, (tb) => tb.id);
  return (
    <>
      {presence.entries.map((entry) => (
        <PresenceItem
          key={entry.key}
          present={entry.present}
          onExited={() => presence.onExited(entry.key)}
        >
          <TimeboxCard
            timebox={entry.item}
            workspaceId={workspaceId}
            locale={locale}
            onEdit={onEdit}
          />
        </PresenceItem>
      ))}
    </>
  );
}

/** Sort timeboxes within a section by `updatedAt` desc, falling back to `createdAt`. */
function byRecency(a: Timebox, b: Timebox): number {
  const upd = b.updatedAt - a.updatedAt;
  if (upd !== 0) return upd;
  return b.createdAt - a.createdAt;
}

export default function TimeboxesPage(): ReactElement {
  const { t, i18n } = useTranslation('common');
  const { id: workspaceId } = routeApi.useParams();
  const locale = i18n.resolvedLanguage ?? 'en';
  const { data: timeboxes } = useTimeboxesQuery(workspaceId);

  const [dialogOpen, setDialogOpen] = useState(false);
  const [editing, setEditing] = useState<Timebox | null>(null);
  const [archiveOpen, setArchiveOpen] = useState(false);

  const active: Timebox[] = [];
  const planned: Timebox[] = [];
  const archive: Timebox[] = [];
  for (const tb of timeboxes) {
    const s = asTimeboxStatus(tb.status);
    if (s === 'active') active.push(tb);
    else if (s === 'planned') planned.push(tb);
    else archive.push(tb);
  }
  active.sort(byRecency);
  planned.sort(byRecency);
  archive.sort(byRecency);

  const hasAny = timeboxes.length > 0;

  const openCreate = (): void => {
    setEditing(null);
    setDialogOpen(true);
  };

  const openEdit = (tb: Timebox): void => {
    setEditing(tb);
    setDialogOpen(true);
  };

  return (
    <section className={styles.page}>
      <header className={styles.header}>
        <div className={styles.headerText}>
          <h1 className={styles.title}>{t('timeboxes.title')}</h1>
          <p className={styles.subtitle}>{t('timeboxes.description')}</p>
        </div>
        <Button type="button" variant="primary" onClick={openCreate}>
          {t('timeboxes.new')}
        </Button>
      </header>

      {hasAny ? (
        <div className={styles.sections}>
          <Collapse open={active.length > 0}>
            <section className={styles.section} aria-label={t('timeboxes.section.active')}>
              <header className={styles.sectionHeader}>
                <h2 className={styles.sectionTitle}>{t('timeboxes.section.active')}</h2>
                <span className={styles.sectionCount}>{active.length}</span>
              </header>
              <TimeboxCardList
                timeboxes={active}
                workspaceId={workspaceId}
                locale={locale}
                onEdit={openEdit}
              />
            </section>
          </Collapse>

          <Collapse open={planned.length > 0}>
            <section className={styles.section} aria-label={t('timeboxes.section.planned')}>
              <header className={styles.sectionHeader}>
                <h2 className={styles.sectionTitle}>{t('timeboxes.section.planned')}</h2>
                <span className={styles.sectionCount}>{planned.length}</span>
              </header>
              <TimeboxCardList
                timeboxes={planned}
                workspaceId={workspaceId}
                locale={locale}
                onEdit={openEdit}
              />
            </section>
          </Collapse>

          <Collapse open={archive.length > 0}>
            <section className={styles.section} aria-label={t('timeboxes.section.archive')}>
              <button
                type="button"
                className={`${styles.archiveToggle} nf-focus-ring`}
                onClick={() => {
                  setArchiveOpen((v) => !v);
                }}
                aria-expanded={archiveOpen}
              >
                <ChevronRight size={14} aria-hidden className={styles.disclosureIcon} />
                <span>{t('timeboxes.section.archive')}</span>
                <span className={styles.sectionCount}>{archive.length}</span>
              </button>
              <Collapse open={archiveOpen} className={styles.archiveCollapse}>
                <div className={styles.archiveList}>
                  <TimeboxCardList
                    timeboxes={archive}
                    workspaceId={workspaceId}
                    locale={locale}
                    onEdit={openEdit}
                  />
                </div>
              </Collapse>
            </section>
          </Collapse>
        </div>
      ) : (
        <EmptyState
          className={styles.empty}
          title={t('timeboxes.empty')}
          description={t('timeboxes.description')}
          action={
            <Button type="button" variant="primary" onClick={openCreate}>
              {t('timeboxes.new')}
            </Button>
          }
        />
      )}

      <TimeboxDialog
        workspaceId={workspaceId}
        mode={editing ? 'edit' : 'create'}
        {...(editing ? { initial: editing } : {})}
        open={dialogOpen}
        onClose={() => {
          setDialogOpen(false);
        }}
      />
    </section>
  );
}
