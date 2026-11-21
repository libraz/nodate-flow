/**
 * Authenticated home (/). Dashboard with quick-access widgets:
 * - Greeting + branding
 * - Assigned task summary (from GET /me/tasks)
 * - Workspace quick links
 * - Quick actions (create task, search)
 */

import { useFirstLoadStagger } from '@nodate-flow/ui/hooks/use-first-load-stagger';
import Icon from '@nodate-flow/ui/icon';
import { cx } from '@nodate-flow/ui/lib/cx';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { createFileRoute, Link } from '@tanstack/react-router';
import {
  CalendarDays,
  CheckCircle2,
  Clock,
  FolderKanban,
  Inbox,
  type LucideIcon,
  Plus,
  Search,
} from 'lucide-react';
import { type CSSProperties, type ReactElement, Suspense, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { OPEN_COMMAND_PALETTE_EVENT } from '../components/layout/command-palette-store';
import { OPEN_CREATE_TASK_EVENT } from '../components/layout/glass-dock';
import PageFrame from '../components/page-frame';
import PageHeader from '../components/page-header';
import { selectUser, useAuth } from '../features/auth/auth-store';
import DashboardView from '../features/dashboard/dashboard-view';
import OnboardingChecklist from '../features/onboarding/onboarding-checklist';
import { type TaskDerivedState, useMyTasksQuery } from '../features/tasks/api';
import CompleteTaskButton from '../features/tasks/complete-task-button';
import { STATE_COLOR } from '../features/tasks/constants';
import { useWorkspacesQuery } from '../features/workspaces/api';
import { todayKey } from '../lib/date-utils';
import { useCurrentWorkspaceId } from '../lib/use-current-workspace';
import { useEffectiveZone } from '../lib/use-effective-timezone';
import styles from './_authenticated.index.module.css';

/* ── Stat card ─────────────────────────────────────────────── */

type StatTone = 'accent' | 'danger' | 'warning' | 'success';

const STAT_TONE_CLASS: Record<StatTone, string | undefined> = {
  accent: undefined,
  danger: styles.statIconDanger,
  warning: styles.statIconWarning,
  success: styles.statIconSuccess,
};

function StatCard({
  icon,
  label,
  value,
  tone,
}: {
  icon: LucideIcon;
  label: string;
  value: number;
  tone: StatTone;
}): ReactElement {
  return (
    <div className={styles.stat}>
      <div className={cx(styles.statIcon, STAT_TONE_CLASS[tone])}>
        <Icon icon={icon} decorative />
      </div>
      <div className={styles.statText}>
        <div className={styles.statValue}>{value}</div>
        <div className={styles.statLabel}>{label}</div>
      </div>
    </div>
  );
}

/** Reserves the stat row and the list heading while the summary loads. */
function TaskSummarySkeleton(): ReactElement {
  return (
    <div className={styles.stats} aria-busy="true">
      <Skeleton className={styles.statSkeleton} />
      <Skeleton className={styles.statSkeleton} />
      <Skeleton className={styles.statSkeleton} />
      <Skeleton className={styles.statSkeleton} />
    </div>
  );
}

/* ── Task summary (suspense boundary) ──────────────────────── */

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

  // The authenticated layout listens for this event.
  const handleCreateTask = (): void => {
    window.dispatchEvent(new Event(OPEN_CREATE_TASK_EVENT));
  };

  const counts = useMemo(() => {
    const today = todayKey(zone);
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
  }, [tasks, zone]);

  const recentTasks = useMemo(() => {
    return tasks
      .filter((t) => t.derivedState !== 'done' && t.derivedState !== 'cancelled')
      .slice(0, 5);
  }, [tasks]);

  return (
    <>
      <div className={cx(styles.stats, styles.statsEnter)}>
        <StatCard
          icon={FolderKanban}
          label={t('home.stat_open')}
          value={counts.open}
          tone="accent"
        />
        <StatCard
          icon={Clock}
          label={t('home.stat_overdue')}
          value={counts.overdue}
          tone="danger"
        />
        <StatCard
          icon={CalendarDays}
          label={t('home.stat_due_today')}
          value={counts.dueToday}
          tone="warning"
        />
        <StatCard
          icon={CheckCircle2}
          label={t('home.stat_done')}
          value={counts.done}
          tone="success"
        />
      </div>
      <p className={styles.statHint}>{t('home.stat_hint')}</p>

      {recentTasks.length === 0 ? (
        <section>
          <h2 className={styles.sectionTitle}>{t('home.recent_tasks')}</h2>
          <div className={styles.emptyTasks}>
            <p className={styles.mutedText}>{t('home.recent_tasks_empty')}</p>
            <button
              type="button"
              onClick={handleCreateTask}
              className={cx(styles.action, 'nf-focus-ring')}
            >
              <Icon icon={Plus} decorative />
              {t('home.quick_create')}
            </button>
          </div>
        </section>
      ) : (
        <section>
          <h2 className={styles.sectionTitle}>{t('home.recent_tasks')}</h2>
          <ul className={styles.taskList}>
            {recentTasks.map((task, index) => (
              <li
                key={task.id}
                className={cx(motion.enter, styles.taskItem)}
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
                  className={cx(styles.taskRow, 'nf-focus-ring')}
                >
                  <span className={styles.taskTitle}>{task.title}</span>
                  {task.projectName ? (
                    <span className={styles.taskProject}>{task.projectName}</span>
                  ) : null}
                </Link>
              </li>
            ))}
          </ul>
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

  // With no workspace the checklist and the sidebar already point at /setup.
  if (workspaces.length === 0) return null;

  return (
    <section>
      <h2 className={styles.sectionTitle}>{t('nav.workspaces')}</h2>
      <div className={styles.workspaces}>
        {workspaces.map((ws) => (
          <Link
            key={ws.id}
            to="/workspaces/$id/projects"
            params={{ id: ws.id }}
            className={cx(styles.workspaceLink, 'nf-focus-ring')}
          >
            <Icon icon={FolderKanban} decorative />
            <span className={styles.workspaceName}>{ws.name}</span>
          </Link>
        ))}
      </div>
    </section>
  );
}

/** Reserves one row of workspace chips while the list loads. */
function WorkspaceLinksSkeleton(): ReactElement {
  return (
    <div className={styles.workspaces} aria-busy="true">
      <Skeleton className={styles.workspaceSkeleton} />
      <Skeleton className={styles.workspaceSkeleton} />
    </div>
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

  // The authenticated layout listens for both events.
  const handleCreateTask = (): void => {
    window.dispatchEvent(new Event(OPEN_CREATE_TASK_EVENT));
  };
  const handleSearch = (): void => {
    window.dispatchEvent(new Event(OPEN_COMMAND_PALETTE_EVENT));
  };

  return (
    <PageFrame>
      <PageHeader title={greeting} description={t('home.description')} help="home" />

      <Suspense fallback={null}>
        <OnboardingChecklist />
      </Suspense>

      <div className={styles.quickActions}>
        <button
          type="button"
          onClick={handleCreateTask}
          className={cx(styles.action, styles.actionPrimary, 'nf-focus-ring')}
        >
          <Icon icon={Plus} decorative />
          {t('home.quick_create')}
        </button>
        <button type="button" onClick={handleSearch} className={cx(styles.action, 'nf-focus-ring')}>
          <Icon icon={Search} decorative />
          {t('home.quick_search')}
        </button>
        <Link to="/inbox" className={cx(styles.action, 'nf-focus-ring')}>
          <Icon icon={Inbox} decorative />
          {t('nav.inbox')}
        </Link>
      </div>

      <Suspense fallback={<TaskSummarySkeleton />}>
        <TaskSummary />
      </Suspense>

      <Suspense fallback={<WorkspaceLinksSkeleton />}>
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
