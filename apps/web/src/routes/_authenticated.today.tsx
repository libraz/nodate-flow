/**
 * /today — "Today" view. Cross-workspace list of tasks where the
 * authenticated user is attached as an actor, grouped by due date
 * (overdue / today / tomorrow / this week / later / no due).
 *
 * Backed by the single `GET /me/tasks` aggregate endpoint so the web
 * client no longer fans out one request per workspace. Each row
 * already carries workspace id + name for grouping/display.
 */

import type { components } from '@nodate-flow/sdk';
import { useSuspenseQuery } from '@tanstack/react-query';
import { Link, createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useMemo } from 'react';
import { useTranslation } from 'react-i18next';

import { OPEN_COMMAND_PALETTE_EVENT } from '../components/layout/glass-dock';
import type { TaskPriority } from '../features/tasks/api';
import { sdk } from '../lib/sdk';

type AssignedTask = components['schemas']['MyTaskListItem'];

type SectionKey = 'overdue' | 'today' | 'tomorrow' | 'thisWeek' | 'later' | 'noDue';

const STATE_COLOR: Record<string, string> = {
  open: 'var(--color-info, #3498db)',
  waiting: 'var(--color-warning, #f39c12)',
  review: 'var(--color-accent, #9b59b6)',
  done: 'var(--color-success, #27ae60)',
  cancelled: 'var(--color-muted, #95a5a6)',
};

const PRIORITY_LABEL: Record<TaskPriority, string> = {
  0: 'tasks.priority.none',
  1: 'tasks.priority.low',
  2: 'tasks.priority.medium',
  3: 'tasks.priority.high',
  4: 'tasks.priority.urgent',
};

const PRIORITY_COLOR: Record<TaskPriority, string> = {
  0: 'transparent',
  1: 'var(--color-info, #3498db)',
  2: 'var(--color-warning, #f39c12)',
  3: 'var(--nf-color-danger, #e67e22)',
  4: 'var(--nf-color-danger, #c0392b)',
};

const SECTION_ORDER: readonly SectionKey[] = [
  'overdue',
  'today',
  'tomorrow',
  'thisWeek',
  'later',
  'noDue',
];

/** Local-time YYYY-MM-DD for the start of `d`. */
function dateKey(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
}

function classifyDue(dueOn: string | undefined, todayKey: string): SectionKey {
  if (!dueOn) return 'noDue';
  if (dueOn < todayKey) return 'overdue';
  if (dueOn === todayKey) return 'today';
  // tomorrow = todayKey + 1 day
  const t = new Date(`${todayKey}T00:00:00`);
  const tomorrow = new Date(t);
  tomorrow.setDate(t.getDate() + 1);
  if (dueOn === dateKey(tomorrow)) return 'tomorrow';
  const weekEnd = new Date(t);
  weekEnd.setDate(t.getDate() + 7);
  if (dueOn <= dateKey(weekEnd)) return 'thisWeek';
  return 'later';
}

