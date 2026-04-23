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
  /**
   * Short weekday labels (7 items). The order MUST match `weekStart`:
   * - `weekStart='monday'` (default) -> `['Mon','Tue','Wed','Thu','Fri','Sat','Sun']`
   * - `weekStart='sunday'`           -> `['Sun','Mon','Tue','Wed','Thu','Fri','Sat']`
   */
  weekdayLabels?: string[];
  /** Format function for the month/year header. Receives (year, month 1-12). */
  formatMonthYear?: (year: number, month: number) => string;
  /**
   * Localized aria-label (and visible text, if any) for the "previous month"
   * nav button. Required to keep the primitive free of i18next / locale state.
   */
  prevLabel: string;
  /**
   * Localized aria-label (and visible text, if any) for the "next month"
   * nav button. Required to keep the primitive free of i18next / locale state.
   */
  nextLabel: string;
  /**
   * First day of the week shown in the weekday row and grid layout.
   * Defaults to `'monday'` to match the main `/calendar` view and the
   * Japanese / most-of-Europe convention.
   */
  weekStart?: 'sunday' | 'monday';
  /** Custom trigger label. Defaults to the value. */
  triggerLabel?: string;
  /** Additional class on the trigger button. */
  className?: string;
}

const DEFAULT_WEEKDAYS_MONDAY = ['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su'];
const DEFAULT_WEEKDAYS_SUNDAY = ['Su', 'Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa'];

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

/**
 * Parse a "YYYY-MM-DD" ISO date string into its components.
 *
 * @param iso - ISO date string (e.g. "2026-06-15").
 * @returns The parsed year/month/day, or `null` if the input is empty or not
 *          a well-formed "YYYY-MM-DD" triple with finite numeric parts.
 *
 * @remarks
 * `""`, `"not-a-date"`, `"2026-"`, `"0-0-0"`, etc. all return `null` so callers
 * can cleanly fall back to a default (typically today) via `?? fallback`.
 * Note that nullish coalescing alone does not replace `NaN`, which is why this
 * helper validates explicitly instead of defaulting numeric parts.
 */
function parseIso(iso: string): { year: number; month: number; day: number } | null {
  const parts = iso.split('-');
  if (parts.length !== 3) return null;
  const [ys, ms, ds] = parts;
  if (ys === undefined || ms === undefined || ds === undefined) return null;
  const y = Number(ys);
  const m = Number(ms);
  const d = Number(ds);
  if (!Number.isFinite(y) || !Number.isFinite(m) || !Number.isFinite(d)) return null;
  if (y <= 0 || m < 1 || m > 12 || d < 1 || d > 31) return null;
  return { year: y, month: m, day: d };
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
  weekdayLabels,
  formatMonthYear = defaultFormatMonthYear,
  prevLabel,
  nextLabel,
  weekStart = 'monday',
  triggerLabel,
  className,
}: DatePickerProps): ReactElement {
  const resolvedWeekdayLabels =
    weekdayLabels ?? (weekStart === 'monday' ? DEFAULT_WEEKDAYS_MONDAY : DEFAULT_WEEKDAYS_SUNDAY);
  const [open, setOpen] = useState(false);
  // Fall back to today (not a hardcoded year) when value is empty or malformed
  // so the picker always opens on a sensible month/year. See parseIso() docs.
  const initial = useMemo(() => {
    const p = parseIso(value);
    if (p) return p;
    const now = new Date();
    return { year: now.getFullYear(), month: now.getMonth() + 1, day: now.getDate() };
  }, [value]);
  const [viewYear, setViewYear] = useState(initial.year);
  const [viewMonth, setViewMonth] = useState(initial.month);

  // Sync view when value changes externally
  const prevValue = useMemo(() => value, [value]);
  if (prevValue !== value) {
    const p = parseIso(value);
    if (p && (p.year !== viewYear || p.month !== viewMonth)) {
      setViewYear(p.year);
      setViewMonth(p.month);
    }
  }

  const days = useMemo(() => daysInMonth(viewYear, viewMonth), [viewYear, viewMonth]);
  const rawStartDay = useMemo(() => startDayOfWeek(viewYear, viewMonth), [viewYear, viewMonth]);
  // Shift the Sun=0..Sat=6 day-of-week into the number of leading empty cells
  // according to weekStart. Monday-first: Sun=6, Mon=0, ..., Sat=5.
  const leadingEmpty = weekStart === 'monday' ? (rawStartDay + 6) % 7 : rawStartDay;
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
            <button type="button" className={styles.navBtn} onClick={goPrev} aria-label={prevLabel}>
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
                <title>{prevLabel}</title>
                <path d="M15 18l-6-6 6-6" />
              </svg>
            </button>
            <span className={styles.monthYear}>{formatMonthYear(viewYear, viewMonth)}</span>
            <button type="button" className={styles.navBtn} onClick={goNext} aria-label={nextLabel}>
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
                <title>{nextLabel}</title>
                <path d="M9 18l6-6-6-6" />
              </svg>
            </button>
          </div>

          {/* Weekday labels */}
          <div className={styles.weekdays}>
            {resolvedWeekdayLabels.map((wd, i) => (
              <div key={`${i}-${wd}`} className={styles.weekday}>
                {wd}
              </div>
            ))}
          </div>

          {/* Day grid */}
          <div className={styles.dayGrid} role="grid">
            {leadingEmpty > 0 && <div style={{ gridColumn: `span ${leadingEmpty}` }} />}
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
