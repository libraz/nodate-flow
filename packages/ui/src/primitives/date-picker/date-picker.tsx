/**
 * DatePicker — dropdown calendar for selecting a date.
 *
 * Fully controlled via `value` / `onChange` with ISO date string "YYYY-MM-DD".
 * No external date library dependency — uses native Date for calendar math.
 * Weekday and month labels are injected via props for i18n.
 */

import {
  type KeyboardEvent,
  type ReactElement,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
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
   * Defaults to `'monday'` to match the Japanese / most-of-Europe
   * convention. Saturday is included because the product offers it as a
   * user preference, and a picker that cannot express the setting the
   * user saved just renders somebody else's week.
   */
  weekStart?: WeekStartDay;
  /** Custom trigger label. Defaults to the value. */
  triggerLabel?: string;
  /** Additional class on the trigger button. */
  className?: string;
  /**
   * Optional handler that, when provided, renders a "Clear" affordance in the
   * popover footer for unsetting the current selection (e.g. clearing a
   * task's due date back to null). Closes the popover after firing.
   * Callers own the semantics of "clear" — typically passing `null` or `''`
   * to their save handler.
   */
  onClear?: () => void;
  /**
   * Localized label for the optional "Clear" button. Required when `onClear`
   * is provided. Kept as a required prop on the primitive so the design
   * system stays free of i18next state.
   */
  clearLabel?: string;
}

/** First day of the week the grid is laid out from. */
export type WeekStartDay = 'sunday' | 'monday' | 'saturday';

/** `Date.getDay()` value (Sun=0..Sat=6) each anchor corresponds to. */
const WEEK_START_DOW: Record<WeekStartDay, number> = { sunday: 0, monday: 1, saturday: 6 };

const CANONICAL_WEEKDAYS = ['Su', 'Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa'];

/** Default short labels rotated so index 0 is the requested start day. */
function defaultWeekdays(weekStart: WeekStartDay): string[] {
  const offset = WEEK_START_DOW[weekStart];
  return Array.from({ length: 7 }, (_, i) => CANONICAL_WEEKDAYS[(i + offset) % 7] as string);
}

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

/** One cell of the day grid. `day` is null for the out-of-month padding. */
interface DayCell {
  /** Stable key: the ISO date the cell stands on, padding included. */
  key: string;
  /** Day-of-month, or null when the cell belongs to a neighbouring month. */
  day: number | null;
}

/**
 * Lay the month out as full seven-cell rows.
 *
 * A `role="grid"` has to contain rows of cells — a flat run of buttons is
 * announced as a list of numbers with no week structure, and it is what left
 * keyboard users tabbing through up to 31 stops to reach the end of a month.
 * The leading and trailing padding cells are emitted (rather than collapsed
 * into one spanning element) so every row really does have seven cells.
 */
