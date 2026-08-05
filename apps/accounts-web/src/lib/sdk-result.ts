/**
 * One place to ask an openapi-fetch result whether the request actually
 * succeeded.
 *
 * `error` alone does not answer that. openapi-fetch only fills it in
 * when it can parse a body into the operation's declared error schema,
 * so any non-ok response that arrives without one — a 204, a
 * `Content-Length: 0` 403, chi's default 405 for a method the route
 * never registered, a gateway 502 — comes back with `error` undefined
 * and `data` undefined. A handler written as `if (error) { ...fail... }`
 * then falls straight through to its success path.
 *
 * On a settings screen that is how "your session has been signed out"
 * appears over a session that is still live, and how two-factor auth
 * shows as switched off while the server still demands a code — at
 * which point deleting the authenticator app locks the account's owner
 * out of it. The status line is the part that cannot go missing, so it
 * is the part that decides.
 */

/** The shape every openapi-fetch call returns, narrowed to what matters here. */
export interface SdkResult {
  error?: unknown;
  response?: Response;
}

/**
 * Reports whether a call failed, counting a non-2xx status as a failure
 * even when no error body came with it.
 *
 * Call sites keep their own branching on the error code — this only
 * answers the yes/no that has to come first.
 *
 * A result with no response at all is also a failure. Nothing in the
 * browser produces one, so in practice it means a stubbed client, and
 * the safe reading of "the status is unknown" on a security action is
 * that it did not happen.
 */
export function requestFailed(result: SdkResult): boolean {
  const hasError = result.error !== undefined && result.error !== null;
  return hasError || result.response?.ok !== true;
}
