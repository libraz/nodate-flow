import type { Availability, Flexibility, ShowAs } from './types';

/**
 * Derives the availability a viewer should be shown for a slot, from the
 * two independent properties the schema records.
 *
 * `showAs` is the iCalendar free/busy axis: does this time read as taken.
 * `flexibility` is whether the commitment could move. They are genuinely
 * independent — a meeting the owner would happily reschedule and one that
 * cannot move are both `busy` — and collapsing them into a single stored
 * value would put non-iCalendar states into a column every external
 * consumer reads as TRANSP.
 *
 * Collapsing them for *display* is the point. Someone looking for a slot
 * does not want two enums; they want to know whether to ask. The four
 * outcomes below are what a scheduling poll would call ◎ ○ △ ×:
 *
 *  - `open`       the time is free
 *  - `tentative`  something is pencilled in but not confirmed
 *  - `negotiable` confirmed, but the owner has said it can move
 *  - `blocked`    confirmed and fixed
 *
 * Note the asymmetry: `flexibility` only ever softens a busy slot. A free
 * slot is already the best answer, and marking it negotiable would mean
 * nothing.
 */
export function getAvailability(showAs: ShowAs, flexibility: Flexibility): Availability {
  if (showAs === 'free') return 'open';
  if (showAs === 'tentative') return 'tentative';
  // busy | oof
  return flexibility === 'fixed' ? 'blocked' : 'negotiable';
}

/**
 * The mark conventionally used for each availability in a scheduling
 * poll. Exported as data rather than baked into a component so a caller
 * can localise or replace the glyphs without re-deriving the meaning.
 */
export const AVAILABILITY_MARK: Record<Availability, string> = {
  open: '◎',
  tentative: '○',
  negotiable: '△',
  blocked: '×',
};

/**
 * Reduces the slots a person holds over one span to the single
 * availability a viewer should see.
 *
 * The worst answer wins, because an overlapping slot cannot be made
 * better by a second one being free. Passing no slots means nothing is
 * booked, which is `open`.
 */
export function combineAvailability(slots: readonly Availability[]): Availability {
  const RANK: Record<Availability, number> = {
    open: 0,
    tentative: 1,
    negotiable: 2,
    blocked: 3,
  };
  let worst: Availability = 'open';
  for (const slot of slots) {
    if (RANK[slot] > RANK[worst]) worst = slot;
  }
  return worst;
}
