/**
 * What a calendar drag carries between the surface it started on and the
 * route that writes the move.
 *
 * It lives here rather than in either surface because both month views —
 * the desktop grid and the phone month scroll — press the same gesture
 * and hand it to the same drop handler, and a payload owned by one of
 * them would make the other import a route.
 */

import type { components } from '@nodate-flow/sdk';

type CalendarEvent = components['schemas']['MyCalendarEventResponse'];

/**
 * Active drag payload. A task drag only needs its id and origin day; an
 * event drag carries the full row so the drop handler can shift the
 * start/end range by whole days while preserving duration.
 *
 * `fromDate` is the day the pill was picked up from, not the day the
 * event starts on: an event drawn across several days can be taken hold
 * of on any of them, and the move is the distance the pill travelled.
 *
 * `label` is what the floating copy reads while the drag is in flight —
 * the pill itself stays in the grid, and on touch the copy is the only
 * thing the moving finger has to look at.
 */
export type CalendarDragPayload =
  | { type: 'task'; taskId: string; fromDate: string; label: string; dotColor: string }
  | { type: 'event'; event: CalendarEvent; fromDate: string; label: string; dotColor: string };
