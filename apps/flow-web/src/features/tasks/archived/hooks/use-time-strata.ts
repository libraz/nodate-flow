/**
 * useTimeStrata — group archived rows into editorial "chapters".
 *
 * Turns a flat, archived-at-descending list of rows into ordered
 * chapters that act as time shelves on the page:
 *
 *   - thisWeek          (last 7 days, including today)
 *   - earlierThisMonth  (current month, but older than 7 days)
 *   - thisQuarter       (current quarter, but older than the current month)
 *   - thisYear          (current year, but older than the current quarter)
 *   - older             (everything else — including rows with no archivedAt)
 *
 * The hook returns one entry per non-empty bucket so the page never
 * paints an empty chapter header. Rows without an `archivedAt` epoch are
 * filed under `older` so they remain reachable; the chapter classifier
 * is purely client-side and updates on every render that changes the
 * input slice.
 */

import { useMemo } from 'react';

import type { TaskListItem } from '../../api';

export type ChapterId = 'thisWeek' | 'earlierThisMonth' | 'thisQuarter' | 'thisYear' | 'older';

export const CHAPTER_ORDER: readonly ChapterId[] = [
  'thisWeek',
  'earlierThisMonth',
  'thisQuarter',
  'thisYear',
  'older',
] as const;

export interface ChapterGroup {
  id: ChapterId;
  /** i18n key inside the `archive` namespace, e.g. `archive.chapter.thisWeek`. */
  labelKey: `archive.chapter.${ChapterId}`;
  rows: TaskListItem[];
}

interface ChapterBoundaries {
  /** Unix seconds threshold: rows with archivedAt >= this fall in `thisWeek`. */
  weekStart: number;
  monthStart: number;
  quarterStart: number;
  yearStart: number;
}

/** Compute the chapter boundaries (unix seconds) for `now`. */
function boundariesFor(now: Date): ChapterBoundaries {
  const startOfDay = new Date(now);
  startOfDay.setHours(0, 0, 0, 0);
  const week = new Date(startOfDay);
  week.setDate(week.getDate() - 6);
  const month = new Date(now.getFullYear(), now.getMonth(), 1);
  const quarterMonth = Math.floor(now.getMonth() / 3) * 3;
  const quarter = new Date(now.getFullYear(), quarterMonth, 1);
  const year = new Date(now.getFullYear(), 0, 1);
  return {
    weekStart: Math.floor(week.getTime() / 1000),
    monthStart: Math.floor(month.getTime() / 1000),
    quarterStart: Math.floor(quarter.getTime() / 1000),
    yearStart: Math.floor(year.getTime() / 1000),
  };
}

/** Classify a single row into a chapter. */
export function chapterOf(archivedAt: number | undefined, b: ChapterBoundaries): ChapterId {
  if (!archivedAt) return 'older';
  if (archivedAt >= b.weekStart) return 'thisWeek';
  if (archivedAt >= b.monthStart) return 'earlierThisMonth';
  if (archivedAt >= b.quarterStart) return 'thisQuarter';
  if (archivedAt >= b.yearStart) return 'thisYear';
  return 'older';
}

export interface UseTimeStrataOptions {
  /** Override the reference date; defaults to `new Date()`. Tests use this. */
  now?: Date;
}

/**
 * Group rows into chapter buckets, preserving the input order inside
 * each bucket. Returns only non-empty buckets, in canonical order.
 */
export function useTimeStrata(
  rows: readonly TaskListItem[],
  opts?: UseTimeStrataOptions,
): ChapterGroup[] {
  const referenceTime = opts?.now?.getTime();
  return useMemo(() => {
    const now = referenceTime !== undefined ? new Date(referenceTime) : new Date();
    const boundaries = boundariesFor(now);
    const buckets = new Map<ChapterId, TaskListItem[]>();
    for (const id of CHAPTER_ORDER) buckets.set(id, []);
    for (const row of rows) {
      const id = chapterOf(row.archivedAt, boundaries);
      buckets.get(id)?.push(row);
    }
    const out: ChapterGroup[] = [];
    for (const id of CHAPTER_ORDER) {
      const bucket = buckets.get(id) ?? [];
      if (bucket.length === 0) continue;
      out.push({ id, labelKey: `archive.chapter.${id}` as const, rows: bucket });
    }
    return out;
  }, [rows, referenceTime]);
}
