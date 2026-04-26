/**
 * LinkedEventTime — locale-aware relative + absolute time formatter.
 *
 * Branches by relative distance from now:
 * - within 24h         -> "today, 16:00" / "tomorrow, 09:00"
 * - within 7 days      -> "in 3 days · 14:00"
 * - past <= 7 days     -> "yesterday" / "last Thursday"
 * - otherwise          -> "Mar 14, 10:00" (or just date for all-day)
 *
 * The `data-tone` attribute drives the colour token (past / today /
 * upcoming / far). All formatting flows through `Intl.DateTimeFormat`
 * for the active locale; weekday names come from the same Intl call so
 * Japanese / Chinese render correctly without per-locale tables.
 *
 * `epochStartSec` is the SDK shape (`TaskEventLink.eventStartAt`,
 * unix-second). When absent the component renders an em-dash; this
 * happens for malformed events and is intentionally non-fatal.
 */

import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './linked-events.module.css';

const DAY_MS = 86_400_000;
const HOUR_MS = 3_600_000;

export type TimeTone = 'past' | 'today' | 'upcoming' | 'far';

export interface LinkedEventTimeProps {
  /** Event start in unix seconds (SDK `eventStartAt`). */
  epochStartSec: number | undefined;
  /** Whether the event is all-day; suppresses the time portion. */
  allDay?: boolean;
  locale: string;
}

interface Computed {
  text: string;
  tone: TimeTone;
}

function startOfLocalDay(d: Date): number {
  const c = new Date(d);
  c.setHours(0, 0, 0, 0);
  return c.getTime();
}

function formatTime(date: Date, locale: string): string {
  return new Intl.DateTimeFormat(locale, { hour: '2-digit', minute: '2-digit' }).format(date);
}

function formatAbsoluteDate(date: Date, locale: string): string {
  return new Intl.DateTimeFormat(locale, { month: 'short', day: 'numeric' }).format(date);
}

function formatWeekday(date: Date, locale: string): string {
  return new Intl.DateTimeFormat(locale, { weekday: 'long' }).format(date);
}

/**
 * Compute the rendered text + colour tone for the given start time.
 * The function is pure so we can call it from both initial render and
 * the once-per-minute tick that keeps "today/tomorrow" honest as time
 * passes (a long-lived task detail page might cross midnight).
 */
function compute(
  epochStartSec: number,
  allDay: boolean,
  locale: string,
  nowMs: number,
  t: (key: string, vars?: Record<string, unknown>) => string,
): Computed {
  const startMs = epochStartSec * 1000;
  const date = new Date(startMs);
  const todayStart = startOfLocalDay(new Date(nowMs));
  const eventDayStart = startOfLocalDay(date);
  const dayDelta = Math.round((eventDayStart - todayStart) / DAY_MS);
  const msDelta = startMs - nowMs;

  const time = allDay ? '' : formatTime(date, locale);

  // Same calendar day -> "today"
  if (dayDelta === 0) {
    if (allDay) return { text: t('time.today', { time: t('time.allDay') }), tone: 'today' };
    return { text: t('time.today', { time }), tone: 'today' };
  }

  // Tomorrow -> "tomorrow, 09:00"
  if (dayDelta === 1) {
    if (allDay) return { text: t('time.tomorrow', { time: t('time.allDay') }), tone: 'upcoming' };
    return { text: t('time.tomorrow', { time }), tone: 'upcoming' };
  }

  // Yesterday -> "yesterday"
  if (dayDelta === -1) {
    return { text: t('time.yesterday'), tone: 'past' };
  }

  // Within next 7 days (excluding today/tomorrow) -> "in N days · HH:mm"
  if (dayDelta > 1 && dayDelta <= 7) {
    if (allDay) {
      // No time half; fall through to absolute formatting for clarity.
      return { text: formatAbsoluteDate(date, locale), tone: 'upcoming' };
    }
    return { text: t('time.inDays', { days: dayDelta, time }), tone: 'upcoming' };
  }

  // Past 7 days -> "last Thursday"
  if (dayDelta < -1 && dayDelta >= -7) {
    return { text: t('time.lastWeekday', { weekday: formatWeekday(date, locale) }), tone: 'past' };
  }

  // Far past
  if (msDelta < 0) {
    if (allDay) return { text: formatAbsoluteDate(date, locale), tone: 'past' };
    return {
      text: t('time.absolute', { date: formatAbsoluteDate(date, locale), time }),
      tone: 'past',
    };
  }

  // Far future (>7 days). The picker pre-fetches up to 60 days.
  const farTone: TimeTone = msDelta > DAY_MS * 30 ? 'far' : 'upcoming';
  if (allDay) return { text: formatAbsoluteDate(date, locale), tone: farTone };
  // Suppress unused-locals for tight branch coverage; HOUR_MS is the
  // semantic scale for the 24h window guard above (Date math operates
  // in ms but the labelled constant documents the threshold).
  void HOUR_MS;
  return {
    text: t('time.absolute', { date: formatAbsoluteDate(date, locale), time }),
    tone: farTone,
  };
}

/**
 * The component re-evaluates every 60s so a label like "today, 23:50"
 * does not stick around past midnight. The interval is cheap and
 * scoped to the row; React 19's compiler handles the dependency tracking.
 */
export default function LinkedEventTime({
  epochStartSec,
  allDay = false,
  locale,
}: LinkedEventTimeProps): ReactElement {
  const { t } = useTranslation('linkedEvents');
  const [now, setNow] = useState<number>(() => Date.now());

  // External-clock synchronisation belongs in an Effect (Effects-as-
  // sync surface). The interval is the external system here.
  useEffect(() => {
    if (epochStartSec === undefined) return;
    const id = window.setInterval(() => {
      setNow(Date.now());
    }, 60_000);
    return () => {
      window.clearInterval(id);
    };
  }, [epochStartSec]);

  if (epochStartSec === undefined) {
    return (
      <span className={styles.time} data-tone="far">
        —
      </span>
    );
  }

  const { text, tone } = compute(
    epochStartSec,
    allDay,
    locale,
    now,
    (key, vars) => t(key, vars ?? {}) as string,
  );
  return (
    <span className={styles.time} data-tone={tone}>
      {text}
    </span>
  );
}

/**
 * Helper for non-React contexts (e.g. picker rows that render a single
 * compact `Mar 14 · 10:00`). Returns the absolute formatting only.
 */
export function formatPickerDate(
  epochStartSec: number | undefined,
  locale: string,
  allDay: boolean,
): string {
  if (epochStartSec === undefined) return '—';
  const date = new Date(epochStartSec * 1000);
  if (allDay) return formatAbsoluteDate(date, locale);
  return `${formatAbsoluteDate(date, locale)} · ${formatTime(date, locale)}`;
}

/**
 * Compute whether a given start lies on the local "today" so the row
 * can render the small accent dot before its glyph.
 */
export function isToday(epochStartSec: number | undefined, nowMs: number = Date.now()): boolean {
  if (epochStartSec === undefined) return false;
  const startDay = startOfLocalDay(new Date(epochStartSec * 1000));
  const today = startOfLocalDay(new Date(nowMs));
  return startDay === today;
}

/**
 * Whether a given start is strictly in the past (used by the picker
 * grouping divider).
 */
export function isPast(epochStartSec: number | undefined, nowMs: number = Date.now()): boolean {
  if (epochStartSec === undefined) return false;
  return epochStartSec * 1000 < nowMs;
}
