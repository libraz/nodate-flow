/**
 * /workspaces/$id/calendars/$calId/events/$evtId — lazy route. Hosts the
 * deep-linkable calendar event detail page (header + Attendees / Invites
 * tabs). The page component owns its own Suspense boundaries per tab so
 * a failure in one pane does not blank the other.
 */

import { createLazyFileRoute } from '@tanstack/react-router';

import EventDetailPage from '../features/events/event-detail-page';

export const Route = createLazyFileRoute(
  '/_authenticated/workspaces/$id/calendars/$calId/events/$evtId',
)({
  component: EventDetailPage,
});
