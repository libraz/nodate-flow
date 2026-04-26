/**
 * AttendeesTab — Attendees pane of the calendar event detail page.
 *
 * Reuses the existing {@link AttendeesSection} component from the
 * calendar-events feature folder so the data-fetching layer
 * ({@link useAttendeesQuery}, {@link useAddAttendeesMutation},
 * {@link useUpdateOwnRsvpMutation} etc.) is not duplicated. The same
 * section already drives the EventDialog attendees row, so any future
 * UX iteration (e.g. richer RSVP picker) updates both surfaces at once.
 *
 * `EventResponse` does not surface `ownerUserId` (see the SDK's
 * generated `components['schemas']['EventResponse']`), so the owner-only
 * controls are intentionally degraded: passing `ownerUserId={null}`
 * disables the can-edit toggle, the per-attendee "Send invite" button,
 * and remove for non-self rows. RSVP and self-row remove still work for
 * any actor who is themselves an attendee.
 */

import type { ReactElement } from 'react';

import { selectUser, useAuth } from '../auth/auth-store';
import AttendeesSection from '../calendar-events/attendees-section';

export interface AttendeesTabProps {
  workspaceId: string;
  calendarId: string;
  eventId: string;
}

/**
 * AttendeesTab — see file-level docstring. Resolves the current actor's
 * id from the auth store and forwards everything else to
 * {@link AttendeesSection}.
 */
export default function AttendeesTab({
  workspaceId,
  calendarId,
  eventId,
}: AttendeesTabProps): ReactElement {
  const currentUser = useAuth(selectUser);
  const selfUserId = currentUser?.id ?? '';

  return (
    <AttendeesSection
      workspaceId={workspaceId}
      calendarId={calendarId}
      eventId={eventId}
      ownerUserId={null}
      selfUserId={selfUserId}
    />
  );
}
