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

import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Input from '@nodate-flow/ui/primitives/input';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { getRouteApi } from '@tanstack/react-router';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { confirmAction } from '../../lib/confirm-action';
import {
  type Timebox,
  type TimeboxStatus,
  type TimeboxTask,
  asTimeboxStatus,
  useAddTimeboxTaskMutation,
  useDeleteTimeboxMutation,
  useRemoveTimeboxTaskMutation,
  useTimeboxTasksQuery,
  useTimeboxesQuery,
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

  const handleRemove = async (taskId: string): Promise<void> => {
    try {
      await removeTask.mutateAsync({ wsId: workspaceId, timeboxId, taskId });
      toaster.show({ tone: 'success', message: t('timeboxes.remove_task.success') });
    } catch {
      toaster.show({ tone: 'danger', message: t('timeboxes.remove_task.error') });
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
    } catch {
      toaster.show({ tone: 'danger', message: t('timeboxes.add_task.error') });
    }
  };

  return (
    <div className={styles.tasks}>
      <h4 className={styles.tasksTitle}>{t('timeboxes.add_task.label')}</h4>
      {tasks.length === 0 ? (
        <p className={styles.tasksEmpty}>{t('timeboxes.no_tasks')}</p>
      ) : (
        tasks.map((task) => (
          <div key={task.id} className={styles.taskRow}>
            <span className={styles.taskTitle}>{task.title}</span>
            <Badge tone="neutral">{task.derivedState}</Badge>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={() => {
                void handleRemove(task.id);
              }}
            >
              {t('timeboxes.remove_task.label')}
            </Button>
          </div>
        ))
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
        <Button type="submit" variant="default" size="sm">
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
  const completedTasks = tasks.filter((tk) => tk.derivedState === 'completed').length;
  const progressPct = totalTasks > 0 ? Math.round((completedTasks / totalTasks) * 100) : 0;
  const [tasksOpen, setTasksOpen] = useState(status === 'active');

  const transition = async (next: TimeboxStatus, errorKey: string): Promise<void> => {
    try {
      await updateStatus.mutateAsync({
        wsId: workspaceId,
        timeboxId: timebox.id,
        status: next,
      });
    } catch {
      toaster.show({ tone: 'danger', message: t(errorKey) });
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
    } catch {
      toaster.show({ tone: 'danger', message: t('timeboxes.delete.error') });
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

  return (
    <article className={cardClass}>
      <div className={styles.cardTop}>
        <h3 className={styles.cardName}>{timebox.name}</h3>
        <Badge tone={toneForStatus(status)}>{t(`timeboxes.status.${status}`)}</Badge>
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
            <div className={styles.progressFill} style={{ inlineSize: `${progressPct}%` }} />
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
              {tasksOpen ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
              <span>{t('timeboxes.add_task.label')}</span>
            </Button>
          </>
        ) : null}
        {status === 'completed' || status === 'cancelled' ? (
          <Button
            type="button"
            size="sm"
            variant="danger"
            onClick={() => {
              void handleDelete();
            }}
          >
            {t('timeboxes.action.delete')}
          </Button>
        ) : null}
      </div>
      {showTasks && tasksOpen ? (
        <TaskList workspaceId={workspaceId} timeboxId={timebox.id} />
      ) : null}
    </article>
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
          {active.length > 0 ? (
            <section className={styles.section} aria-label={t('timeboxes.section.active')}>
              <header className={styles.sectionHeader}>
                <h2 className={styles.sectionTitle}>{t('timeboxes.section.active')}</h2>
                <span className={styles.sectionCount}>{active.length}</span>
              </header>
              {active.map((tb) => (
                <TimeboxCard
                  key={tb.id}
                  timebox={tb}
                  workspaceId={workspaceId}
                  locale={locale}
                  onEdit={openEdit}
                />
              ))}
            </section>
          ) : null}

          {planned.length > 0 ? (
            <section className={styles.section} aria-label={t('timeboxes.section.planned')}>
              <header className={styles.sectionHeader}>
                <h2 className={styles.sectionTitle}>{t('timeboxes.section.planned')}</h2>
                <span className={styles.sectionCount}>{planned.length}</span>
              </header>
              {planned.map((tb) => (
                <TimeboxCard
                  key={tb.id}
                  timebox={tb}
                  workspaceId={workspaceId}
                  locale={locale}
                  onEdit={openEdit}
                />
              ))}
            </section>
          ) : null}

          {archive.length > 0 ? (
            <section className={styles.section} aria-label={t('timeboxes.section.archive')}>
              <button
                type="button"
                className={`${styles.archiveToggle} nf-focus-ring`}
                onClick={() => {
                  setArchiveOpen((v) => !v);
                }}
                aria-expanded={archiveOpen}
              >
                {archiveOpen ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                <span>{t('timeboxes.section.archive')}</span>
                <span className={styles.sectionCount}>{archive.length}</span>
              </button>
              {archiveOpen
                ? archive.map((tb) => (
                    <TimeboxCard
                      key={tb.id}
                      timebox={tb}
                      workspaceId={workspaceId}
                      locale={locale}
                      onEdit={openEdit}
                    />
                  ))
                : null}
            </section>
          ) : null}
        </div>
      ) : (
        <div className={styles.empty}>
          <p className={styles.emptyTitle}>{t('timeboxes.empty')}</p>
          <p className={styles.emptyDescription}>{t('timeboxes.description')}</p>
        </div>
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
