/**
 * The signed-in user's first-day-of-week preference, in the vocabulary
 * the DatePicker primitive speaks.
 *
 * The `/calendar` grid has always honoured `me.weekStart`, including
 * Saturday. Every date picker in the product ignored it and laid out a
 * Monday week, so the same account saw two different weeks depending on
 * which surface it was looking at.
 *
 * Reads the session rather than issuing a query: pickers open inside
 * dialogs and table rows, and suspending those on a profile fetch would
 * trade one defect for a worse one.
 */

import type { WeekStartDay } from '@nodate-flow/ui/primitives/date-picker';

import { selectUser, useAuth } from '../features/auth/auth-store';

/** Server-side `me.weekStart` values. */
type StoredWeekStart = 'mon' | 'sun' | 'sat';

const TO_WEEK_START_DAY: Record<StoredWeekStart, WeekStartDay> = {
  mon: 'monday',
  sun: 'sunday',
  sat: 'saturday',
};

/**
 * Map a stored preference onto the primitive's vocabulary.
 *
 * Falls back to Monday, which is what the picker already defaulted to —
 * an account with no stored preference keeps rendering exactly as before.
 */
export function toWeekStartDay(stored: string | undefined): WeekStartDay {
  return TO_WEEK_START_DAY[stored as StoredWeekStart] ?? 'monday';
}

/** The current user's week-start preference for DatePicker. */
export function useWeekStart(): WeekStartDay {
  const user = useAuth(selectUser);
  return toWeekStartDay(user?.weekStart);
}
