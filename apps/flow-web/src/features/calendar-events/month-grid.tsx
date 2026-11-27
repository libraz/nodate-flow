import type { HolidayEntry } from '@nodate-flow/holidays';
import type { components } from '@nodate-flow/sdk';
import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import { cx } from '@nodate-flow/ui/lib/cx';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { Link } from '@tanstack/react-router';
import { Users } from 'lucide-react';
import {
  type CSSProperties,
  type KeyboardEvent,
  type ReactElement,
  useCallback,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';
import { dateKey } from '../../lib/date-utils';
import type { TaskDerivedState } from '../tasks/api';
import { STATE_COLOR } from '../tasks/constants';
import styles from './calendar-page.module.css';
import { eventColorStyle, eventPillClassName, eventPillMarkerClassName } from './event-pill';
import type { CalendarDragPayload } from './lib/drag-payload';
import type { CalendarEvent } from './lib/expand-calendar-events';
import {
  buildMonthGrid,
  dateNumberClass,
  daysInMonth,
  directionBetween,
  dowFromStart,
  keyDayDelta,
  type MonthCell,
  type NavDirection,
  WEEKDAY_KEYS_BY_START,
  type WeekStart,
  weekdayHeaderClass,
} from './lib/month-geometry';
import type { PointerDragHandle } from './lib/pointer-drag';

type CalendarTask = components['schemas']['MyTaskListItem'];

/** Pills a desktop cell draws before its first measurement. */
const DEFAULT_PILL_SLOTS = 5;

/**
 * Whole pill lines that fit in a cell's pill list, or null when the list
 * has no layout to read. One line is `--cal-pill-block-size` (rem) plus
 * the list's own row gap.
 */
function pillSlotsFor(list: HTMLElement): number | null {
  const height = list.clientHeight;
  if (height <= 0) return null;
  const style = getComputedStyle(list);
  const remPx = Number.parseFloat(getComputedStyle(list.ownerDocument.documentElement).fontSize);
  const pillPx = Number.parseFloat(style.getPropertyValue('--cal-pill-block-size')) * remPx;
  const gapPx = Number.parseFloat(style.rowGap) || 0;
  if (!(pillPx > 0)) return null;
  return Math.max(0, Math.floor((height + gapPx) / (pillPx + gapPx)));
}

interface MonthGridProps {
  cursor: { year: number; month: number };
  weekStart: WeekStart;
  /** Slide-in class for the month the grid has just navigated to. */
  slideClass: string | undefined;
  /** True while either window query has no data yet. */
  isPending: boolean;
  /** dueOn → tasks, after layer filtering. */
  byDate: ReadonlyMap<string, CalendarTask[]>;
  /** dateKey → events, after kind and label filtering. */
  eventsByDate: ReadonlyMap<string, CalendarEvent[]>;
  holidaysByDate: ReadonlyMap<string, HolidayEntry[]>;
  todayKeyValue: string;
  locale: string;
  drag: PointerDragHandle<CalendarDragPayload>;
  onCellClick: (cellKey: string, shiftKey: boolean) => void;
  onDayOpen: (dayKey: string) => void;
  /** Keyboard navigation crossed into another month. */
  onMonthChange: (month: { year: number; month: number }, direction: NavDirection) => void;
  onEventOpen: (event: CalendarEvent) => void;
}

/** The desktop month grid: weekday header row plus one cell per day with its pills. */
function MonthGrid({
  cursor,
  weekStart,
  slideClass,
  isPending,
  byDate,
  eventsByDate,
  holidaysByDate,
  todayKeyValue,
  locale,
  drag: pointerDrag,
  onCellClick: handleCellClick,
  onDayOpen,
  onMonthChange,
  onEventOpen,
}: MonthGridProps): ReactElement {
  const { t } = useTranslation('common');
  const weekdayKeys = WEEKDAY_KEYS_BY_START[weekStart];
  const monthKey = `${cursor.year}-${cursor.month}`;
  const dragOverKey = pointerDrag.drag?.overKey ?? null;
  const cells = useMemo(
    () => buildMonthGrid(cursor.year, cursor.month, weekStart),
    [cursor, weekStart],
  );
  const cellDayFormat = useMemo(
    () => new Intl.DateTimeFormat(locale, { dateStyle: 'full' }),
    [locale],
  );
  /** The desktop cell that holds the grid's single tab stop. */
  const [focusKey, setFocusKey] = useState<string | null>(null);
  const cellNodes = useRef(new Map<string, HTMLDivElement>());
  /**
   * How many pills one desktop cell draws. Cells have a fixed height
   * derived from the viewport, so the budget is read off one cell's pill
   * list and shared by all of them. The default holds until the first
   * measurement, and is what a DOM without layout keeps.
   */
  const [pillSlots, setPillSlots] = useState(DEFAULT_PILL_SLOTS);
  // Stable so the observer is attached once per mounted list, not once per render.
  const pillListRef = useCallback((el: HTMLUListElement | null): (() => void) | undefined => {
    if (el === null || typeof ResizeObserver === 'undefined') return undefined;
    const measure = (): void => {
      const slots = pillSlotsFor(el);
      if (slots !== null) setPillSlots(slots);
    };
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    measure();
    return () => observer.disconnect();
  }, []);
  // A cell keyboard navigation moved to, focused once it has rendered —
  // after a month change that is a cell of the next grid.
  const pendingFocusKey = useRef<string | null>(null);

  /**
   * The cell that is the grid's tab stop: the last one keyboard focus
   * reached while it is still on screen, else today, else the first day
   * of the month. One stop rather than thirty keeps Tab moving through
   * the page; the arrow keys move between days.
   */
  const inMonthKeys = cells.filter((c) => c.inMonth).map((c) => c.key);
  const firstInMonthCell = cells.find((c) => c.inMonth);
  const tabStopKey =
    focusKey !== null && inMonthKeys.includes(focusKey)
      ? focusKey
      : inMonthKeys.includes(todayKeyValue)
        ? todayKeyValue
        : (inMonthKeys[0] ?? null);

  const cellRef =
    (key: string) =>
    (el: HTMLDivElement | null): void => {
      pointerDrag.dropCellRef(key)(el);
      if (el) cellNodes.current.set(key, el);
      else cellNodes.current.delete(key);
    };

  // Focus is the DOM's, so moving it to a cell that only exists after
  // this render is a synchronisation with something outside React.
  useLayoutEffect(() => {
    const key = pendingFocusKey.current;
    if (key === null) return;
    const node = cellNodes.current.get(key);
    if (!node) return;
    pendingFocusKey.current = null;
    node.focus();
  });

  const handleCellKeyDown = (e: KeyboardEvent<HTMLDivElement>, cell: MonthCell): void => {
    // Keys pressed on a pill inside the cell belong to the pill: Enter on
    // an event pill opens that event, not a new one on its day.
    if (e.target !== e.currentTarget) return;
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      if (cell.inMonth) handleCellClick(cell.key, e.shiftKey);
      return;
    }
    let target: Date;
    if (e.key === 'PageUp' || e.key === 'PageDown') {
      const step = e.key === 'PageUp' ? -1 : 1;
      const first = new Date(cell.date.getFullYear(), cell.date.getMonth() + step, 1);
      const day = Math.min(cell.date.getDate(), daysInMonth(first.getFullYear(), first.getMonth()));
      target = new Date(first.getFullYear(), first.getMonth(), day);
    } else {
      const rtl = getComputedStyle(e.currentTarget).direction === 'rtl';
      const delta = keyDayDelta(e.key, dowFromStart(cell.date, weekStart), rtl);
      if (delta === null) return;
      target = new Date(cell.date);
      target.setDate(target.getDate() + delta);
    }
    e.preventDefault();
    const targetKey = dateKey(target);
    const targetMonth = { year: target.getFullYear(), month: target.getMonth() };
    const direction = directionBetween(cursor, targetMonth);
    if (direction !== null) {
      onMonthChange(targetMonth, direction);
    }
    setFocusKey(targetKey);
    pendingFocusKey.current = targetKey;
  };

  return (
    <>
      <div className={styles.weekdayRow}>
        {weekdayKeys.map((wk, idx) => (
          <div key={wk} className={cx(styles.weekday, weekdayHeaderClass(weekdayKeys, idx))}>
            {t(`calendar.weekday.${wk}`)}
          </div>
        ))}
      </div>
      {/* Keyed by month, so a navigation mounts a fresh grid that
            slides in from the side it came from. A refetch keeps
            the key, and the arrival month has no direction yet. */}
      <div
        key={monthKey}
        className={cx(styles.grid, slideClass)}
        // The week count sets the row tracks the remaining height is shared by.
        style={{ '--cal-weeks': cells.length / 7 } as CSSProperties}
        aria-busy={isPending || undefined}
      >
        {cells.map((cell) => {
          const dayTasks = byDate.get(cell.key) ?? [];
          const dayEvents = eventsByDate.get(cell.key) ?? [];
          const dayHolidays = holidaysByDate.get(cell.key) ?? [];
          const totalCount = dayTasks.length + dayEvents.length;
          // The cell draws as many pills as its fixed height holds,
          // tasks first; the rest fold into the header's "+N more".
          const visibleTasks = dayTasks.slice(0, pillSlots);
          const visibleEvents = dayEvents.slice(0, Math.max(0, pillSlots - visibleTasks.length));
          const hiddenCount = totalCount - visibleTasks.length - visibleEvents.length;
          const isToday = cell.key === todayKeyValue;
          const isDragOver = dragOverKey === cell.key;
          // Render only the first holiday label on the cell (rare
          // multi-holiday days append "+N" — full list lives in the
          // `title` so it stays accessible on hover).
          const primaryHoliday = dayHolidays[0] ?? null;
          const extraHolidayCount = Math.max(0, dayHolidays.length - 1);
          const holidayTitle = dayHolidays.map((h) => h.name).join(', ');
          const dateNumberCls = dateNumberClass(cell.date, dayHolidays.length > 0);
          return (
            // A cell carries its handlers whether or not it is a
            // day of the month on screen, but only a day of the
            // month is given a role — a leading or trailing cell
            // is filler, and both handlers no-op on one. The rule
            // reads the role as an expression rather than a value
            // and so cannot see the one the day cells get, and it
            // is right about the filler ones either way.
            // biome-ignore lint/a11y/noStaticElementInteractions: role is conditional on `cell.inMonth`; the handlers below are inert for the cells that have none.
            // biome-ignore lint/a11y/useAriaPropsSupportedByRole: same — the label is set only alongside the conditional `button` role.
            <div
              key={cell.key}
              // Registers the cell as a drop target: the gesture
              // hit-tests these rects rather than asking the DOM
              // what is under the pointer, which a finger — whose
              // events stay aimed at the element it started on —
              // would answer wrongly.
              ref={cellRef(cell.key)}
              // data-cell-key exposes the YYYY-MM-DD cell address for
              // E2E drag-target selection (otherwise CSS-module hashes
              // make cells un-addressable).
              data-cell-key={cell.key}
              onClick={(e) => {
                if ((e.target as HTMLElement).closest('a, button')) return;
                if (cell.inMonth) handleCellClick(cell.key, e.shiftKey);
              }}
              onKeyDown={(e) => handleCellKeyDown(e, cell)}
              onFocus={(e) => {
                if (e.target === e.currentTarget && cell.inMonth) setFocusKey(cell.key);
              }}
              role={cell.inMonth ? 'button' : undefined}
              tabIndex={cell.inMonth ? (cell.key === tabStopKey ? 0 : -1) : undefined}
              aria-label={
                cell.inMonth
                  ? primaryHoliday
                    ? t('calendar.month_scroll.day_label_holiday', {
                        date: cellDayFormat.format(cell.date),
                        holiday: primaryHoliday.name,
                        count: totalCount,
                      })
                    : t('calendar.month_scroll.day_label', {
                        date: cellDayFormat.format(cell.date),
                        count: totalCount,
                      })
                  : undefined
              }
              aria-current={isToday ? 'date' : undefined}
              className={cx(
                styles.cell,
                cell.inMonth && styles['cell--inMonth'],
                isToday && styles['cell--today'],
                isDragOver && styles['cell--dragOver'],
              )}
            >
              <div className={styles.cellHeader}>
                <span className={cx(styles.dateNumber, dateNumberCls, isToday && styles.todayPill)}>
                  {cell.date.getDate()}
                </span>
                {primaryHoliday ? (
                  <span className={styles.holidayLabel} title={holidayTitle}>
                    {extraHolidayCount > 0
                      ? t('calendar.holiday_more', {
                          name: primaryHoliday.name,
                          count: extraHolidayCount,
                        })
                      : primaryHoliday.name}
                  </span>
                ) : null}
                {/* In the header rather than on a line of its own, so
                      every line below it is a pill. One count for what
                      the cell had no room for, and a way to reach it:
                      the day sheet lists the whole day. */}
                {hiddenCount > 0 ? (
                  <button
                    type="button"
                    className={styles.morePill}
                    onClick={(e) => {
                      e.stopPropagation();
                      onDayOpen(cell.key);
                    }}
                  >
                    {t('calendar.more', { count: hiddenCount })}
                  </button>
                ) : null}
              </div>
              <DayPillList
                tasks={visibleTasks}
                events={visibleEvents}
                cellKey={cell.key}
                drag={pointerDrag}
                onEventOpen={onEventOpen}
                {...(cell === firstInMonthCell ? { listRef: pillListRef } : {})}
              />
            </div>
          );
        })}
      </div>
    </>
  );
}

