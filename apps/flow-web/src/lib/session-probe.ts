/**
 * Which routes are allowed to re-establish a session on mount.
 *
 * Most of the app lives under `_authenticated`, which runs the session
 * probe itself. The routes outside it split in two:
 *
 *   - Pages that render differently once a session exists — `/invite/$token`
 *     above all, since invite links are followed from mail and chat and
 *     therefore land in a browser context holding the refresh cookie but
 *     no in-memory session. Without a probe these decide "signed out"
 *     about someone who is signed in.
 *   - Public views (shared calendars, embeds, public lenses, the
 *     calendar-invite RSVP page) plus the login and signup screens, which
 *     render the same thing either way. A probe there is a refresh POST
 *     that is expected to fail, charged against the caller's rate limit,
 *     on every single view.
 *
 * The second group is listed below; everything else probes.
 */

/** Route prefixes that render identically with or without a session. */
const SESSION_PROBE_EXEMPT_PREFIXES: readonly string[] = [
  '/share/cal/',
  '/embed/cal/',
  '/public/lenses/',
  '/invites/accept',
  '/login',
  '/signup',
];

/**
 * Whether the session probe should run for the given pathname.
 *
 * Note that `/invite/$token` (workspace join) and `/invites/accept`
 * (calendar RSVP) differ by one character and want opposite answers, so
 * the exempt entry for the latter is spelled out in full rather than as
 * an `/invite` prefix.
 */
export function shouldProbeSession(pathname: string): boolean {
  return !SESSION_PROBE_EXEMPT_PREFIXES.some((prefix) => pathname.startsWith(prefix));
}
