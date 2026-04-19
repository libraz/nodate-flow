import { getOrCreateProvider } from '@nodate/holidays';
import { DateTime, Info } from 'luxon';
import {
  Fragment,
  type ReactElement,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';

import { useCalendarUi } from '../../stores/calendar-ui-store';
import { useCalendarEventsQuery, useUpdateEventMutation } from './api';
import styles from './calendar-grid.module.css';
import DayCell from './day-cell';
import type { CalendarEvent } from './types';

const WEEKDAY_NAMES = Info.weekdays('short');
const SUNDAY_FIRST = [WEEKDAY_NAMES[6] ?? 'Sun', ...WEEKDAY_NAMES.slice(0, 6)];
const MONTHS_BUFFER = 4;

interface MonthDay {
  date: DateTime;
  isCurrentMonth: boolean;
}

function buildMonthGrid(year: number, month: number): MonthDay[] {
  const firstOfMonth = DateTime.local(year, month, 1);
  const startDow = firstOfMonth.weekday % 7;
  const startDate = firstOfMonth.minus({ days: startDow });
  const daysInMonth = firstOfMonth.daysInMonth ?? 30;
  const totalCells = startDow + daysInMonth > 35 ? 42 : 35;

  const days: MonthDay[] = [];
  for (let i = 0; i < totalCells; i++) {
    const date = startDate.plus({ days: i });
    days.push({ date, isCurrentMonth: date.month === month });
  }
  return days;
}

function groupEventsByDay(events: CalendarEvent[]): Map<string, CalendarEvent[]> {
  const map = new Map<string, CalendarEvent[]>();
  for (const event of events) {
    const start = DateTime.fromISO(event.startAt).startOf('day');
    const end = DateTime.fromISO(event.endAt).startOf('day');
    const endInclusive = event.allDay
      ? end
      : DateTime.fromISO(event.endAt) > end
        ? end
        : end.minus({ days: 1 });

    let current = start;
    while (current <= endInclusive) {
      const key = current.toISODate();
      if (key) {
        const list = map.get(key);
        if (list) {
          list.push(event);
        } else {
          map.set(key, [event]);
        }
      }
      current = current.plus({ days: 1 });
    }
  }
  return map;
}

function toWeeks(days: MonthDay[]): MonthDay[][] {
  const weeks: MonthDay[][] = [];
  for (let i = 0; i < days.length; i += 7) {
    weeks.push(days.slice(i, i + 7));
  }
  return weeks;
}

function WeekRow({
  week,
  eventsByDay,
  holidayProvider,
  weekCount,
  onEventDrop,
}: {
  week: MonthDay[];
  eventsByDay: Map<string, CalendarEvent[]>;
  holidayProvider: ReturnType<typeof getOrCreateProvider>;
  weekCount: number;
  onEventDrop: (event: CalendarEvent, targetDate: DateTime) => void;
}): ReactElement {
  return (
    <div className={styles.weekRow} style={{ height: `calc((100dvh - 89px) / ${weekCount})` }}>
      {week.map((day) => {
        const isoDate = day.date.toISODate() ?? '';
        const dow = day.date.weekday % 7;
        const isWeekend = dow === 0 || dow === 6;
        const holiday = holidayProvider.isHoliday(day.date.toJSDate());
        return (
          <DayCell
            key={isoDate}
            date={day.date}
            events={eventsByDay.get(isoDate) ?? []}
            isCurrentMonth={day.isCurrentMonth}
            isWeekend={isWeekend}
            holiday={holiday}
            onEventDrop={onEventDrop}
          />
        );
      })}
    </div>
  );
}

function MonthSection({
  month,
  eventsByDay,
  holidayProvider,
  onEventDrop,
}: {
  month: DateTime;
  eventsByDay: Map<string, CalendarEvent[]>;
  holidayProvider: ReturnType<typeof getOrCreateProvider>;
  onEventDrop: (event: CalendarEvent, targetDate: DateTime) => void;
}): ReactElement {
  const days = useMemo(() => buildMonthGrid(month.year, month.month), [month.year, month.month]);
  const weeks = useMemo(() => toWeeks(days), [days]);

  return (
    <section data-month-section="" data-month={month.toFormat('yyyy-MM')}>
      {weeks.map((week, i) => (
        <WeekRow
          key={week[0]?.date.toISODate() ?? i}
          week={week}
          eventsByDay={eventsByDay}
          holidayProvider={holidayProvider}
          weekCount={weeks.length}
          onEventDrop={onEventDrop}
        />
      ))}
    </section>
  );
}

function MonthBoundary({ month }: { month: DateTime }): ReactElement {
  return (
    <div className={styles.boundary} data-boundary-month={month.toFormat('yyyy-MM')}>
      <span className={styles.boundaryLabel}>
        {month.toLocaleString({ month: 'long', year: 'numeric' })}
      </span>
    </div>
  );
}

export default function CalendarGrid(): ReactElement {
  const displayMonth = useCalendarUi((s) => s.displayMonth);
  const setDisplayMonth = useCalendarUi((s) => s.setDisplayMonth);
  const scrollRef = useRef<HTMLDivElement>(null);
  const lastDisplayKey = useRef(displayMonth.toFormat('yyyy-MM'));
  const programmaticScroll = useRef(false);

  const [renderCenter, setRenderCenter] = useState(displayMonth.startOf('month'));

  const months = useMemo(() => {
    return Array.from({ length: MONTHS_BUFFER * 2 + 1 }, (_, i) =>
      renderCenter.plus({ months: i - MONTHS_BUFFER }),
    );
  }, [renderCenter]);

  const rangeStart = months[0]?.startOf('month').minus({ days: 6 }).toISODate() ?? '';
  const rangeEnd = months[months.length - 1]?.endOf('month').plus({ days: 7 }).toISODate() ?? '';

  const { data: events } = useCalendarEventsQuery(rangeStart, rangeEnd);
  const eventsByDay = useMemo(() => groupEventsByDay(events ?? []), [events]);
  const holidayProvider = useMemo(() => getOrCreateProvider('JP'), []);
  const updateMutation = useUpdateEventMutation();

  const handleEventDrop = useCallback(
    (dragData: CalendarEvent, targetDate: DateTime) => {
      const origStart = DateTime.fromISO(dragData.startAt);
      const origEnd = DateTime.fromISO(dragData.endAt);
      const dayDelta = targetDate.startOf('day').diff(origStart.startOf('day'), 'days').days;
      if (dayDelta === 0) return;
      const newStart = origStart.plus({ days: dayDelta });
      const newEnd = origEnd.plus({ days: dayDelta });
      const timezone = DateTime.local().zoneName;
      updateMutation.mutate({
        eventId: dragData.id,
        calendarId: dragData.calendarId,
        startAt: dragData.allDay ? `${newStart.toISODate()}T00:00:00Z` : (newStart.toISO() ?? ''),
        endAt: dragData.allDay ? `${newEnd.toISODate()}T00:00:00Z` : (newEnd.toISO() ?? ''),
        timezone,
      });
    },
    [updateMutation],
  );

  const scrollToMonth = useCallback((monthKey: string, behavior: ScrollBehavior = 'smooth') => {
    const el = scrollRef.current?.querySelector(`[data-month="${monthKey}"]`);
    if (el && scrollRef.current) {
      programmaticScroll.current = true;
      el.scrollIntoView({ behavior, block: 'start' });
      setTimeout(
        () => {
          programmaticScroll.current = false;
        },
        behavior === 'smooth' ? 600 : 100,
      );
    }
  }, []);

  useEffect(() => {
    const monthKey = displayMonth.toFormat('yyyy-MM');
    if (monthKey === lastDisplayKey.current) return;
    lastDisplayKey.current = monthKey;

    const targetMs = displayMonth.startOf('month').toMillis();
    const firstMs = months[0]?.toMillis() ?? 0;
    const lastMs = months[months.length - 1]?.toMillis() ?? 0;

    if (targetMs < firstMs || targetMs > lastMs) {
      setRenderCenter(displayMonth.startOf('month'));
      requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          scrollToMonth(monthKey, 'instant');
        });
      });
    } else {
      scrollToMonth(monthKey, 'smooth');
    }
  }, [displayMonth, months, scrollToMonth]);

  useEffect(() => {
    const scroll = scrollRef.current;
    if (!scroll) return;

    const handleScroll = () => {
      if (programmaticScroll.current) return;
      const probeOffset = scroll.scrollTop + scroll.clientHeight * 0.35;
      const sections = scroll.querySelectorAll('[data-month-section]');
      for (const section of sections) {
        const el = section as HTMLElement;
        const sectionTop = el.offsetTop;
        const sectionBottom = sectionTop + el.offsetHeight;
        if (sectionTop <= probeOffset && sectionBottom > probeOffset) {
          const monthStr = el.dataset.month;
          if (monthStr && monthStr !== lastDisplayKey.current) {
            lastDisplayKey.current = monthStr;
            const parts = monthStr.split('-');
            const y = Number(parts[0]);
            const m = Number(parts[1]);
            if (!Number.isNaN(y) && !Number.isNaN(m)) {
              setDisplayMonth(DateTime.local(y, m, 1));
            }
          }
          break;
        }
      }
    };

    scroll.addEventListener('scroll', handleScroll, { passive: true });
    return () => scroll.removeEventListener('scroll', handleScroll);
  }, [setDisplayMonth]);

  const initialKey = useRef(displayMonth.toFormat('yyyy-MM'));
  const hasScrolledInitial = useRef(false);
  useEffect(() => {
    if (hasScrolledInitial.current) return;
    const tryScroll = () => {
      const el = scrollRef.current?.querySelector(`[data-month="${initialKey.current}"]`);
      if (
        el &&
        el.getBoundingClientRect().height > 0 &&
        el.getBoundingClientRect().height < 10000
      ) {
        hasScrolledInitial.current = true;
        scrollToMonth(initialKey.current, 'instant');
      } else {
        requestAnimationFrame(tryScroll);
      }
    };
    requestAnimationFrame(tryScroll);
  }, [scrollToMonth]);

  return (
    <div className={styles.wrapper}>
      {/* Sticky weekday header */}
      <div className={styles.weekdayStrip}>
        {SUNDAY_FIRST.map((name, i) => {
          let colorStyle: string;
          if (i === 0) colorStyle = 'var(--nf-cal-sunday)';
          else if (i === 6) colorStyle = 'var(--nf-cal-saturday)';
          else colorStyle = 'var(--nf-color-fg-muted)';
          return (
            <div key={name} className={styles.weekdayCell} style={{ color: colorStyle }}>
              {name}
            </div>
          );
        })}
      </div>

      <div ref={scrollRef} className={styles.scrollArea}>
        {months.map((month, idx) => (
          <Fragment key={month.toFormat('yyyy-MM')}>
            {idx > 0 && <MonthBoundary month={month} />}
            <MonthSection
              month={month}
              eventsByDay={eventsByDay}
              holidayProvider={holidayProvider}
              onEventDrop={handleEventDrop}
            />
          </Fragment>
        ))}
      </div>
    </div>
  );
}
