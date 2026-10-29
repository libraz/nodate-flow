/**
 * Authenticated home (/). Dashboard with quick-access widgets:
 * - Greeting + branding
 * - Assigned task summary (from GET /me/tasks)
 * - Workspace quick links
 * - Quick actions (create task, search)
 */

import type { components } from '@nodate-flow/sdk';
import { useFirstLoadStagger } from '@nodate-flow/ui/hooks/use-first-load-stagger';
import Icon from '@nodate-flow/ui/icon';
import { cx } from '@nodate-flow/ui/lib/cx';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { useSuspenseQuery } from '@tanstack/react-query';
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
import { type ReactElement, Suspense, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { OPEN_COMMAND_PALETTE_EVENT } from '../components/layout/glass-dock';
import { selectUser, useAuth } from '../features/auth/auth-store';
import DashboardView from '../features/dashboard/dashboard-view';
import { useWorkspacesQuery } from '../features/workspaces/api';
import { apiRequest } from '../lib/api';
import { todayKey } from '../lib/date-utils';
import { useEffectiveZone } from '../lib/use-effective-timezone';
import styles from './_authenticated.index.module.css';

type AssignedTask = components['schemas']['MyTaskListItem'];

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
  const { data: tasks } = useSuspenseQuery({
    queryKey: ['me', 'tasks'] as const,
    staleTime: 30_000,
    queryFn: async (): Promise<AssignedTask[]> => {
      const data = await apiRequest(
        (client) => client.GET('/me/tasks', { params: { query: { limit: 200, offset: 0 } } }),
        'Failed to load assigned tasks',
      );
      return data.tasks ?? [];
    },
  });

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

      {recentTasks.length > 0 && (
        <section>
          <h2 className={styles.sectionTitle}>{t('home.recent_tasks')}</h2>
          <ul className={styles.taskList}>
            {recentTasks.map((task, index) => (
              <li key={task.id} className={motion.enter} style={stagger(index)}>
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

function WorkspaceLinks(): ReactElement {
  const { t } = useTranslation('common');
  const { data: workspaces } = useWorkspacesQuery();

  if (workspaces.length === 0) {
    return (
      <div className={styles.workspacesEmpty}>
        <p className={styles.mutedText}>{t('workspaces.empty')}</p>
        <Link to="/setup" className={cx(styles.action, styles.actionPrimary, 'nf-focus-ring')}>
          <Icon icon={Plus} decorative />
          {t('workspaces.setup.submit')}
        </Link>
      </div>
    );
  }

  return (
    <div className={styles.workspaces}>
      {workspaces.map((ws) => (
        <Link
          key={ws.id}
          to="/workspaces/$id"
          params={{ id: ws.id }}
          className={cx(styles.workspaceLink, 'nf-focus-ring')}
        >
          <Icon icon={FolderKanban} decorative />
          <span className={styles.workspaceName}>{ws.name}</span>
        </Link>
      ))}
    </div>
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
  const firstWs = workspaces[0];
  if (!firstWs) return null;
  return <DashboardView workspaceId={firstWs.id} />;
}

/* ── Main dashboard ────────────────────────────────────────── */

function HomePage(): ReactElement {
  const { t } = useTranslation('common');
  const user = useAuth(selectUser);
  const greeting = user?.displayName
    ? t('home.greeting_name', { name: user.displayName })
    : t('landing.hello');

  // Both buttons open the shared command palette; the layout listens for the event.
  const openPalette = (): void => {
    window.dispatchEvent(new Event(OPEN_COMMAND_PALETTE_EVENT));
  };

  return (
    <section className={styles.page}>
      <header className={styles.header}>
        <h1 className={styles.greeting}>{greeting}</h1>
        <p className={styles.tagline}>{t('landing.tagline')}</p>
      </header>

      <div className={styles.quickActions}>
        <button
          type="button"
          onClick={openPalette}
          className={cx(styles.action, styles.actionPrimary, 'nf-focus-ring')}
        >
          <Icon icon={Plus} decorative />
          {t('home.quick_create')}
        </button>
        <button type="button" onClick={openPalette} className={cx(styles.action, 'nf-focus-ring')}>
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

      <section>
        <h2 className={styles.sectionTitle}>{t('nav.workspaces')}</h2>
        <Suspense fallback={<WorkspaceLinksSkeleton />}>
          <WorkspaceLinks />
        </Suspense>
      </section>

      <Suspense fallback={<Skeleton className={styles.dashboardSkeleton} />}>
        <HomeDashboard />
      </Suspense>
    </section>
  );
}

export const Route = createFileRoute('/_authenticated/')({
  component: HomePage,
});
