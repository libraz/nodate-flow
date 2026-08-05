/**
 * Authenticated home (/). Dashboard with quick-access widgets:
 * - Greeting + branding
 * - Assigned task summary (from GET /me/tasks)
 * - Workspace quick links
 * - Quick actions (create task, search)
 */

import type { components } from '@nodate-flow/sdk';
import Icon from '@nodate-flow/ui/icon';
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
import { sdk } from '../lib/sdk';

type AssignedTask = components['schemas']['MyTaskListItem'];

/* ── Stat card ─────────────────────────────────────────────── */

function StatCard({
  icon,
  label,
  value,
  accent,
}: {
  icon: LucideIcon;
  label: string;
  value: number;
  accent?: string;
}): ReactElement {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 'var(--nf-space-3)',
        padding: 'var(--nf-space-4) var(--nf-space-5)',
        borderRadius: 'var(--nf-radius-lg)',
        background: 'var(--nf-color-surface)',
        border: '1px solid var(--nf-color-border)',
        flex: '1 1 0',
        // nf-token-override: component dimension, not a spacing step
        minWidth: '10rem',
      }}
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          // nf-token-override: component dimension, not a spacing step
          width: '2.5rem',
          // nf-token-override: component dimension, not a spacing step
          height: '2.5rem',
          borderRadius: 'var(--nf-radius-md)',
          background: accent ?? 'var(--nf-color-accent)',
          color: 'white',
          flexShrink: 0,
        }}
      >
        <Icon icon={icon} decorative />
      </div>
      <div>
        <div style={{ fontSize: 'var(--nf-text-2xl)', fontWeight: 700, lineHeight: 1.1 }}>
          {value}
        </div>
        <div
          style={{
            fontSize: 'var(--nf-text-xs)',
            color: 'var(--nf-color-fg-muted)',
            marginTop: 'var(--nf-space-0-5)',
          }}
        >
          {label}
        </div>
      </div>
    </div>
  );
}

/* ── Task summary (suspense boundary) ──────────────────────── */

