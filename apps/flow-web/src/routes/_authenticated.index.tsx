/**
 * Authenticated home (/). Dashboard with quick-access widgets:
 * - Greeting + branding
 * - Assigned task summary (from GET /me/tasks)
 * - Workspace quick links
 * - Quick actions (create task, search)
 * - Theme / language switchers
 */

import type { components } from '@nodate-flow/sdk';
import { useSuspenseQuery } from '@tanstack/react-query';
import { Link, createFileRoute } from '@tanstack/react-router';
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

import Icon from '@nodate-flow/ui/icon';
import { OPEN_COMMAND_PALETTE_EVENT } from '../components/layout/glass-dock';
import { selectUser, useAuth } from '../features/auth/auth-store';
import DashboardView from '../features/dashboard/dashboard-view';
import { useWorkspacesQuery } from '../features/workspaces/api';
import { type SupportedLanguage, setLanguage, supportedLanguages } from '../i18n';
import { sdk } from '../lib/sdk';
import {
  type ThemePreference,
  type concreteThemes,
  themePreferences,
  useTheme,
} from '../providers/theme-provider';

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
        gap: '0.75rem',
        padding: '1rem 1.25rem',
        borderRadius: '0.75rem',
        background: 'var(--nf-color-surface))',
        border: '1px solid var(--nf-color-border, var(--nf-color-hairline))',
        flex: '1 1 0',
        minWidth: '10rem',
      }}
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          width: '2.5rem',
          height: '2.5rem',
          borderRadius: '0.5rem',
          background: accent ?? 'var(--nf-color-accent, var(--nf-color-accent))',
          color: 'white',
          flexShrink: 0,
        }}
      >
        <Icon icon={icon} decorative />
      </div>
      <div>
        <div style={{ fontSize: '1.5rem', fontWeight: 700, lineHeight: 1.1 }}>{value}</div>
        <div
          style={{ fontSize: '0.75rem', color: 'var(--nf-color-fg-muted)', marginTop: '0.125rem' }}
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
      <div style={{ display: 'flex', gap: '0.75rem', flexWrap: 'wrap' }}>
        <StatCard
          icon={FolderKanban}
          label={t('home.stat_open')}
          value={counts.open}
          accent="var(--nf-color-accent, #3498db)"
        />
        <StatCard
          icon={Clock}
          label={t('home.stat_overdue')}
          value={counts.overdue}
          accent="var(--nf-color-danger, #c0392b)"
        />
        <StatCard
          icon={CalendarDays}
          label={t('home.stat_due_today')}
          value={counts.dueToday}
          accent="var(--nf-color-warning, #e67e22)"
        />
        <StatCard
          icon={CheckCircle2}
          label={t('home.stat_done')}
          value={counts.done}
          accent="var(--nf-color-success, #27ae60)"
        />
      </div>

      {recentTasks.length > 0 && (
        <section>
          <h2
            style={{
              margin: '0 0 0.75rem',
              fontSize: '0.85rem',
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
              gap: '0.25rem',
            }}
          >
            {recentTasks.map((task) => (
              <li
                key={task.id}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: '0.75rem',
                  padding: '0.6rem 0.75rem',
                  borderRadius: '0.5rem',
                  background: 'var(--nf-color-surface))',
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
                    fontSize: '0.75rem',
                    color: 'var(--nf-color-fg-muted)',
                    whiteSpace: 'nowrap',
                  }}
                >
                  {task.projectName ?? ''}
                </span>
              </li>
            ))}
          </ul>
          <div style={{ marginTop: '0.5rem' }}>
            <Link
              to="/today"
              style={{
                fontSize: '0.8125rem',
                color: 'var(--nf-color-accent, var(--nf-color-accent))',
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
      <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: '0.875rem' }}>
        {t('workspaces.empty')}
      </p>
    );
  }

  return (
    <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap' }}>
      {workspaces.map((ws) => (
        <Link
          key={ws.id}
          to="/workspaces/$id"
          params={{ id: ws.id }}
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: '0.5rem',
            padding: '0.5rem 1rem',
            borderRadius: '0.5rem',
            background: 'var(--nf-color-surface))',
            border: '1px solid var(--nf-color-border, var(--nf-color-hairline))',
            color: 'inherit',
            textDecoration: 'none',
            fontSize: '0.875rem',
          }}
        >
          <Icon icon={FolderKanban} decorative />
          {ws.name}
        </Link>
      ))}
    </div>
  );
}

/* ── Theme / language switchers (kept from original) ───────── */

