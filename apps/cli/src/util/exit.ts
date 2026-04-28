/**
 * Structured exit-code policy for the `tnk` CLI.
 *
 * Scripts driving `tnk` distinguish failure classes by the process
 * exit code:
 *
 *   - `0` — success (set implicitly by Node when no `exitCode` is
 *     written).
 *   - `1` — server / runtime error. The default for any failure that
 *     is not specifically validation or auth (network errors, 4xx /
 *     5xx from the API, unexpected throws). This is also the value
 *     `@libraz/node-cli` sets when an action throws.
 *   - `2` — validation / usage error. Bad flag value (e.g. malformed
 *     date), missing required combination of flags, empty positional
 *     argument.
 *   - `3` — authentication error. No stored credentials or the auth
 *     API rejected the supplied login.
 *
 * Action handlers must use these constants instead of writing literal
 * numbers, so the policy is grep-able and changes in one place.
 */

/** Server / runtime error. The fallback for anything not covered below. */
export const EXIT_RUNTIME = 1 as const;

/** Validation / usage error (bad flag value, missing scope, etc.). */
export const EXIT_VALIDATION = 2 as const;

/** Authentication error (no credentials or auth API rejected login). */
export const EXIT_AUTH = 3 as const;

/** Union of all known structured exit codes. */
export type ExitCode = typeof EXIT_RUNTIME | typeof EXIT_VALIDATION | typeof EXIT_AUTH;

/**
 * Marker error thrown to signal that the CLI should exit with
 * `EXIT_AUTH` (3). `createFlowClient()` throws a plain `Error` when
 * credentials are missing; action handlers catch it and re-throw as
 * `AuthRequiredError` (or check via `isAuthRequiredError`) so the
 * top-level error trap in `index.ts` knows which exit code to set.
 */
export class AuthRequiredError extends Error {
  /**
   * Create a new `AuthRequiredError`.
   *
   * @param message Human-readable explanation written to stderr.
   */
  constructor(message = 'Not logged in. Run `tnk auth login` to authenticate first.') {
    super(message);
    this.name = 'AuthRequiredError';
  }
}

/**
 * Heuristic to decide whether an arbitrary thrown value should be
 * treated as an auth failure. Recognises both `AuthRequiredError`
 * instances and the plain-`Error` shape thrown by
 * `createFlowClient()` (whose message starts with `"Not logged in"`).
 *
 * @param err Value caught from a throwing action.
 */
export function isAuthRequiredError(err: unknown): boolean {
  if (err instanceof AuthRequiredError) return true;
  if (err instanceof Error && err.message.startsWith('Not logged in')) return true;
  return false;
}