function TodayRoute(): ReactElement {
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

  const sections = useMemo<Record<SectionKey, AssignedTask[]>>(() => {
    const empty: Record<SectionKey, AssignedTask[]> = {
      overdue: [],
      today: [],
      tomorrow: [],
      thisWeek: [],
      later: [],
      noDue: [],
    };
    const todayKey = dateKey(new Date());
    for (const task of tasks) {
      // Hide closed states from the daily focus view.
      if (task.derivedState === 'done' || task.derivedState === 'cancelled') continue;
      empty[classifyDue(task.dueOn, todayKey)].push(task);
    }
    // Sort each section: overdue ascending (oldest first), no_due by
    // priority desc, others ascending by due date then priority desc.
    const byDueAsc = (a: AssignedTask, b: AssignedTask): number => {
      const ad = a.dueOn ?? '';
      const bd = b.dueOn ?? '';
      if (ad !== bd) return ad < bd ? -1 : 1;
      return b.priority - a.priority;
    };
    const byPriorityDesc = (a: AssignedTask, b: AssignedTask): number => b.priority - a.priority;
    for (const key of SECTION_ORDER) {
      empty[key].sort(key === 'noDue' ? byPriorityDesc : byDueAsc);
    }
    return empty;
  }, [tasks]);

  const totalCount = SECTION_ORDER.reduce((sum, k) => sum + sections[k].length, 0);

  return (
    <section
      style={{
        padding: 'clamp(1.5rem, 4vw, 2.5rem)',
        display: 'flex',
        flexDirection: 'column',
        gap: '1.5rem',
        maxInlineSize: '60rem',
        marginInline: 'auto',
        inlineSize: '100%',
      }}
    >
      <header style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
        <h1
          style={{
            margin: 0,
            fontFamily: 'var(--font-display)',
            fontSize: 'clamp(1.75rem, 3vw, 2.25rem)',
          }}
        >
          {t('today.title')}
        </h1>
        <p style={{ margin: 0, color: 'var(--color-muted)' }}>{t('today.subtitle')}</p>
      </header>

      {totalCount === 0 ? (
        <div
          style={{
            padding: '3rem 1rem',
            textAlign: 'center',
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            gap: '1rem',
            color: 'var(--nf-color-fg-muted)',
            border: '1px dashed var(--nf-color-border)',
            borderRadius: '0.75rem',
            background: 'var(--nf-color-bg-sunken)',
          }}
        >
          <p style={{ margin: 0 }}>{t('today.empty')}</p>
          <button
            type="button"
            onClick={() => {
              window.dispatchEvent(new Event(OPEN_COMMAND_PALETTE_EVENT));
            }}
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              padding: '0.5rem 1rem',
              borderRadius: '0.5rem',
              background: 'var(--nf-color-accent, var(--color-accent, #9b59b6))',
              color: 'var(--nf-color-accent-fg, white)',
              border: 'none',
              cursor: 'pointer',
              fontSize: '0.875rem',
              fontWeight: 500,
            }}
          >
            {t('dock.command_palette.create_task')}
          </button>
        </div>
      ) : null}

      {SECTION_ORDER.map((key) => {
        const items = sections[key];
        if (items.length === 0) return null;
        const isOverdue = key === 'overdue';
        return (
          <section
            key={key}
            aria-label={t(`today.sections.${key}`)}
            style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}
          >
            <h2
              style={{
                margin: 0,
                fontSize: '0.85rem',
                textTransform: 'uppercase',
                letterSpacing: '0.05em',
                color: isOverdue ? 'var(--nf-color-danger, #c0392b)' : 'var(--color-muted)',
              }}
            >
              {t(`today.sections.${key}`)} ({items.length})
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
              {items.map((task) => {
                const pri = (task.priority ?? 0) as TaskPriority;
                return (
                  <li
                    key={task.id}
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: '0.75rem',
                      padding: '0.6rem 0.75rem',
                      borderRadius: '0.5rem',
                      background: isOverdue
                        ? 'var(--nf-color-danger-subtle, rgba(192,57,43,0.08))'
                        : 'var(--color-surface, rgba(127,127,127,0.05))',
                      borderInlineStart: isOverdue
                        ? '3px solid var(--nf-color-danger, #c0392b)'
                        : '3px solid transparent',
                    }}
                  >
                    <span
                      aria-hidden
                      style={{
                        inlineSize: '0.5rem',
                        blockSize: '0.5rem',
                        borderRadius: '999px',
                        background: STATE_COLOR[task.derivedState] ?? 'var(--color-muted)',
                        flexShrink: 0,
                      }}
                    />
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
                    {pri > 0 ? (
                      <span
                        style={{
                          fontSize: '0.6875rem',
                          fontWeight: 600,
                          padding: '0.125rem 0.375rem',
                          borderRadius: '0.25rem',
                          background: PRIORITY_COLOR[pri],
                          color: 'white',
                          whiteSpace: 'nowrap',
                          lineHeight: 1.3,
                        }}
                      >
                        {t(PRIORITY_LABEL[pri])}
                      </span>
                    ) : null}
                    <span
                      style={{
                        fontSize: '0.75rem',
                        color: 'var(--color-muted)',
                        whiteSpace: 'nowrap',
                      }}
                    >
                      {task.projectName
                        ? `${task.workspaceName} · ${task.projectName}`
                        : task.workspaceName}
                    </span>
                    <span
                      style={{
                        fontSize: '0.75rem',
                        color: 'var(--color-muted)',
                        whiteSpace: 'nowrap',
                        minWidth: '5.5rem',
                        textAlign: 'right',
                      }}
                    >
                      {task.dueOn ?? t('today.no_due_label')}
                    </span>
                  </li>
                );
              })}
            </ul>
          </section>
        );
      })}
    </section>
  );
}

export const Route = createFileRoute('/_authenticated/today')({
  component: TodayRoute,
});
