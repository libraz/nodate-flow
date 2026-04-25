/**
 * /invites/accept?token=XXX — public magic-link RSVP page for calendar
 * event invites. No authentication required.
 *
 * This is distinct from `/invite/$token` (workspace join invites); the
 * two flows are intentionally kept separate because they cover different
 * surfaces (calendar event invites vs workspace membership).
 */

import { createFileRoute } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import PublicAcceptInvitePage from '../features/calendar-invites/public-accept-page';

/** Shape of the `/invites/accept` search params. */
export interface AcceptSearch {
  token?: string;
}

function AcceptInviteRoute(): ReactElement {
  const { token } = Route.useSearch();
  return <PublicAcceptInvitePage token={token} />;
}

export const Route = createFileRoute('/invites/accept')({
  validateSearch: (search: Record<string, unknown>): AcceptSearch => {
    const token = typeof search.token === 'string' ? search.token : undefined;
    return token ? { token } : {};
  },
  component: AcceptInviteRoute,
});