/** One pill a desktop cell draws: a task due that day, or an event covering it. */
type DayPill = { kind: 'task'; task: CalendarTask } | { kind: 'event'; event: CalendarEvent };

interface DayPillListProps {
  cellKey: string;
  /** The tasks the cell has room for, already cut to fit. */
  tasks: CalendarTask[];
  /** The events the cell has room for, already cut to fit. */
  events: CalendarEvent[];
  drag: PointerDragHandle<CalendarDragPayload>;
  onEventOpen: (event: CalendarEvent) => void;
  /** Set on one cell only: the list the grid measures to size every cell's pill budget. */
  listRef?: (el: HTMLUListElement | null) => (() => void) | undefined;
}

/**
 * The pills of one desktop cell.
 *
 * Rendered through {@link usePresenceList}, so a pill that leaves the day
 * — deleted, moved by a drop, hidden by a layer or calendar toggle —
 * collapses and fades instead of vanishing, and one that arrives slides
 * in. A refetch with the same members keeps every node and plays nothing.
 */
function DayPillList({
  cellKey,
  tasks,
  events,
  drag: pointerDrag,
  onEventOpen,
  listRef,
}: DayPillListProps): ReactElement {
  const { t } = useTranslation('common');
  const pills: DayPill[] = [
    ...tasks.map((task): DayPill => ({ kind: 'task', task })),
    ...events.map((event): DayPill => ({ kind: 'event', event })),
  ];
  const { entries, onExited } = usePresenceList(pills, (p) =>
    p.kind === 'task' ? `task-${p.task.id}` : `ev-${p.event.id}`,
  );

  return (
    <ul ref={listRef} className={styles.pillList}>
      {entries.map(({ key, item, present }) => (
        <PresenceItem
          key={key}
          as="li"
          present={present}
          onExited={() => onExited(key)}
          className={motion.enter}
        >
          {item.kind === 'task' ? renderTaskPill(item.task) : renderEventPill(item.event)}
        </PresenceItem>
      ))}
    </ul>
  );

  function renderTaskPill(task: CalendarTask): ReactElement {
    const sourceKey = `task-${task.id}@${cellKey}`;
    const dotColor =
      STATE_COLOR[task.derivedState as TaskDerivedState] ?? 'var(--nf-color-fg-muted)';
    return (
      <Link
        to="/tasks/$taskId"
        params={{ taskId: task.id }}
        title={`${task.title} · ${task.workspaceName}\n${t('calendar.drag_to_reschedule')}`}
        // The browser drags a link's URL of its own
        // accord, which would run alongside this
        // gesture and drop a hyperlink somewhere.
        draggable={false}
        onPointerDown={(e) => {
          pointerDrag.pressSource(e, sourceKey, {
            type: 'task',
            taskId: task.id,
            fromDate: cellKey,
            label: task.title,
            dotColor,
          });
        }}
        className={cx(
          styles.taskPill,
          pointerDrag.holdingKey === sourceKey && styles['pill--holding'],
          pointerDrag.drag?.sourceKey === sourceKey && styles['pill--lifted'],
        )}
        onClick={(e) => {
          e.stopPropagation();
          // The release that ended a drag becomes a
          // click on the source; navigating on it
          // would take the user off the calendar
          // every time they moved a task.
          if (pointerDrag.wasDragged()) e.preventDefault();
        }}
      >
        <span aria-hidden className={styles.taskPill__dot} style={{ background: dotColor }} />
        <span className={styles.taskPill__title}>{task.title}</span>
      </Link>
    );
  }

  function renderEventPill(ev: CalendarEvent): ReactElement {
    const sourceKey = `ev-${ev.id}@${cellKey}`;
    return (
      <button
        type="button"
        title={`${ev.title} · ${ev.workspaceName}\n${t('calendar.drag_to_reschedule')}`}
        aria-label={
          ev.labelName
            ? t('calendar.event_detail.open_label_with_label', {
                title: ev.title,
                label: ev.labelName,
                workspace: ev.workspaceName,
              })
            : t('calendar.event_detail.open_label', {
                title: ev.title,
                workspace: ev.workspaceName,
              })
        }
        onPointerDown={(e) => {
          pointerDrag.pressSource(e, sourceKey, {
            type: 'event',
            event: ev,
            fromDate: cellKey,
            label: ev.title,
            dotColor: ev.color,
          });
        }}
        onClick={(e) => {
          e.stopPropagation();
          // A drag ends in a click on the pill it
          // started from; opening the dialog then
          // would bury the move under a form.
          if (pointerDrag.wasDragged()) return;
          onEventOpen(ev);
        }}
        className={cx(
          styles.eventPill,
          eventPillClassName(ev.kind),
          ev.viewerAttending && styles['eventPill--viewerAttending'],
          styles['eventPill--draggable'],
          pointerDrag.holdingKey === sourceKey && styles['pill--holding'],
          pointerDrag.drag?.sourceKey === sourceKey && styles['pill--lifted'],
        )}
        style={eventColorStyle(ev.color)}
      >
        <span aria-hidden className={cx(styles.eventPill__marker, eventPillMarkerClassName)} />
        <span className={styles.eventPill__title}>{ev.title}</span>
        {ev.attendeeCount > 0 ? (
          <span
            role="img"
            className={styles.eventPill__attendees}
            aria-label={t('calendar.event_attendee_count', { count: ev.attendeeCount })}
          >
            <Users aria-hidden className={styles.eventPill__attendeesIcon} />
            {ev.attendeeCount}
          </span>
        ) : null}
      </button>
    );
  }
}

export default MonthGrid;