function TaskSummary(): ReactElement {
  const { t } = useTranslation('common');
  const { data: tasks } = useSuspenseQuery({
    queryKey: ['me', 'tasks'] as const,
    staleTime: 30_000,
    queryFn: async (): Promise<AssignedTask[]> => {
      const { data, error } = await sdk.GET('/me/tasks', {
        params: { query: { limit: 200, offset: 0 } },
      });
      if (error || !data) return [];
      return data.tasks ?? [];
    },
  });

  const counts = useMemo(() => {
    const now = new Date();
    const todayKey = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`;
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
      if (t.dueOn && t.dueOn < todayKey) overdue++;
      if (t.dueOn === todayKey) dueToday++;
    }
    return { open, overdue, dueToday, done };
  }, [tasks]);

  const recentTasks = useMemo(() => {
    return tasks
      .filter((t) => t.derivedState !== 'done' && t.derivedState !== 'cancelled')
      .slice(0, 5);
  }, [tasks]);

  return (
    <>
      <div style={{ display: 'flex', gap: 'var(--nf-space-3)', flexWrap: 'wrap' }}>
        <StatCard
          icon={FolderKanban}
          label={t('home.stat_open')}
          value={counts.open}
          accent="var(--nf-color-accent)"
        />
        <StatCard
          icon={Clock}
          label={t('home.stat_overdue')}
          value={counts.overdue}
          accent="var(--nf-color-danger)"
        />
        <StatCard
          icon={CalendarDays}
          label={t('home.stat_due_today')}
          value={counts.dueToday}
          accent="var(--nf-color-warning)"
        />
        <StatCard
          icon={CheckCircle2}
          label={t('home.stat_done')}
          value={counts.done}
          accent="var(--nf-color-success)"
        />
      </div>

      {recentTasks.length > 0 && (
        <section>
          <h2
            style={{
              margin: '0 0 var(--nf-space-3)',
              fontSize: 'var(--nf-text-supporting)',
              textTransform: 'uppercase',
              letterSpacing: '0.05em',
              color: 'var(--nf-color-fg-muted)',
            }}
          >
            {t('home.recent_tasks')}
          </h2>
          <ul
            style={{
              listStyle: 'none',
              margin: 0,
              padding: 0,
              display: 'flex',
              flexDirection: 'column',
              gap: 'var(--nf-space-1)',
            }}
          >
            {recentTasks.map((task) => (
              <li
                key={task.id}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 'var(--nf-space-3)',
                  padding: 'var(--nf-space-2-5) var(--nf-space-3)',
                  borderRadius: 'var(--nf-radius-md)',
                  background: 'var(--nf-color-surface)',
                }}
              >
                <Link
                  to="/tasks/$taskId"
                  params={{ taskId: task.id }}
                  style={{
                    flex: 1,
                    minWidth: 0,
                    color: 'inherit',
                    textDecoration: 'none',
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                  }}
                >
                  {task.title}
                </Link>
                <span
                  style={{
                    fontSize: 'var(--nf-text-xs)',
                    color: 'var(--nf-color-fg-muted)',
                    whiteSpace: 'nowrap',
                  }}
                >
                  {task.projectName ?? ''}
                </span>
              </li>
            ))}
          </ul>
          <div style={{ marginTop: 'var(--nf-space-2)' }}>
            <Link
              to="/today"
              style={{
                fontSize: 'var(--nf-text-supporting)',
                color: 'var(--nf-color-accent)',
                textDecoration: 'none',
              }}
            >
              {t('home.view_all_tasks')}
            </Link>
          </div>
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
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'flex-start',
          gap: 'var(--nf-space-3)',
        }}
      >
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: 'var(--nf-text-sm)' }}>
          {t('workspaces.empty')}
        </p>
        <Link
          to="/setup"
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: 'var(--nf-space-2)',
            padding: 'var(--nf-space-2) var(--nf-space-3-5)',
            borderRadius: 'var(--nf-radius-md)',
            background: 'var(--nf-color-accent)',
            color: 'var(--nf-color-fg-on-accent)',
            textDecoration: 'none',
            fontSize: 'var(--nf-text-sm)',
            fontWeight: 600,
          }}
        >
          <Icon icon={Plus} decorative />
          {t('workspaces.setup.submit')}
        </Link>
      </div>
    );
  }

  return (
    <div style={{ display: 'flex', gap: 'var(--nf-space-2)', flexWrap: 'wrap' }}>
      {workspaces.map((ws) => (
        <Link
          key={ws.id}
          to="/workspaces/$id"
          params={{ id: ws.id }}
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: 'var(--nf-space-2)',
            padding: 'var(--nf-space-2) var(--nf-space-4)',
            borderRadius: 'var(--nf-radius-md)',
            background: 'var(--nf-color-surface)',
            border: '1px solid var(--nf-color-border)',
            color: 'inherit',
            textDecoration: 'none',
            fontSize: 'var(--nf-text-sm)',
          }}
        >
          <Icon icon={FolderKanban} decorative />
          {ws.name}
        </Link>
      ))}
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

  return (
    <section
      style={{
        minBlockSize: '100%',
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-8)',
        paddingBlock: 'var(--nf-space-8)',
        // nf-token-override: upper bound of a fluid range; the gutter stops widening here so the dashboard columns keep their measure
        paddingInline: 'clamp(var(--nf-space-6), 6vw, 3.5rem)',
        // nf-token-override: component dimension, not a spacing step
        maxInlineSize: '72rem',
        marginInline: 'auto',
        inlineSize: '100%',
      }}
    >
      {/* Header */}
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-2)' }}>
        <h1
          style={{
            fontFamily: 'var(--nf-font-display)',
            fontOpticalSizing: 'auto',
            fontWeight: 400,
            // nf-token-override: the dashboard greeting is the one heading set in the display face over the wordmark gradient, and it is sized against that gradient rather than against the page-title step
            fontSize: 'clamp(1.75rem, 4vw, 2.5rem)',
            lineHeight: 1.1,
            letterSpacing: '-0.02em',
            margin: 0,
            backgroundImage: 'var(--nf-gradient-wordmark)',
            backgroundClip: 'text',
            // biome-ignore lint/style/useNamingConvention: React PascalCase for vendor-prefixed CSS
            WebkitBackgroundClip: 'text',
            color: 'transparent',
            // biome-ignore lint/style/useNamingConvention: React PascalCase for vendor-prefixed CSS
            WebkitTextFillColor: 'transparent',
          }}
        >
          {greeting}
        </h1>
        <p
          style={{
            fontFamily: 'var(--nf-font-mono)',
            fontSize: 'var(--nf-text-micro)',
            letterSpacing: '0.18em',
            color: 'var(--nf-color-fg-muted)',
            margin: 0,
          }}
        >
          {t('landing.tagline')}
        </p>
      </header>

      {/* Quick actions */}
      <div style={{ display: 'flex', gap: 'var(--nf-space-2)', flexWrap: 'wrap' }}>
        <button
          type="button"
          onClick={() => window.dispatchEvent(new Event(OPEN_COMMAND_PALETTE_EVENT))}
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: 'var(--nf-space-2)',
            padding: 'var(--nf-space-2) var(--nf-space-4)',
            borderRadius: 'var(--nf-radius-md)',
            background: 'var(--nf-color-accent)',
            color: 'white',
            border: 'none',
            cursor: 'pointer',
            fontSize: 'var(--nf-text-sm)',
            fontWeight: 500,
          }}
        >
          <Icon icon={Plus} decorative />
          {t('home.quick_create')}
        </button>
        <button
          type="button"
          onClick={() => {
            const btn = document.querySelector<HTMLButtonElement>('[data-search-trigger]');
            btn?.click();
          }}
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: 'var(--nf-space-2)',
            padding: 'var(--nf-space-2) var(--nf-space-4)',
            borderRadius: 'var(--nf-radius-md)',
            background: 'transparent',
            color: 'var(--nf-color-fg)',
            border: '1px solid var(--nf-color-border)',
            cursor: 'pointer',
            fontSize: 'var(--nf-text-sm)',
          }}
        >
          <Icon icon={Search} decorative />
          {t('home.quick_search')}
        </button>
        <Link
          to="/inbox"
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: 'var(--nf-space-2)',
            padding: 'var(--nf-space-2) var(--nf-space-4)',
            borderRadius: 'var(--nf-radius-md)',
            background: 'transparent',
            color: 'var(--nf-color-fg)',
            border: '1px solid var(--nf-color-border)',
            textDecoration: 'none',
            fontSize: 'var(--nf-text-sm)',
          }}
        >
          <Icon icon={Inbox} decorative />
          {t('nav.inbox')}
        </Link>
      </div>

      {/* Task summary */}
      <Suspense
        fallback={
          <div
            style={{
              padding: 'var(--nf-space-8)',
              textAlign: 'center',
              color: 'var(--nf-color-fg-muted)',
            }}
          >
            {t('common.loading')}
          </div>
        }
      >
        <TaskSummary />
      </Suspense>

      {/* Workspaces */}
      <section>
        <h2
          style={{
            margin: '0 0 var(--nf-space-3)',
            fontSize: 'var(--nf-text-supporting)',
            textTransform: 'uppercase',
            letterSpacing: '0.05em',
            color: 'var(--nf-color-fg-muted)',
          }}
        >
          {t('nav.workspaces')}
        </h2>
        <Suspense fallback={null}>
          <WorkspaceLinks />
        </Suspense>
      </section>

      {/* Dashboard widgets */}
      <Suspense fallback={null}>
        <HomeDashboard />
      </Suspense>
    </section>
  );
}

export const Route = createFileRoute('/_authenticated/')({
  component: HomePage,
});
