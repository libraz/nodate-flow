/**
 * Workspace-invite link construction.
 *
 * The path lives here once and is typed against the generated route
 * tree, so renaming or removing the route breaks the typecheck instead
 * of shipping a link to a 404. The API builds the same link for invite
 * emails from its own single constant and a test there reads this route
 * tree to prove the two still agree.
 */

import type { FileRouteTypes } from '../../routeTree.gen';

/**
 * Public route that accepts a workspace invite token. Singular — the
 * plural `/invites/accept` next to it is the calendar event RSVP page.
 */
export const WORKSPACE_INVITE_ROUTE = '/invite/$token' satisfies FileRouteTypes['fullPaths'];

/** Path (no origin) an invitee follows to join a workspace. */
export function workspaceInvitePath(token: string): string {
  return WORKSPACE_INVITE_ROUTE.replace('$token', encodeURIComponent(token));
}

/** Absolute link to share for a workspace invite token. */
export function workspaceInviteUrl(origin: string, token: string): string {
  return `${origin.replace(/\/+$/, '')}${workspaceInvitePath(token)}`;
}
