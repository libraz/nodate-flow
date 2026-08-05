/** Local-time YYYY-MM-DD for the start of `d`. */
export function dateKey(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
}

/**
 * `YYYY-MM-DD` for a calendar event's start, read in the frame the event
 * is stored in.
 *
 * All-day events are dates, not intervals on the world clock: "5 August"
 * is the same square for everyone. The API stores them at midnight UTC
 * precisely so there is one answer, and reading them with local getters
 * undoes that — a Tokyo user's company holiday reappears on the 4th for
 * a viewer in Europe, which is the bug this pairing exists to close.
 *
 * Timed events are the opposite case: 14:00 UTC genuinely is a different
 * hour, and often a different day, depending on where you are. Those
 * stay local.
 */
export function eventDateKey(unixSeconds: number, allDay: boolean): string {
  const d = new Date(unixSeconds * 1000);
  if (!allDay) return dateKey(d);
  const y = d.getUTCFullYear();
  const m = String(d.getUTCMonth() + 1).padStart(2, '0');
  const day = String(d.getUTCDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
}

/**
 * Local midnight `Date` for the day an event's instant belongs to, using
 * the same frame rule as [eventDateKey].
 *
 * The returned Date is always in local time, because the grid it feeds
 * lays out local day columns; only the choice of *which* day is read in
 * UTC for all-day rows.
 */
export function eventStartOfDay(unixSeconds: number, allDay: boolean): Date {
  const key = eventDateKey(unixSeconds, allDay);
  const [y, m, day] = key.split('-').map(Number);
  return new Date(y ?? 1970, (m ?? 1) - 1, day ?? 1, 0, 0, 0, 0);
}
