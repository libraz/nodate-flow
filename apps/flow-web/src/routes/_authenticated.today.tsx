/**
 * /today — "Today" view. Cross-workspace list of tasks where the
 * authenticated user is attached as an actor, grouped by due date
 * (overdue / today / tomorrow / this week / later / no due).
 *
 * Backed by the single `GET /me/tasks` aggregate endpoint so the web
 * client no longer fans out one request per workspace. Each row
 * already carries workspace id + name for grouping/display.
 */

import { useFirstLoadStagger } from '@nodate-flow/ui/hooks/use-first-load-stagger';
import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import { cx } from '@nodate-flow/ui/lib/cx';
import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { Day } from '@nodate-flow/ui/time';
import { createFileRoute, Link } from '@tanstack/react-router';
import { CheckCircle } from 'lucide-react';
import { type CSSProperties, type ReactElement, useDeferredValue, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { OPEN_CREATE_TASK_EVENT } from '../components/layout/glass-dock';
import PageFrame from '../components/page-frame';
import PageHeader from '../components/page-header';
import {
  type MyTaskListItem,
  type TaskDerivedState,
  type TaskPriority,
  useMyTasksQuery,
} from '../features/tasks/api';
import CompleteTaskButton from '../features/tasks/complete-task-button';
import { PRIORITY_KEY, PRIORITY_TONE, STATE_COLOR } from '../features/tasks/constants';
import MyTasksFilterBar from '../features/tasks/my-tasks-filter-bar';
import rowStyles from '../features/tasks/task-row.module.css';
import {
  isDefaultMyTaskFilters,
  resetMyTaskFilters,
  useMyTaskFilters,
  useMyTaskSort,
} from '../features/tasks/use-my-task-filters';
import { formatDueDate } from '../lib/format-date';
import { useEffectiveZone } from '../lib/use-effective-timezone';
import styles from './_authenticated.today.module.css';

type AssignedTask = MyTaskListItem;

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

const SECTION_ORDER_DESC: readonly SectionKey[] = [
  'later',
  'thisWeek',
  'tomorrow',
  'today',
  'overdue',
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

interface TodayListProps {
  items: AssignedTask[];
  isOverdue: (task: AssignedTask) => boolean;
  stagger: (index: number) => CSSProperties | undefined;
  locale: string;
}

/** One section's rows; a completed row collapses out so the rows below close the gap. */
function TodayList({ items, isOverdue, stagger, locale }: TodayListProps): ReactElement {
  const { t } = useTranslation('common');
  const { entries, onExited } = usePresenceList(items, (task) => task.id);

  return (
    <ul className={rowStyles.list}>
      {entries.map(({ key, item: task, present }, index) => {
        const pri = (task.priority ?? 0) as TaskPriority;
        const context = task.projectName
          ? `${task.workspaceName} · ${task.projectName}`
          : task.workspaceName;
        return (
          <PresenceItem key={key} as="li" present={present} onExited={() => onExited(key)}>
            <div
              className={cx(motion.enter, rowStyles.item, isOverdue(task) && rowStyles.itemOverdue)}
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
                <span className={cx(rowStyles.context, rowStyles.contextHideNarrow)}>
                  {context}
                </span>
                <span className={rowStyles.due}>
                  {task.dueOn ? formatDueDate(task.dueOn, locale) : t('today.no_due_label')}
                </span>
              </Link>
            </div>
          </PresenceItem>
        );
      })}
    </ul>
  );
}

function TodayRoute(): ReactElement {
  const { t, i18n } = useTranslation('common');
  const locale = i18n.resolvedLanguage ?? 'en';
  // Which day a task is due relative to has to be read in the same zone
  // the calendar files it under, or a task sits in "overdue" here while
  // the grid still shows it on today.
  const zone = useEffectiveZone();

  const filters = useMyTaskFilters();
  const sort = useMyTaskSort();
  const live = useMemo(() => ({ filters, sort }), [filters, sort]);
  // A filter change keeps the previous rows on screen while the new ones
  // load, instead of dropping to the Suspense fallback on every keystroke.
  const params = useDeferredValue(live);
  const pending = params !== live;
  const { data: tasks } = useMyTasksQuery(params);

  const grouped = params.sort === 'dueOn' || params.sort === '-dueOn';
  const today = Day.today(zone);
  const isOverdue = (task: AssignedTask): boolean => classifyDue(task.dueOn, today) === 'overdue';

  const visible = useMemo(() => {
    const states = params.filters.states ?? [];
    return states.length === 0
      ? tasks
      : tasks.filter((task) => states.some((state) => state === task.derivedState));
  }, [tasks, params.filters.states]);

  const sections = useMemo<Record<SectionKey, AssignedTask[]>>(() => {
    const empty: Record<SectionKey, AssignedTask[]> = {
      overdue: [],
      today: [],
      tomorrow: [],
      thisWeek: [],
      later: [],
      noDue: [],
    };
    if (!grouped) return empty;
    const day = Day.today(zone);
    // Rows keep the order the server returned them in.
    for (const task of visible) empty[classifyDue(task.dueOn, day)].push(task);
    return empty;
  }, [visible, grouped, zone]);

  const stagger = useFirstLoadStagger();

  const sectionOrder = params.sort === '-dueOn' ? SECTION_ORDER_DESC : SECTION_ORDER;
  const defaultFilters = isDefaultMyTaskFilters(params.filters);

  return (
    <PageFrame>
      <PageHeader title={t('today.title')} description={t('today.subtitle')} help="my-tasks" />
      <MyTasksFilterBar />

      <div className={styles.list} aria-busy={pending ? 'true' : undefined}>
        {visible.length === 0 ? (
          <EmptyState
            className={styles.empty}
            icon={<CheckCircle size={40} strokeWidth={1.25} aria-hidden="true" />}
            title={defaultFilters ? t('today.empty') : t('today.filters.no_match')}
            titleAs="p"
            action={
              defaultFilters ? (
                <Button
                  type="button"
                  variant="primary"
                  onClick={() => {
                    window.dispatchEvent(new Event(OPEN_CREATE_TASK_EVENT));
                  }}
                >
                  {t('dock.command_palette.create_task')}
                </Button>
              ) : (
                <Button type="button" variant="default" onClick={resetMyTaskFilters}>
                  {t('tasks.filters.clear')}
                </Button>
              )
            }
          />
        ) : null}

        {grouped ? (
          sectionOrder.map((key) => {
            const items = sections[key];
            if (items.length === 0) return null;
            const overdueSection = key === 'overdue';
            const label = t(SECTION_LABEL_KEY[key]);
            return (
              <section key={key} aria-label={label} className={styles.section}>
                <h2
                  className={cx(styles.sectionTitle, overdueSection && styles.sectionTitleOverdue)}
                >
                  {label}
                  <span className={styles.sectionCount}>({items.length})</span>
                </h2>
                <TodayList items={items} isOverdue={isOverdue} stagger={stagger} locale={locale} />
              </section>
            );
          })
        ) : visible.length > 0 ? (
          <section aria-label={t('today.title')} className={styles.section}>
            <TodayList items={visible} isOverdue={isOverdue} stagger={stagger} locale={locale} />
          </section>
        ) : null}
      </div>
    </PageFrame>
  );
}

export const Route = createFileRoute('/_authenticated/today')({
  component: TodayRoute,
});
