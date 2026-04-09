/**
 * /calendar — monthly grid of cross-workspace tasks with a due date.
 *
 * Backed by `GET /me/tasks` (the same aggregate endpoint as /today).
 * Renders a 7-column grid for the current month with up to N tasks
 * per cell; overflow collapses to "+N more" but each cell stays
 * scrollable so the user can still drill in. Read-only — no drag.
 */

import type { components } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import { useSuspenseQuery } from '@tanstack/react-query';
import { Link, createFileRoute } from '@tanstack/react-router';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { type ReactElement, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { sdk } from '../lib/sdk';

type AssignedTask = components['schemas']['MyTaskListItem'];

const WEEKDAY_KEYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'] as const;

const STATE_COLOR: Record<string, string> = {
  open: 'var(--color-info, #3498db)',
  waiting: 'var(--color-warning, #f39c12)',
  review: 'var(--color-accent, #9b59b6)',
  done: 'var(--color-success, #27ae60)',
  cancelled: 'var(--color-muted, #95a5a6)',
};

/** Local-time YYYY-MM-DD for the start of `d`. */
function dateKey(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
}

/** Number of days in a given (year, monthIndex). */
function daysInMonth(year: number, monthIndex: number): number {
  return new Date(year, monthIndex + 1, 0).getDate();
}

/** 0..6 with Monday = 0. */
function mondayBasedDow(d: Date): number {
  return (d.getDay() + 6) % 7;
}

interface MonthCell {
  date: Date;
  key: string;
  inMonth: boolean;
}

function buildMonthGrid(year: number, monthIndex: number): MonthCell[] {
  const first = new Date(year, monthIndex, 1);
  const lead = mondayBasedDow(first);
  const cells: MonthCell[] = [];
  // Leading days from the previous month.
  for (let i = lead; i > 0; i--) {
    const d = new Date(year, monthIndex, 1 - i);
    cells.push({ date: d, key: dateKey(d), inMonth: false });
  }
  const total = daysInMonth(year, monthIndex);
  for (let day = 1; day <= total; day++) {
    const d = new Date(year, monthIndex, day);
    cells.push({ date: d, key: dateKey(d), inMonth: true });
  }
  // Trailing days to fill out a 6×7 grid (42 cells max).
  while (cells.length % 7 !== 0) {
    const last = cells[cells.length - 1];
    if (!last) break;
    const d = new Date(last.date);
    d.setDate(d.getDate() + 1);
    cells.push({ date: d, key: dateKey(d), inMonth: false });
  }
  return cells;
}

function CalendarRoute(): ReactElement {
  const { t, i18n } = useTranslation('common');
  const locale = i18n.resolvedLanguage ?? 'en';
  const today = new Date();
  const [cursor, setCursor] = useState<{ year: number; month: number }>({
    year: today.getFullYear(),
    month: today.getMonth(),
  });

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

  const cells = useMemo(() => buildMonthGrid(cursor.year, cursor.month), [cursor]);

  /** dueOn → tasks for the current month grid. */
  const byDate = useMemo(() => {
    const map = new Map<string, AssignedTask[]>();
    for (const task of tasks) {
      if (!task.dueOn) continue;
      if (task.derivedState === 'cancelled') continue;
      const arr = map.get(task.dueOn);
      if (arr) {
        arr.push(task);
      } else {
        map.set(task.dueOn, [task]);
      }
    }
    for (const arr of map.values()) {
      arr.sort((a, b) => b.priority - a.priority);
    }
    return map;
  }, [tasks]);

  const todayKey = dateKey(today);
  const monthLabel = useMemo(
    () =>
      new Intl.DateTimeFormat(locale, { year: 'numeric', month: 'long' }).format(
        new Date(cursor.year, cursor.month, 1),
      ),
    [locale, cursor],
  );

  const goPrev = (): void => {
    setCursor((c) => {
      const m = c.month - 1;
      return m < 0 ? { year: c.year - 1, month: 11 } : { year: c.year, month: m };
    });
  };
  const goNext = (): void => {
    setCursor((c) => {
      const m = c.month + 1;
      return m > 11 ? { year: c.year + 1, month: 0 } : { year: c.year, month: m };
    });
  };
  const goToday = (): void => {
    setCursor({ year: today.getFullYear(), month: today.getMonth() });
  };

  return (
    <section
      style={{
        padding: 'clamp(1.5rem, 4vw, 2.5rem)',
        display: 'flex',
        flexDirection: 'column',
        gap: '1.5rem',
        maxInlineSize: '78rem',
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
          {t('calendar.title')}
        </h1>
        <p style={{ margin: 0, color: 'var(--color-muted)' }}>{t('calendar.subtitle')}</p>
      </header>

      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: '0.75rem',
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            aria-label={t('calendar.prev')}
            onClick={goPrev}
          >
            <ChevronLeft size={16} aria-hidden />
          </Button>
          <h2 style={{ margin: 0, fontSize: '1.125rem', minInlineSize: '10rem' }}>{monthLabel}</h2>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            aria-label={t('calendar.next')}
            onClick={goNext}
          >
            <ChevronRight size={16} aria-hidden />
          </Button>
          <Button type="button" variant="ghost" size="sm" onClick={goToday}>
            {t('calendar.today')}
          </Button>
        </div>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column' }}>
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(7, minmax(0, 1fr))',
            gap: '0.25rem',
            marginBlockEnd: '0.25rem',
          }}
        >
          {WEEKDAY_KEYS.map((wk) => (
            <div
              key={wk}
              style={{
                fontSize: '0.75rem',
                textTransform: 'uppercase',
                letterSpacing: '0.05em',
                color: 'var(--color-muted)',
                paddingInline: '0.5rem',
              }}
            >
              {t(`calendar.weekday.${wk}`)}
            </div>
          ))}
        </div>
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(7, minmax(0, 1fr))',
            gap: '0.25rem',
          }}
        >
          {cells.map((cell) => {
            const dayTasks = byDate.get(cell.key) ?? [];
            const isToday = cell.key === todayKey;
            return (
              <div
                key={cell.key}
                style={{
                  minBlockSize: '7rem',
                  padding: '0.5rem',
                  borderRadius: '0.5rem',
                  background: cell.inMonth
                    ? 'var(--color-surface, rgba(127,127,127,0.05))'
                    : 'transparent',
                  border: isToday
                    ? '1px solid var(--color-accent, #9b59b6)'
                    : '1px solid transparent',
                  opacity: cell.inMonth ? 1 : 0.4,
                  display: 'flex',
                  flexDirection: 'column',
                  gap: '0.25rem',
                  overflow: 'hidden',
                }}
              >
                <div
                  style={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'center',
                    fontSize: '0.75rem',
                    fontVariantNumeric: 'tabular-nums',
                    color: isToday ? 'var(--color-accent, #9b59b6)' : 'var(--color-muted)',
                    fontWeight: isToday ? 600 : 400,
                  }}
                >
                  <span>{cell.date.getDate()}</span>
                  {dayTasks.length > 0 ? <span>{dayTasks.length}</span> : null}
                </div>
                <ul
                  style={{
                    listStyle: 'none',
                    margin: 0,
                    padding: 0,
                    display: 'flex',
                    flexDirection: 'column',
                    gap: '0.125rem',
                    overflow: 'hidden',
                  }}
                >
                  {dayTasks.slice(0, 3).map((task) => (
                    <li key={task.id}>
                      <Link
                        to="/tasks/$taskId"
                        params={{ taskId: task.id }}
                        title={`${task.title} · ${task.workspaceName}`}
                        style={{
                          display: 'flex',
                          alignItems: 'center',
                          gap: '0.25rem',
                          fontSize: '0.75rem',
                          color: 'inherit',
                          textDecoration: 'none',
                          padding: '0.125rem 0.25rem',
                          borderRadius: '0.25rem',
                          background: 'var(--color-bg, rgba(255,255,255,0.04))',
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
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
                        <span
                          style={{
                            overflow: 'hidden',
                            textOverflow: 'ellipsis',
                            whiteSpace: 'nowrap',
                          }}
                        >
                          {task.title}
                        </span>
                      </Link>
                    </li>
                  ))}
                  {dayTasks.length > 3 ? (
                    <li
                      style={{
                        fontSize: '0.6875rem',
                        color: 'var(--color-muted)',
                        paddingInline: '0.25rem',
                      }}
                    >
                      {t('calendar.more', { count: dayTasks.length - 3 })}
                    </li>
                  ) : null}
                </ul>
              </div>
            );
          })}
        </div>
      </div>
    </section>
  );
}

export const Route = createFileRoute('/_authenticated/calendar')({
  component: CalendarRoute,
});