function LanguageSwitcher(): ReactElement {
  const { t, i18n } = useTranslation('common');
  const current = (i18n.resolvedLanguage ?? 'en') as SupportedLanguage;
  return (
    <fieldset
      style={{
        border: '1px solid var(--nf-color-hairline)',
        borderRadius: '999px',
        padding: '0.125rem',
        display: 'inline-flex',
        gap: '0.125rem',
      }}
    >
      <legend
        style={{
          fontFamily: 'var(--font-mono)',
          fontSize: '0.625rem',
          letterSpacing: '0.08em',
          color: 'var(--nf-color-fg-muted)',
          paddingInline: '0.5rem',
        }}
      >
        {t('nav.language')}
      </legend>
      {supportedLanguages.map((lng) => {
        const active = lng === current;
        const label = lng === 'en' ? t('lang.en') : t('lang.ja');
        return (
          <button
            key={lng}
            type="button"
            onClick={() => {
              setLanguage(lng);
            }}
            style={{
              background: active ? 'var(--nf-color-fg)' : 'transparent',
              color: active ? 'var(--nf-color-bg)' : 'var(--nf-color-fg)',
              border: 'none',
              borderRadius: '999px',
              paddingBlock: '0.375rem',
              paddingInline: '0.75rem',
              fontFamily: 'var(--font-mono)',
              fontSize: '0.75rem',
              cursor: 'pointer',
            }}
          >
            {label}
          </button>
        );
      })}
    </fieldset>
  );
}

function ThemeSwitcher(): ReactElement {
  const { t } = useTranslation('common');
  const { preference, setPreference } = useTheme();
  return (
    <fieldset
      style={{
        border: '1px solid var(--nf-color-hairline)',
        borderRadius: '999px',
        padding: '0.125rem',
        display: 'inline-flex',
        gap: '0.125rem',
        flexWrap: 'wrap',
      }}
    >
      <legend
        style={{
          fontFamily: 'var(--font-mono)',
          fontSize: '0.625rem',
          letterSpacing: '0.08em',
          color: 'var(--nf-color-fg-muted)',
          paddingInline: '0.5rem',
        }}
      >
        {t('nav.theme')}
      </legend>
      {themePreferences.map((pref: ThemePreference) => {
        const active = pref === preference;
        return (
          <button
            key={pref}
            type="button"
            onClick={() => {
              setPreference(pref);
            }}
            style={{
              background: active ? 'var(--nf-color-fg)' : 'transparent',
              color: active ? 'var(--nf-color-bg)' : 'var(--nf-color-fg)',
              border: 'none',
              borderRadius: '999px',
              paddingBlock: '0.375rem',
              paddingInline: '0.75rem',
              fontFamily: 'var(--font-mono)',
              fontSize: '0.75rem',
              cursor: 'pointer',
            }}
          >
            {pref === 'system' ? t('theme.system') : t(themeLabelKey(pref))}
          </button>
        );
      })}
    </fieldset>
  );
}

function themeLabelKey(name: (typeof concreteThemes)[number]): string {
  return `theme.${name}` as const;
}

/* ── Dashboard widgets (workspace-scoped) ─────────────────── */

function HomeDashboard(): ReactElement {
  const { data: workspaces } = useWorkspacesQuery();
  const firstWs = workspaces[0];
  if (!firstWs) return <></>;
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
        gap: '2rem',
        paddingBlock: '2rem',
        paddingInline: 'clamp(1.5rem, 6vw, 3.5rem)',
        maxInlineSize: '72rem',
        marginInline: 'auto',
        inlineSize: '100%',
      }}
    >
      {/* Header */}
      <header style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
        <h1
          style={{
            fontFamily: 'var(--font-display)',
            fontOpticalSizing: 'auto',
            fontWeight: 400,
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
            fontFamily: 'var(--font-mono)',
            fontSize: '0.6875rem',
            letterSpacing: '0.18em',
            color: 'var(--nf-color-fg-muted)',
            margin: 0,
          }}
        >
          {t('landing.tagline')}
        </p>
      </header>

      {/* Quick actions */}
      <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap' }}>
        <button
          type="button"
          onClick={() => window.dispatchEvent(new Event(OPEN_COMMAND_PALETTE_EVENT))}
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: '0.5rem',
            padding: '0.5rem 1rem',
            borderRadius: '0.5rem',
            background: 'var(--nf-color-accent, var(--nf-color-accent))',
            color: 'white',
            border: 'none',
            cursor: 'pointer',
            fontSize: '0.875rem',
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
            gap: '0.5rem',
            padding: '0.5rem 1rem',
            borderRadius: '0.5rem',
            background: 'transparent',
            color: 'var(--nf-color-fg)',
            border: '1px solid var(--nf-color-border, var(--nf-color-hairline))',
            cursor: 'pointer',
            fontSize: '0.875rem',
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
            gap: '0.5rem',
            padding: '0.5rem 1rem',
            borderRadius: '0.5rem',
            background: 'transparent',
            color: 'var(--nf-color-fg)',
            border: '1px solid var(--nf-color-border, var(--nf-color-hairline))',
            textDecoration: 'none',
            fontSize: '0.875rem',
          }}
        >
          <Icon icon={Inbox} decorative />
          {t('nav.inbox')}
        </Link>
      </div>

      {/* Task summary */}
      <Suspense
        fallback={
          <div style={{ padding: '2rem', textAlign: 'center', color: 'var(--nf-color-fg-muted)' }}>
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
            margin: '0 0 0.75rem',
            fontSize: '0.85rem',
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

      {/* Theme / language */}
      <footer
        style={{
          display: 'flex',
          gap: '1rem',
          flexWrap: 'wrap',
          alignItems: 'center',
          marginTop: 'auto',
        }}
      >
        <ThemeSwitcher />
        <LanguageSwitcher />
      </footer>
    </section>
  );
}

export const Route = createFileRoute('/_authenticated/')({
  component: HomePage,
});