function buildWeeks(year: number, month: number, weekStart: WeekStartDay): DayCell[][] {
  const total = daysInMonth(year, month);
  const leading = (startDayOfWeek(year, month) - WEEK_START_DOW[weekStart] + 7) % 7;
  const rows = Math.ceil((leading + total) / 7);
  const weeks: DayCell[][] = [];
  for (let row = 0; row < rows; row += 1) {
    const cells: DayCell[] = [];
    for (let col = 0; col < 7; col += 1) {
      const day = row * 7 + col - leading + 1;
      // Date normalises out-of-range days into the neighbouring month, which
      // is exactly what the padding cells need for a unique, stable key.
      const spill = new Date(year, month - 1, day);
      cells.push({
        key: toIso(spill.getFullYear(), spill.getMonth() + 1, spill.getDate()),
        day: day >= 1 && day <= total ? day : null,
      });
    }
    weeks.push(cells);
  }
  return weeks;
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
  onClear,
  clearLabel,
}: DatePickerProps): ReactElement {
  const resolvedWeekdayLabels = weekdayLabels ?? defaultWeekdays(weekStart);
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

  /*
   * Move the visible month when `value` changes from outside.
   *
   * The previous value has to be held in state. It was held in a
   * `useMemo(() => value, [value])`, which returns the new value the
   * moment `value` changes — so the comparison below was `value !==
   * value` and the whole block was unreachable. A date set from anywhere
   * other than this calendar (a form reset, a "today" shortcut, a value
   * arriving with the record) left the picker showing whatever month it
   * happened to be on, with the selected day off screen.
   */
  const [prevValue, setPrevValue] = useState(value);
  // Day the roving tabindex sits on. The grid contributes exactly one tab
  // stop; everything else inside it is reached with the arrow keys.
  const [focusedDay, setFocusedDay] = useState(initial.day);
  if (prevValue !== value) {
    setPrevValue(value);
    const p = parseIso(value);
    if (p) {
      setFocusedDay(p.day);
      if (p.year !== viewYear || p.month !== viewMonth) {
        setViewYear(p.year);
        setViewMonth(p.month);
      }
    }
  }

  const days = useMemo(() => daysInMonth(viewYear, viewMonth), [viewYear, viewMonth]);
  const weeks = useMemo(
    () => buildWeeks(viewYear, viewMonth, weekStart),
    [viewYear, viewMonth, weekStart],
  );
  // Shift the Sun=0..Sat=6 day-of-week into the number of leading empty
  // cells, counting from whichever day the week starts on.
  const leadingEmpty = (startDayOfWeek(viewYear, viewMonth) - WEEK_START_DOW[weekStart] + 7) % 7;
  const today = useMemo(todayIso, []);
  // Paging to a shorter month must not leave the tab stop on a day that no
  // longer exists, which would drop the grid out of the tab order entirely.
  const rovingDay = Math.min(Math.max(focusedDay, 1), days);

  const dayRefs = useRef(new Map<number, HTMLButtonElement>());
  // Set when a key press moves focus to a day that is not on screen yet
  // (month or year boundary), and consumed once the new month has rendered.
  const pendingFocusRef = useRef<number | null>(null);
  useEffect(() => {
    const day = pendingFocusRef.current;
    if (day === null) return;
    pendingFocusRef.current = null;
    dayRefs.current.get(day)?.focus();
  });

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

  /** Move the roving focus to a concrete calendar date, paging if needed. */
  const focusDate = useCallback(
    (target: Date) => {
      const y = target.getFullYear();
      const m = target.getMonth() + 1;
      const d = target.getDate();
      setFocusedDay(d);
      if (y !== viewYear || m !== viewMonth) {
        setViewYear(y);
        setViewMonth(m);
      }
      pendingFocusRef.current = d;
    },
    [viewYear, viewMonth],
  );

  /**
   * Arrow-key navigation over the grid, per the WAI-ARIA date-picker
   * pattern. Movement crosses month and year boundaries so the whole
   * calendar is reachable without touching the nav buttons.
   */
  const handleGridKeyDown = useCallback(
    (event: KeyboardEvent<HTMLButtonElement>, day: number) => {
      if (event.altKey || event.ctrlKey || event.metaKey) return;
      const column = (leadingEmpty + day - 1) % 7;
      const rtl =
        event.currentTarget.closest('[dir="rtl"]') !== null ||
        (typeof document !== 'undefined' && document.documentElement.dir === 'rtl');
      let byDays: number | null = null;
      let byMonths: number | null = null;
      switch (event.key) {
        case 'ArrowLeft':
          byDays = rtl ? 1 : -1;
          break;
        case 'ArrowRight':
          byDays = rtl ? -1 : 1;
          break;
        case 'ArrowUp':
          byDays = -7;
          break;
        case 'ArrowDown':
          byDays = 7;
          break;
        case 'Home':
          byDays = -column;
          break;
        case 'End':
          byDays = 6 - column;
          break;
        case 'PageUp':
          byMonths = event.shiftKey ? -12 : -1;
          break;
        case 'PageDown':
          byMonths = event.shiftKey ? 12 : 1;
          break;
        default:
          return;
      }
      event.preventDefault();
      if (byDays !== null) {
        focusDate(new Date(viewYear, viewMonth - 1, day + byDays));
        return;
      }
      if (byMonths !== null) {
        // Clamp so 31 January + 1 month lands on 28/29 February rather than
        // silently spilling into March.
        const targetMonth = viewMonth - 1 + byMonths;
        const targetYear = viewYear + Math.floor(targetMonth / 12);
        const normalizedMonth = ((targetMonth % 12) + 12) % 12;
        const clamped = Math.min(day, daysInMonth(targetYear, normalizedMonth + 1));
        focusDate(new Date(targetYear, normalizedMonth, clamped));
      }
    },
    [focusDate, leadingEmpty, viewMonth, viewYear],
  );

  const handleClear = useCallback(() => {
    onClear?.();
    setOpen(false);
  }, [onClear]);

  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      placement="bottom-start"
      ariaLabel={formatMonthYear(viewYear, viewMonth)}
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
            <span className={styles.monthYear} aria-live="polite">
              {formatMonthYear(viewYear, viewMonth)}
            </span>
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

          {/* Day grid: weekday header row + one row per week */}
          <div
            className={styles.dayGrid}
            role="grid"
            aria-label={formatMonthYear(viewYear, viewMonth)}
          >
            <div className={styles.weekdays} role="row">
              {resolvedWeekdayLabels.map((wd, i) => {
                // Canonical Sunday-based day-of-week (0=Sun..6=Sat) for a stable key
                // independent of label content or week-start offset.
                const dow = (i + WEEK_START_DOW[weekStart]) % 7;
                return (
                  <div key={`dow-${dow}`} className={styles.weekday} role="columnheader">
                    {wd}
                  </div>
                );
              })}
            </div>
            {weeks.map((week) => (
              // Rows are keyed by the date their first cell stands on, which
              // is unique across months and stable across re-renders.
              <div key={`week-${week[0]?.key ?? ''}`} className={styles.week} role="row">
                {week.map((cell) => {
                  if (cell.day === null) {
                    return <div key={cell.key} className={styles.dayCell} role="gridcell" />;
                  }
                  const day = cell.day;
                  const iso = toIso(viewYear, viewMonth, day);
                  const isSelected = iso === value;
                  const isToday = iso === today;
                  const isDisabled = minDate ? iso < minDate : false;
                  return (
                    <div
                      key={cell.key}
                      className={styles.dayCell}
                      role="gridcell"
                      aria-selected={isSelected}
                    >
                      <button
                        ref={(node) => {
                          if (node) dayRefs.current.set(day, node);
                          else dayRefs.current.delete(day);
                        }}
                        type="button"
                        // `aria-disabled` rather than `disabled`: a disabled
                        // button cannot take focus, so a roving tabindex that
                        // landed on one would strand the keyboard user with no
                        // way back into the grid.
                        aria-disabled={isDisabled || undefined}
                        aria-current={isToday ? 'date' : undefined}
                        tabIndex={day === rovingDay ? 0 : -1}
                        className={cx(
                          styles.day,
                          isSelected && styles.daySelected,
                          isToday && !isSelected && styles.dayToday,
                        )}
                        onClick={() => handleSelect(day)}
                        onFocus={() => setFocusedDay(day)}
                        onKeyDown={(event) => handleGridKeyDown(event, day)}
                      >
                        {day}
                      </button>
                    </div>
                  );
                })}
              </div>
            ))}
          </div>

          {/* Footer: optional clear affordance. Only rendered when the caller
              opts in via `onClear`; otherwise the panel keeps its compact
              calendar-only layout. */}
          {onClear !== undefined && (
            <div className={styles.footer}>
              <button
                type="button"
                className={styles.clearBtn}
                onClick={handleClear}
                aria-label={clearLabel}
              >
                {clearLabel}
              </button>
            </div>
          )}
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
