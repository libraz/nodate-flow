/**
 * Authenticated home (/): greeting with the New task action, a count
 * strip that links into My tasks, the first open assigned tasks, workspace
 * links when there is more than one, and the selected workspace's widgets.
 */

import { useFirstLoadStagger } from '@nodate-flow/ui/hooks/use-first-load-stagger';
import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Icon from '@nodate-flow/ui/icon';
import { cx } from '@nodate-flow/ui/lib/cx';
import Badge from '@nodate-flow/ui/primitives/badge';
import Button, { buttonClassName } from '@nodate-flow/ui/primitives/button';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { createFileRoute, Link } from '@tanstack/react-router';
import { FolderKanban, Plus } from 'lucide-react';
import { type CSSProperties, type ReactElement, Suspense, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { OPEN_CREATE_TASK_EVENT } from '../components/layout/glass-dock';
import PageFrame from '../components/page-frame';
import PageHeader from '../components/page-header';
import { selectUser, useAuth } from '../features/auth/auth-store';
import DashboardView from '../features/dashboard/dashboard-view';
import OnboardingChecklist from '../features/onboarding/onboarding-checklist';
import {
  type MyTaskListItem,
  type TaskDerivedState,
  type TaskPriority,
  useMyTasksQuery,
} from '../features/tasks/api';
import CompleteTaskButton from '../features/tasks/complete-task-button';
import { PRIORITY_KEY, PRIORITY_TONE, STATE_COLOR } from '../features/tasks/constants';
import rowStyles from '../features/tasks/task-row.module.css';
import {
  DEFAULT_MY_TASK_FILTERS,
  resetMyTaskFilters,
  setMyTaskFilters,
} from '../features/tasks/use-my-task-filters';
import { useWorkspacesQuery } from '../features/workspaces/api';
import { todayKey } from '../lib/date-utils';
import { formatDueDate } from '../lib/format-date';
import { useCurrentWorkspaceId } from '../lib/use-current-workspace';
import { useEffectiveZone } from '../lib/use-effective-timezone';
import styles from './_authenticated.index.module.css';

/* ── Count strip ──────────────────────────────────────────── */

/**
 * One count in the strip; a link into My tasks. `onSelect` sets the My tasks
 * filters the count corresponds to before the navigation lands.
 */
function StatLink({
  label,
  value,
  danger,
  onSelect,
}: {
  label: string;
  value: number;
  danger?: boolean;
  onSelect: () => void;
}): ReactElement {
  return (
    <Link to="/today" className={cx(styles.stat, 'nf-focus-ring')} onClick={onSelect}>
      <span className={cx(styles.statValue, danger && value > 0 && styles.statValueDanger)}>
        {value}
      </span>
      <span className={styles.statLabel}>{label}</span>
    </Link>
  );
}

/** Reserves the count strip while the summary loads. */
function TaskSummarySkeleton(): ReactElement {
  return <Skeleton className={styles.statSkeleton} aria-busy="true" />;
}

/* ── Task summary (suspense boundary) ──────────────────────── */

/** The home summary's rows; a completed row collapses out so the rows below close the gap. */
function RecentTaskList({
  tasks,
  today,
  stagger,
}: {
  tasks: MyTaskListItem[];
  /** `YYYY-MM-DD` in the effective zone; a due date before it is overdue. */
  today: string;
  stagger: (index: number) => CSSProperties | undefined;
}): ReactElement {
  const { t, i18n } = useTranslation('common');
  const locale = i18n.resolvedLanguage ?? 'en';
  const { entries, onExited } = usePresenceList(tasks, (task) => task.id);

  return (
    <ul className={cx(rowStyles.list, styles.recentList)}>
      {entries.map(({ key, item: task, present }, index) => {
        const pri = (task.priority ?? 0) as TaskPriority;
        const overdue = task.dueOn !== undefined && task.dueOn < today;
        return (
          <PresenceItem key={key} as="li" present={present} onExited={() => onExited(key)}>
            <div
              className={cx(motion.enter, rowStyles.item)}
              style={
                {
                  ...stagger(index),
                  '--state-tone': STATE_COLOR[task.derivedState as TaskDerivedState],
                } as CSSProperties
              }
            >
              {task.actorRole === 'assignee' ? (
                <CompleteTaskButton
                  task={{
                    id: task.id,
                    title: task.title,
                    derivedState: task.derivedState as TaskDerivedState,
                    projectId: task.projectId,
                  }}
                />
              ) : (
                <span aria-hidden="true" />
              )}
              <Link
                to="/tasks/$taskId"
                params={{ taskId: task.id }}
                className={cx(rowStyles.row, 'nf-focus-ring')}
              >
                <span className={rowStyles.taskTitle} title={task.title}>
                  {task.title}
                </span>
                {pri > 0 ? <Badge tone={PRIORITY_TONE[pri]}>{t(PRIORITY_KEY[pri])}</Badge> : null}
                {task.projectName ? (
                  <span className={cx(rowStyles.context, rowStyles.contextHideNarrow)}>
                    {task.projectName}
                  </span>
                ) : null}
                {task.dueOn ? (
                  <span className={cx(rowStyles.due, overdue && styles.dueOverdue)}>
                    {formatDueDate(task.dueOn, locale)}
                  </span>
                ) : null}
              </Link>
            </div>
          </PresenceItem>
        );
      })}
    </ul>
  );
}

function TaskSummary(): ReactElement {
  const { t } = useTranslation('common');
  const stagger = useFirstLoadStagger();
  // "Overdue" and "due today" are calendar-day questions, so they are
  // read in the effective zone — the same one the calendar files the
  // same tasks under. Read from the browser, the two counts disagreed
  // with the grid for anyone whose profile zone is on the other side of
  // midnight.
  const zone = useEffectiveZone();
  const { data: tasks } = useMyTasksQuery();

  const today = todayKey(zone);
  const counts = useMemo(() => {
    let open = 0;
    let overdue = 0;
    let dueToday = 0;
    let done = 0;
    for (const t of tasks) {
      if (t.derivedState === 'done') {
        done++;
        continue;
      }
      if (t.derivedState === 'cancelled') continue;
      open++;
      if (t.dueOn && t.dueOn < today) overdue++;
      if (t.dueOn === today) dueToday++;
    }
    return { open, overdue, dueToday, done };
  }, [tasks, today]);

  const recentTasks = useMemo(() => {
    return tasks
      .filter((t) => t.derivedState !== 'done' && t.derivedState !== 'cancelled')
      .slice(0, 5);
  }, [tasks]);

  return (
    <>
      <nav className={cx(styles.stats, styles.statsEnter)} aria-label={t('home.stats_label')}>
        <StatLink label={t('home.stat_open')} value={counts.open} onSelect={resetMyTaskFilters} />
        <StatLink
          label={t('home.stat_overdue')}
          value={counts.overdue}
          danger
          onSelect={resetMyTaskFilters}
        />
        <StatLink
          label={t('home.stat_due_today')}
          value={counts.dueToday}
          onSelect={resetMyTaskFilters}
        />
        <StatLink
          label={t('home.stat_done')}
          value={counts.done}
          onSelect={() => {
            setMyTaskFilters({ ...DEFAULT_MY_TASK_FILTERS, states: ['done'] });
          }}
        />
      </nav>

      {recentTasks.length === 0 ? (
        <section>
          <h2 className={styles.sectionTitle}>{t('home.recent_tasks')}</h2>
          <p className={styles.mutedText}>{t('home.recent_tasks_empty')}</p>
        </section>
      ) : (
        <section>
          <h2 className={styles.sectionTitle}>{t('home.recent_tasks')}</h2>
          <RecentTaskList tasks={recentTasks} today={today} stagger={stagger} />
          <Link to="/today" className={cx(styles.viewAll, 'nf-focus-ring')}>
            {t('home.view_all_tasks')}
          </Link>
        </section>
      )}
    </>
  );
}

/* ── Workspace quick links ─────────────────────────────────── */

function WorkspaceLinks(): ReactElement | null {
  const { t } = useTranslation('common');
  const { data: workspaces } = useWorkspacesQuery();

  // With no workspace the checklist and the sidebar already point at /setup;
  // with one, the sidebar's workspace switcher is the same link.
  if (workspaces.length < 2) return null;

  return (
    <section>
      <h2 className={styles.sectionTitle}>{t('nav.workspaces')}</h2>
      <div className={styles.workspaces}>
        {workspaces.map((ws) => (
          <Link
            key={ws.id}
            to="/workspaces/$id/projects"
            params={{ id: ws.id }}
            className={buttonClassName({ className: styles.workspaceLink })}
          >
            <Icon icon={FolderKanban} decorative />
            <span className={styles.workspaceName}>{ws.name}</span>
          </Link>
        ))}
      </div>
    </section>
  );
}

/* ── Dashboard widgets (workspace-scoped) ─────────────────── */

function HomeDashboard(): ReactElement | null {
  const { data: workspaces } = useWorkspacesQuery();
  const currentWsId = useCurrentWorkspaceId();
  // Fall back to the first workspace when there is no current id or it is not listed.
  const ws = workspaces.find((w) => w.id === currentWsId) ?? workspaces[0];
  if (!ws) return null;
  return <DashboardView workspaceId={ws.id} workspaceName={ws.name} />;
}

/* ── Main dashboard ────────────────────────────────────────── */

function HomePage(): ReactElement {
  const { t } = useTranslation('common');
  const user = useAuth(selectUser);
  const greeting = user?.displayName
    ? t('home.greeting_name', { name: user.displayName })
    : t('landing.hello');

  // The authenticated layout listens for this event.
  const handleCreateTask = (): void => {
    window.dispatchEvent(new Event(OPEN_CREATE_TASK_EVENT));
  };

  return (
    <PageFrame>
      <PageHeader
        title={greeting}
        help="home"
        actions={
          <Button type="button" variant="primary" onClick={handleCreateTask}>
            <Icon icon={Plus} decorative />
            {t('home.quick_create')}
          </Button>
        }
      />

      <Suspense fallback={null}>
        <OnboardingChecklist />
      </Suspense>

      <Suspense fallback={<TaskSummarySkeleton />}>
        <TaskSummary />
      </Suspense>

      <Suspense fallback={null}>
        <WorkspaceLinks />
      </Suspense>

      <Suspense fallback={<Skeleton className={styles.dashboardSkeleton} />}>
        <HomeDashboard />
      </Suspense>
    </PageFrame>
  );
}

export const Route = createFileRoute('/_authenticated/')({
  component: HomePage,
});
