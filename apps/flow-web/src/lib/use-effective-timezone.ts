/**
 * The timezone the calendar surface reads and writes times in.
 *
 * A user's profile timezone was stored, validated and offered in
 * settings, and then read by nothing on the calendar: the grid, the
 * pills, the create dialog and every timestamp used the browser's zone.
 * Someone from Tokyo working in Berlin set "Asia/Tokyo" and saw Berlin
 * everywhere — a setting that did nothing, while the server-side
 * reminders and digests were already honouring it, so the two disagreed
 * about where a day begins.
 *
 * The chain is the same one the API resolves on its side
 * (apps/flow-api/internal/mcp/tools.go resolveUserTimezone): the user's
 * own preference, then the workspace's, then whatever the browser says.
 * Keeping the order identical is the point — a client that resolved it
 * differently would put events in different days from the reminders
 * about them.
 */

import { detectTimezone } from '@nodate-flow/sdk';

interface WorkspaceTimezone {
  id: string;
  timezone?: string | undefined;
}

/**
 * Resolve the effective timezone from an already-loaded profile and
 * workspace list.
 *
 * Written as a plain function rather than a hook that fetches, because
 * every caller on the calendar surface already holds both — adding a
 * query would make the zone arrive a render later than the data it has
 * to interpret, and the grid would paint once in the wrong zone.
 */
export function resolveEffectiveTimezone(
  userTimezone: string | undefined,
  workspaces: readonly WorkspaceTimezone[],
  activeWorkspaceId: string | null | undefined,
): string {
  if (userTimezone) return userTimezone;
  if (activeWorkspaceId) {
    const ws = workspaces.find((w) => w.id === activeWorkspaceId);
    if (ws?.timezone) return ws.timezone;
  }
  return detectTimezone();
}
