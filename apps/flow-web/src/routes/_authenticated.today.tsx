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
import { createFileRoute, Link } from '@tanstack/react-router';
import { CheckCircle } from 'lucide-react';
import { type ReactElement, useMemo } from 'react';
import { useTranslation } from 'react-i18next';

import { OPEN_CREATE_TASK_EVENT } from '../components/layout/glass-dock';
import type { TaskDerivedState, TaskPriority } from '../features/tasks/api';
import { PRIORITY_COLOR, PRIORITY_KEY, STATE_COLOR } from '../features/tasks/constants';
import { dateKey } from '../lib/date-utils';
import { formatDueDate } from '../lib/format-date';
import { sdk } from '../lib/sdk';

type AssignedTask = components['schemas']['MyTaskListItem'];

type SectionKey = 'overdue' | 'today' | 'tomorrow' | 'thisWeek' | 'later' | 'noDue';

const SECTION_ORDER: readonly SectionKey[] = [
  'overdue',
  'today',
  'tomorrow',
  'thisWeek',
  'later',
  'noDue',
];

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
  const { t, i18n } = useTranslation('common');
  const locale = i18n.resolvedLanguage ?? 'en';

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
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>{t('today.subtitle')}</p>
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
          <CheckCircle
            size={48}
            strokeWidth={1}
            style={{ color: 'var(--nf-color-fg-muted)', opacity: 0.5 }}
          />
          <p style={{ margin: 0 }}>{t('today.empty')}</p>
          <button
            type="button"
            onClick={() => {
              window.dispatchEvent(new Event(OPEN_CREATE_TASK_EVENT));
            }}
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              padding: '0.5rem 1rem',
              borderRadius: '0.5rem',
              background: 'var(--nf-color-accent)',
              color: 'white',
              border: 'none',
              cursor: 'pointer',
              fontSize: 'var(--nf-text-sm)',
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
                color: isOverdue ? 'var(--nf-color-danger)' : 'var(--nf-color-fg-muted)',
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
                        ? 'var(--nf-color-danger-subtle)'
                        : 'var(--nf-color-surface)',
                      borderInlineStart: isOverdue
                        ? '3px solid var(--nf-color-danger)'
                        : '3px solid transparent',
                    }}
                  >
                    <span
                      aria-hidden
                      style={{
                        inlineSize: '0.5rem',
                        blockSize: '0.5rem',
                        borderRadius: '999px',
                        background:
                          STATE_COLOR[task.derivedState as TaskDerivedState] ??
                          'var(--nf-color-fg-muted)',
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
                        {t(PRIORITY_KEY[pri])}
                      </span>
                    ) : null}
                    <span
                      style={{
                        fontSize: 'var(--nf-text-xs)',
                        color: 'var(--nf-color-fg-muted)',
                        whiteSpace: 'nowrap',
                      }}
                    >
                      {task.projectName
                        ? `${task.workspaceName} · ${task.projectName}`
                        : task.workspaceName}
                    </span>
                    <span
                      style={{
                        fontSize: 'var(--nf-text-xs)',
                        color: 'var(--nf-color-fg-muted)',
                        whiteSpace: 'nowrap',
                        minWidth: '5.5rem',
                        textAlign: 'right',
                      }}
                    >
                      {task.dueOn ? formatDueDate(task.dueOn, locale) : t('today.no_due_label')}
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
