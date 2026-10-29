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
import { useFirstLoadStagger } from '@nodate-flow/ui/hooks/use-first-load-stagger';
import { cx } from '@nodate-flow/ui/lib/cx';
import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { Day } from '@nodate-flow/ui/time';
import { useSuspenseQuery } from '@tanstack/react-query';
import { createFileRoute, Link } from '@tanstack/react-router';
import { CheckCircle } from 'lucide-react';
import { type CSSProperties, type ReactElement, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { OPEN_CREATE_TASK_EVENT } from '../components/layout/glass-dock';
import type { TaskDerivedState, TaskPriority } from '../features/tasks/api';
import { PRIORITY_KEY, PRIORITY_TONE, STATE_COLOR } from '../features/tasks/constants';
import { apiRequest } from '../lib/api';
import { formatDueDate } from '../lib/format-date';
import { useEffectiveZone } from '../lib/use-effective-timezone';
import styles from './_authenticated.today.module.css';

type AssignedTask = components['schemas']['MyTaskListItem'];

type SectionKey = 'overdue' | 'today' | 'tomorrow' | 'thisWeek' | 'later' | 'noDue';

const SECTION_LABEL_KEY: Record<SectionKey, string> = {
  overdue: 'today.sections.overdue',
  today: 'today.sections.today',
  tomorrow: 'today.sections.tomorrow',
  thisWeek: 'today.sections.thisWeek',
  later: 'today.sections.later',
  noDue: 'today.sections.noDue',
};

const SECTION_ORDER: readonly SectionKey[] = [
  'overdue',
  'today',
  'tomorrow',
  'thisWeek',
  'later',
  'noDue',
];

/**
 * Which section a due date belongs to, relative to `today`.
 *
 * Both operands are calendar days, so the comparison never touches an
 * instant and the offsets are stepped on {@link Day} rather than by
 * adding 86,400 seconds — the day after a DST transition is still the
 * next day.
 */
function classifyDue(dueOn: string | undefined, today: Day): SectionKey {
  if (!dueOn) return 'noDue';
  const todayKey = today.toString();
  if (dueOn < todayKey) return 'overdue';
  if (dueOn === todayKey) return 'today';
  if (dueOn === today.addDays(1).toString()) return 'tomorrow';
  if (dueOn <= today.addDays(7).toString()) return 'thisWeek';
  return 'later';
}

function TodayRoute(): ReactElement {
  const { t, i18n } = useTranslation('common');
  const locale = i18n.resolvedLanguage ?? 'en';
  // Which day a task is due relative to has to be read in the same zone
  // the calendar files it under, or a task sits in "overdue" here while
  // the grid still shows it on today.
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

  const sections = useMemo<Record<SectionKey, AssignedTask[]>>(() => {
    const empty: Record<SectionKey, AssignedTask[]> = {
      overdue: [],
      today: [],
      tomorrow: [],
      thisWeek: [],
      later: [],
      noDue: [],
    };
    const today = Day.today(zone);
    for (const task of tasks) {
      // Hide closed states from the daily focus view.
      if (task.derivedState === 'done' || task.derivedState === 'cancelled') continue;
      empty[classifyDue(task.dueOn, today)].push(task);
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
  }, [tasks, zone]);

  const stagger = useFirstLoadStagger();

  const totalCount = SECTION_ORDER.reduce((sum, k) => sum + sections[k].length, 0);

  return (
    <section className={styles.page}>
      <header className={styles.header}>
        <h1 className={styles.title}>{t('today.title')}</h1>
        <p className={styles.subtitle}>{t('today.subtitle')}</p>
      </header>

      {totalCount === 0 ? (
        <EmptyState
          className={styles.empty}
          icon={<CheckCircle size={40} strokeWidth={1.25} aria-hidden="true" />}
          title={t('today.empty')}
          titleAs="p"
          action={
            <Button
              type="button"
              variant="primary"
              onClick={() => {
                window.dispatchEvent(new Event(OPEN_CREATE_TASK_EVENT));
              }}
            >
              {t('dock.command_palette.create_task')}
            </Button>
          }
        />
      ) : null}

      {SECTION_ORDER.map((key) => {
        const items = sections[key];
        if (items.length === 0) return null;
        const isOverdue = key === 'overdue';
        const label = t(SECTION_LABEL_KEY[key]);
        return (
          <section key={key} aria-label={label} className={styles.section}>
            <h2 className={cx(styles.sectionTitle, isOverdue && styles.sectionTitleOverdue)}>
              {label}
              <span className={styles.sectionCount}>({items.length})</span>
            </h2>
            <ul className={styles.list}>
              {items.map((task, index) => {
                const pri = (task.priority ?? 0) as TaskPriority;
                return (
                  <li
                    key={task.id}
                    className={motion.enter}
                    style={
                      {
                        ...stagger(index),
                        '--state-tone': STATE_COLOR[task.derivedState as TaskDerivedState],
                      } as CSSProperties
                    }
                  >
                    <Link
                      to="/tasks/$taskId"
                      params={{ taskId: task.id }}
                      className={cx(styles.row, isOverdue && styles.rowOverdue, 'nf-focus-ring')}
                    >
                      <span aria-hidden className={styles.stateDot} />
                      <span className={styles.taskTitle}>{task.title}</span>
                      {pri > 0 ? (
                        <Badge tone={PRIORITY_TONE[pri]}>{t(PRIORITY_KEY[pri])}</Badge>
                      ) : null}
                      <span className={styles.context}>
                        {task.projectName
                          ? `${task.workspaceName} · ${task.projectName}`
                          : task.workspaceName}
                      </span>
                      <span className={styles.due}>
                        {task.dueOn ? formatDueDate(task.dueOn, locale) : t('today.no_due_label')}
                      </span>
                    </Link>
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
