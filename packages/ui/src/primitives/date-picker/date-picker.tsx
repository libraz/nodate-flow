/**
 * DatePicker — dropdown calendar for selecting a date.
 *
 * Fully controlled via `value` / `onChange` with ISO date string "YYYY-MM-DD".
 * No external date library dependency — uses native Date for calendar math.
 * Weekday and month labels are injected via props for i18n.
 */

import { type ReactElement, useCallback, useMemo, useState } from 'react';
import { cx } from '../../lib/cx';
import Popover from '../popover/popover';
import styles from './date-picker.module.css';

export interface DatePickerProps {
  /** Current value in "YYYY-MM-DD" format. */
  value: string;
  /** Called when the user selects a date. */
  onChange: (date: string) => void;
  /** Minimum selectable date in "YYYY-MM-DD" format. */
  minDate?: string;
  /** Short weekday labels starting from Sunday (7 items). */
  weekdayLabels?: string[];
  /** Format function for the month/year header. Receives (year, month 1-12). */
  formatMonthYear?: (year: number, month: number) => string;
  /** Custom trigger label. Defaults to the value. */
  triggerLabel?: string;
  /** Additional class on the trigger button. */
  className?: string;
}

const DEFAULT_WEEKDAYS = ['Su', 'Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa'];

const MONTH_NAMES = [
  'January',
  'February',
  'March',
  'April',
  'May',
  'June',
  'July',
  'August',
  'September',
  'October',
  'November',
  'December',
];

function defaultFormatMonthYear(year: number, month: number): string {
  return `${MONTH_NAMES[month - 1]} ${year}`;
}

function toIso(year: number, month: number, day: number): string {
  return `${year}-${String(month).padStart(2, '0')}-${String(day).padStart(2, '0')}`;
}

function parseIso(iso: string): { year: number; month: number; day: number } {
  const [y, m, d] = iso.split('-').map(Number);
  return { year: y ?? 2024, month: m ?? 1, day: d ?? 1 };
}

function daysInMonth(year: number, month: number): number {
  return new Date(year, month, 0).getDate();
}

function startDayOfWeek(year: number, month: number): number {
  return new Date(year, month - 1, 1).getDay();
}

function todayIso(): string {
  const d = new Date();
  return toIso(d.getFullYear(), d.getMonth() + 1, d.getDate());
}

/** DatePicker renders a popover with a calendar grid. */
export default function DatePicker({
  value,
  onChange,
  minDate,
  weekdayLabels = DEFAULT_WEEKDAYS,
  formatMonthYear = defaultFormatMonthYear,
  triggerLabel,
  className,
}: DatePickerProps): ReactElement {
  const [open, setOpen] = useState(false);
  const parsed = useMemo(() => parseIso(value), [value]);
  const [viewYear, setViewYear] = useState(parsed.year);
  const [viewMonth, setViewMonth] = useState(parsed.month);

  // Sync view when value changes externally
  const prevValue = useMemo(() => value, [value]);
  if (prevValue !== value) {
    const p = parseIso(value);
    if (p.year !== viewYear || p.month !== viewMonth) {
      setViewYear(p.year);
      setViewMonth(p.month);
    }
  }

  const days = useMemo(() => daysInMonth(viewYear, viewMonth), [viewYear, viewMonth]);
  const startDay = useMemo(() => startDayOfWeek(viewYear, viewMonth), [viewYear, viewMonth]);
  const today = useMemo(todayIso, []);

  const goPrev = useCallback(() => {
    setViewMonth((m) => {
      if (m === 1) {
        setViewYear((y) => y - 1);
        return 12;
      }
      return m - 1;
    });
  }, []);

  const goNext = useCallback(() => {
    setViewMonth((m) => {
      if (m === 12) {
        setViewYear((y) => y + 1);
        return 1;
      }
      return m + 1;
    });
  }, []);

  const handleSelect = useCallback(
    (day: number) => {
      const iso = toIso(viewYear, viewMonth, day);
      if (minDate && iso < minDate) return;
      onChange(iso);
      setOpen(false);
    },
    [viewYear, viewMonth, minDate, onChange],
  );

  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      placement="bottom-start"
      content={
        <div className={styles.panel}>
          {/* Header: prev / month-year / next */}
          <div className={styles.header}>
            <button
              type="button"
              className={styles.navBtn}
              onClick={goPrev}
              aria-label="Previous month"
            >
              <svg
                width="16"
                height="16"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                aria-hidden="true"
                role="img"
              >
                <title>Previous</title>
                <path d="M15 18l-6-6 6-6" />
              </svg>
            </button>
            <span className={styles.monthYear}>{formatMonthYear(viewYear, viewMonth)}</span>
            <button
              type="button"
              className={styles.navBtn}
              onClick={goNext}
              aria-label="Next month"
            >
              <svg
                width="16"
                height="16"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                aria-hidden="true"
                role="img"
              >
                <title>Next</title>
                <path d="M9 18l6-6-6-6" />
              </svg>
            </button>
          </div>

          {/* Weekday labels */}
          <div className={styles.weekdays}>
            {weekdayLabels.map((wd) => (
              <div key={wd} className={styles.weekday}>
                {wd}
              </div>
            ))}
          </div>

          {/* Day grid */}
          <div className={styles.dayGrid} role="grid">
            {startDay > 0 && <div style={{ gridColumn: `span ${startDay}` }} />}
            {Array.from({ length: days }, (_, i) => {
              const day = i + 1;
              const iso = toIso(viewYear, viewMonth, day);
              const isSelected = iso === value;
              const isToday = iso === today;
              const isDisabled = minDate ? iso < minDate : false;
              return (
                <button
                  key={day}
                  type="button"
                  disabled={isDisabled}
                  className={cx(
                    styles.day,
                    isSelected && styles.daySelected,
                    isToday && !isSelected && styles.dayToday,
                  )}
                  onClick={() => handleSelect(day)}
                >
                  {day}
                </button>
              );
            })}
          </div>
        </div>
      }
    >
      <button
        type="button"
        className={className ? `${styles.trigger} ${className}` : styles.trigger}
      >
        {triggerLabel ?? value}
      </button>
    </Popover>
  );
}
