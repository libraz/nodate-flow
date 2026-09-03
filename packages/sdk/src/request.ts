/**
 * The single door between feature code and the HTTP client.
 *
 * Feature modules do not hold the openapi-fetch client. They hand a
 * call to a requester built here, and every failure leaves through one
 * function that runs {@link toApiError}. That is the whole point: the
 * conversion is not something a call site can be trusted to remember,
 * because a call site that forgets it still compiles, still runs, and
 * still looks correct — it just loses the status, the error code, the
 * translation key and the recovery hint on the way out.
 *
 * Three of those four are invisible until something goes wrong in
 * production, so a hand-rolled converter can sit in a feature folder
 * for months reporting "Request failed" for an expired session and a
 * revoked invite alike. Routing every call through here means the
 * conversion cannot be skipped, only replaced — and replacing it means
 * editing this file, where the reason is written down.
 */

import { toApiError } from './api-error.js';
import type { NodateFlowClient } from './client.js';

/**
 * The shape every openapi-fetch call returns, narrowed to what the
 * requester reads. `data` is optional because a 204 is a success with
 * nothing in it.
 */
export interface SdkResult<T> {
  data?: T | undefined;
  error?: unknown;
  response?: Response | undefined;
}

/**
 * Reports whether a call failed, counting a non-2xx status as a failure
 * even when no error body came with it.
 *
 * `error` alone does not answer that. openapi-fetch only fills it in
 * when it can parse a body into the operation's declared error schema,
 * so any non-ok response that arrives without one — a
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
 *
 * A result with no response at all is also a failure. Nothing in the
 * browser produces one, so in practice it means a stubbed client, and
 * the safe reading of "the status is unknown" on a security action is
 * that it did not happen.
 */
export function requestFailed(result: SdkResult<unknown>): boolean {
  const hasError = result.error !== undefined && result.error !== null;
  return hasError || result.response?.ok !== true;
}

/** A single call against the typed client, deferred so the requester owns the client. */
export type SdkCall<T> = (client: NodateFlowClient) => Promise<SdkResult<T>>;

/**
 * What the requester does when a call fails.
 *
 * `'throw'` is the default and the only mode that lets a failure reach
 * the user. `'empty'` exists for the handful of call sites where not
 * knowing the answer is itself an acceptable answer — a background
 * prefetch, an optional side panel — and it has to be written out,
 * with the value to fall back to, at the call site. Swallowing a
 * failure is a decision; an unmarked `return []` in a catch block is
 * the same decision taken silently, and downstream code cannot tell
 * the two apart.
 *
 * The stand-in value has its own type, so the caller can fall back to
 * `null` and keep the "we did not get an answer" case visible in the
 * result type instead of forging a whole response body.
 */
export type ApiFailurePolicy<E> = { onError?: 'throw' } | { onError: 'empty'; empty: E };

function swallows<E>(policy: ApiFailurePolicy<E>): policy is { onError: 'empty'; empty: E } {
  return policy.onError === 'empty';
}

/** A call plus its response, for the few sites that read headers or the status line. */
export interface ApiRequestResult<T> {
  data: T;
  response: Response;
}

/**
 * The outcome of a call that the caller wants to inspect rather than
 * have decided for it. The failure branch still carries the converted
 * error, so the conversion cannot be skipped by choosing this shape.
 */
export type ApiOutcome<T> =
  | { ok: true; data: T; response: Response }
  | { ok: false; error: unknown; response: Response | undefined };

/** The functions a feature module is allowed to reach the network through. */
export interface ApiRequester {
  /**
   * Runs a call and resolves with its body, converting any failure into
   * an {@link ApiError} carrying the status, code, translation key,
   * extensions and recovery hint.
   *
   * `fallback` is the message used when the failure arrives with no
   * readable envelope — a bodyless 405 or 502 has nothing else to say.
   */
  request<T, E = never>(
    send: SdkCall<T>,
    fallback: string,
    policy?: ApiFailurePolicy<E>,
  ): Promise<T | E>;
  /** As {@link ApiRequester.request}, but also hands back the raw response. */
  requestDetailed<T, E = never>(
    send: SdkCall<T>,
    fallback: string,
    policy?: ApiFailurePolicy<E>,
  ): Promise<ApiRequestResult<T> | E>;
  /**
   * Resolves with the HTTP status of a call and never throws for a
   * non-2xx, for loaders that ask "does this exist" and translate the
   * answer into routing rather than into an error.
   *
   * Returns 0 when the request never reached a server, so callers
   * comparing against a status code cannot mistake a dead network for
   * a definite answer.
   */
  probe(send: SdkCall<unknown>): Promise<number>;
  /**
   * Runs a call and hands back the outcome instead of throwing, for the
   * few call sites that have to read the raw response of a refusal —
   * a `Retry-After` header, say, which no error envelope carries.
   *
   * The failure branch already holds the converted error, so this is a
   * different way of receiving it, not a way of going without it.
   */
  settle<T>(send: SdkCall<T>, fallback: string): Promise<ApiOutcome<T>>;
}

/** Builds the requester bound to one client. */
export function createApiRequester(client: NodateFlowClient): ApiRequester {
  /**
   * Runs the call once and reports the outcome without deciding what to
   * do about it. Both public entry points share this, so the rule that
   * a failure carries a converted `ApiError` is stated once.
   */
  async function settle<T>(send: SdkCall<T>, fallback: string): Promise<ApiOutcome<T>> {
    let result: SdkResult<T>;
    try {
      result = await send(client);
    } catch (cause) {
      // Transport failures (DNS, CORS, an aborted fetch) never produce
      // a result at all, so they cannot be read off `response`, and the
      // original is carried through so callers can still recognise it.
      return { ok: false, error: cause, response: undefined };
    }
    if (requestFailed(result) || result.response === undefined) {
      return {
        ok: false,
        error: toApiError(result.error, fallback, result.response?.status),
        response: result.response,
      };
    }
    return { ok: true, data: result.data as T, response: result.response };
  }

  async function requestDetailed<T, E = never>(
    send: SdkCall<T>,
    fallback: string,
    policy: ApiFailurePolicy<E> = {},
  ): Promise<ApiRequestResult<T> | E> {
    const settled = await settle<T>(send, fallback);
    if (settled.ok) return { data: settled.data, response: settled.response };
    if (swallows(policy)) return policy.empty;
    throw settled.error;
  }

  async function request<T, E = never>(
    send: SdkCall<T>,
    fallback: string,
    policy: ApiFailurePolicy<E> = {},
  ): Promise<T | E> {
    const settled = await settle<T>(send, fallback);
    if (settled.ok) return settled.data;
    if (swallows(policy)) return policy.empty;
    throw settled.error;
  }

  async function probe(send: SdkCall<unknown>): Promise<number> {
    try {
      const result = await send(client);
      return result.response?.status ?? 0;
    } catch {
      return 0;
    }
  }

  return { request, requestDetailed, probe, settle };
}
