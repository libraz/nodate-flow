/**
 * The timezone this client reads and writes times in.
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
 * (apps/flow-api/internal/http/handlers/calendars/resolve.go): the
 * user's own preference, then the workspace's, then whatever the
 * browser says. Keeping the order identical is the point — a client
 * that resolved it differently would put events in different days from
 * the reminders about them.
 *
 * Both entry points hand back a resolved {@link Zone} rather than a
 * string. A `string` timezone is exactly as easy to omit as to pass, and
 * a call site that omits it inherits the browser's zone without saying
 * so; a `Zone` has to be constructed, so a write path that forgets one
 * does not compile.
 */

import { Zone } from '@nodate-flow/ui/time';

import { selectUser, useAuth } from '../features/auth/auth-store';

interface WorkspaceTimezone {
  id: string;
  timezone?: string | undefined;
}

/**
 * Resolve the effective zone from an already-loaded profile and
 * workspace list.
 *
 * Written as a plain function rather than a hook that fetches, because
 * the calendar surface already holds both — adding a query would make
 * the zone arrive a render later than the data it has to interpret, and
 * the grid would paint once in the wrong zone.
 */
export function resolveEffectiveZone(
  userTimezone: string | undefined,
  workspaces: readonly WorkspaceTimezone[],
  activeWorkspaceId: string | null | undefined,
): Zone {
  const workspaceTimezone = activeWorkspaceId
    ? workspaces.find((w) => w.id === activeWorkspaceId)?.timezone
    : undefined;
  return Zone.resolve(userTimezone, workspaceTimezone, Zone.browser().name);
}

/**
 * The effective zone for a surface that does not already hold the
 * profile — a popover, a picker inside a settings page.
 *
 * Reads the session rather than issuing a query, the same trade the
 * week-start preference makes: these controls open inside dialogs and
 * table rows, and suspending one of those on a profile fetch would swap
 * a wrong day boundary for a blank panel.
 *
 * That leaves out the workspace step of the chain, which the session
 * cannot answer. It costs nothing in practice: `users.timezone` is NOT
 * NULL with a UTC default, so a signed-in session always carries a
 * timezone and the workspace step is only ever reached by a caller that
 * holds a profile with the field genuinely absent. Callers that do hold
 * the workspace list should use {@link resolveEffectiveZone}.
 */
export function useEffectiveZone(): Zone {
  const user = useAuth(selectUser);
  return Zone.resolve(user?.timezone, Zone.browser().name);
}
