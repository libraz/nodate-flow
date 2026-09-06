/**
 * Token refresh utilities shared across all frontend apps.
 *
 * Provides three building blocks:
 *
 * 1. `createTokenRefresher` -- a memoized refresh function with
 *    grace-window deduplication so concurrent 401s collapse into a
 *    single refresh round-trip.
 *
 * 2. `createRefreshMiddleware` -- an openapi-fetch response middleware
 *    that intercepts 401 responses, attempts a token refresh, and
 *    replays the original request with the rotated token. Used as a
 *    reactive backstop for unexpected 401s (e.g. a token revoked mid
 *    session by the server).
 *
 * 3. `createAuthRequestMiddleware` -- an openapi-fetch **request**
 *    middleware that proactively refreshes the access token when it is
 *    within {@link EXPIRY_BUFFER_SECONDS} of expiry, BEFORE the request
 *    is dispatched. This avoids the console-level 401 noise caused by a
 *    reactive-only refresh (the browser logs a 401 response the instant
 *    it arrives, even when the fetch middleware transparently replays
 *    the request with a fresh token).
 */

import { isTransportFailure } from './api-error.js';

/** Options for creating a token refresh middleware. */
export interface RefreshMiddlewareOptions {
  /** Base URL of the auth-api service (e.g. http://localhost:8082). */
  authApiBaseUrl: string;
  /** Returns the current access token. */
  getAccessToken: () => string | undefined;
  /** Stores a new access token after successful refresh. */
  setAccessToken: (token: string) => void;
  /** Clears the entire auth session on refresh failure. */
  clearSession: () => void;
  /**
   * Optional callback invoked after session is cleared on refresh
   * failure. Useful for redirecting to the login page.
   */
  onSessionExpired?: () => void;
}

/**
 * Grace window (ms) after a refresh resolves during which subsequent
 * 401s reuse the same resolved promise instead of triggering another
 * refresh. This prevents cascading refreshes that would invalidate the
 * single-use refresh cookie.
 */
const REFRESH_GRACE_MS = 1500;

/**
 * How many seconds before a token's `exp` to treat it as "near-expiry"
 * and trigger a proactive refresh. Tokens whose remaining lifetime is
 * below this threshold are refreshed BEFORE the next outbound request
 * instead of waiting for the server to reject them with a 401.
 */
const EXPIRY_BUFFER_SECONDS = 10;

/** Standard base64 alphabet, used by the runtime-agnostic decoder below. */
const BASE64_ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';

/**
 * Decode a padded standard-base64 string to a UTF-8 string without
 * relying on `atob` (browser) or `Buffer` (Node). The SDK is shipped to
 * browser bundles, so it must not depend on Node globals; this pure
 * fallback keeps decoding working in runtimes that lack `atob` (some
 * SSR/test probes) without pulling in `@types/node`.
 *
 * Throws if the input contains characters outside the base64 alphabet,
 * letting {@link decodeTokenExp} treat malformed payloads as "no exp".
 */
function base64ToUtf8(input: string): string {
  const clean = input.replace(/=+$/, '');
  const bytes: number[] = [];
  let buffer = 0;
  let bits = 0;
  for (const char of clean) {
    const value = BASE64_ALPHABET.indexOf(char);
    if (value === -1) throw new Error('invalid base64');
    buffer = (buffer << 6) | value;
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      bytes.push((buffer >> bits) & 0xff);
    }
  }
  // Decode the raw bytes as UTF-8. JWT payloads are JSON, so any
  // multi-byte sequences must be reconstructed before JSON.parse.
  return decodeURIComponent(bytes.map((byte) => `%${byte.toString(16).padStart(2, '0')}`).join(''));
}

/**
 * Decode the `exp` claim from a JWT access token without verifying the
 * signature. Returns the expiry as a unix epoch seconds value, or null
 * if the token is not a well-formed JWT or lacks a numeric `exp`.
 *
 * This is intentionally permissive: callers use the result as a hint to
 * decide whether to refresh proactively, and fall back to the reactive
 * 401 path if decoding fails (e.g. the backend issues opaque tokens).
 */
export function decodeTokenExp(token: string): number | null {
  const parts = token.split('.');
  if (parts.length < 2) return null;
  const payload = parts[1];
  if (!payload) return null;
  try {
    // JWT uses base64url (no padding). Convert to base64 and decode.
    const base64 = payload.replace(/-/g, '+').replace(/_/g, '/');
    const padded = base64 + '='.repeat((4 - (base64.length % 4)) % 4);
    const json =
      typeof atob === 'function'
        ? atob(padded)
        : // Fallback for non-browser runtimes (tests, SSR probes) that
          // lack `atob`. Pure JS so the SDK stays free of Node globals.
          base64ToUtf8(padded);
    const claims = JSON.parse(json) as { exp?: unknown };
    if (typeof claims.exp === 'number' && Number.isFinite(claims.exp)) {
      return claims.exp;
    }
    return null;
  } catch {
    return null;
  }
}

/**
 * Why the most recent refresh returned no token.
 *
 * `network` means the request never reached the server, so nothing is
 * known about the session; `rejected` means the server answered and
 * declined. Only the second is grounds for ending the session.
 */
export type RefreshFailureKind = 'none' | 'network' | 'rejected';

/** Token refresher handle with helpers for expiry-aware request gating. */
export interface TokenRefresher {
  /** Trigger a refresh (deduped within the grace window). */
  (): Promise<string | null>;
  /**
   * Returns true when the current token is missing or within
   * {@link EXPIRY_BUFFER_SECONDS} of its `exp` claim.
   */
  isExpiringSoon: () => boolean;
  /**
   * Returns the cached `exp` claim (unix seconds) for the most recent
   * token observed by this refresher, or null if unknown.
   */
  currentExp: () => number | null;
  /**
   * Why the most recent refresh produced no token. Callers use this to
   * tell "the server says you are signed out" from "we could not ask",
   * which decide opposite things: sign the user out, or offer a retry.
   */
  lastFailure: () => RefreshFailureKind;
}

/**
 * Creates a memoized `refreshAccessToken` function with grace-window
 * deduplication and token-expiry awareness.
 *
 * - Concurrent callers share the same in-flight promise.
 * - After resolution the promise is held for {@link REFRESH_GRACE_MS}
 *   so that 401 responses from requests already in flight with the
 *   pre-rotation token reuse the same new token.
 * - On success: decodes the new token's `exp` claim for later expiry
 *   checks, calls `setAccessToken`, and returns the new token.
 * - On rejection by the server: calls `clearSession`, then
 *   `onSessionExpired` (if provided), and returns `null`.
 * - On a transport failure (the request never reached the server):
 *   returns `null` with the session **left intact**. A connection that
 *   dropped for a moment says nothing about whether the refresh cookie
 *   is still good, and signing someone out over it discards unsaved work
 *   with no way back but a manual reload. Callers distinguish the two
 *   through {@link TokenRefresher.lastFailure}.
 *
 * The returned function carries two helper methods (`isExpiringSoon`,
 * `currentExp`) so request-middleware callers can decide whether to
 * block on a refresh without reaching back into the store.
 */
export function createTokenRefresher(options: RefreshMiddlewareOptions): TokenRefresher {
  let refreshInFlight: Promise<string | null> | null = null;
  // Cached expiry (unix seconds) for the most recently observed token.
  // Seeded from `getAccessToken` on first expiry check so an already-
  // authenticated session (bootstrap's /me raw fetch, restored from a
  // prior tab) still participates in proactive refresh.
  let cachedExp: number | null = null;
  let cachedForToken: string | undefined;
  let lastFailureKind: RefreshFailureKind = 'none';

  /** Server said no: drop the session and notify. */
  function endSession(): void {
    lastFailureKind = 'rejected';
    options.clearSession();
    cachedExp = null;
    cachedForToken = undefined;
    options.onSessionExpired?.();
  }

  function syncCachedExpFromStore(): void {
    const current = options.getAccessToken();
    if (current !== cachedForToken) {
      cachedForToken = current;
      cachedExp = current ? decodeTokenExp(current) : null;
    }
  }

  const refreshAccessToken = function refreshAccessToken(): Promise<string | null> {
    if (refreshInFlight) return refreshInFlight;

    refreshInFlight = (async () => {
      try {
        const res = await fetch(`${options.authApiBaseUrl}/auth/refresh`, {
          method: 'POST',
          credentials: 'include',
        });
        if (!res.ok) {
          endSession();
          return null;
        }
        const body = (await res.json()) as { accessToken?: string };
        const token = body.accessToken;
        if (!token) {
          endSession();
          return null;
        }
        options.setAccessToken(token);
        cachedExp = decodeTokenExp(token);
        cachedForToken = token;
        lastFailureKind = 'none';
        return token;
      } catch (err) {
        if (isTransportFailure(err)) {
          // We never reached the server, so the session is unproven, not
          // invalid. Leave it alone and report why there is no token.
          lastFailureKind = 'network';
          return null;
        }
        endSession();
        return null;
      }
    })();

    // Hold the resolved promise for a grace window so concurrent 401s
    // that arrive within REFRESH_GRACE_MS all reuse the same token
    // instead of each triggering another refresh (which would
    // invalidate the single-use refresh cookie and cascade the session
    // into a logged-out state).
    const captured = refreshInFlight;
    void captured.finally(() => {
      setTimeout(() => {
        if (refreshInFlight === captured) refreshInFlight = null;
      }, REFRESH_GRACE_MS);
    });

    return refreshInFlight;
  } as TokenRefresher;

  refreshAccessToken.isExpiringSoon = (): boolean => {
    syncCachedExpFromStore();
    if (!cachedForToken) return true; // no token => must refresh
    if (cachedExp === null) return false; // opaque / non-JWT token — don't probe
    const nowSec = Math.floor(Date.now() / 1000);
    return cachedExp - nowSec <= EXPIRY_BUFFER_SECONDS;
  };

  refreshAccessToken.currentExp = (): number | null => {
    syncCachedExpFromStore();
    return cachedExp;
  };

  refreshAccessToken.lastFailure = (): RefreshFailureKind => lastFailureKind;

  return refreshAccessToken;
}

/** The request's path, falling back to the raw string if it will not parse. */
function pathOf(url: string): string {
  try {
    return new URL(url).pathname;
  } catch {
    return url;
  }
}

/**
 * Whether the request is to one of the API's unauthenticated operations.
 *
 * These endpoints carry their own capability — an opaque share or invite
 * token in the path, a credential in the body, or nothing at all for the
 * health probe — and a bearer changes nothing about what they return.
 *
 * Two things follow from calling one with a refresh in front of it.
 * Every `/auth/*` call would recurse, since a refresh is itself an
 * `/auth/*` call. And nobody following a shared link has a session, so
 * the first paint of a public page would wait on a round trip to
 * auth-api that is certain to be refused, spending the caller's
 * `/auth/refresh` budget on the way. Behind a shared egress address that
 * budget belongs to signed-in colleagues, who get logged out to pay for
 * it.
 *
 * The list lives in the SDK because the paths are the API's, not any one
 * app's: flow-web, accounts-web and the CLI all call through here and
 * would otherwise each need their own copy to stay correct. What keeps
 * it honest is the spec: `public-paths.test.ts` walks every operation in
 * the committed OpenAPI document and requires this function to agree
 * with the security requirement each one declares.
 */
function skipsRefresh(url: string): boolean {
  const segments = pathOf(url).split('/').filter(Boolean);
  const [first, second, third] = segments;
  // Signing in, signing out, and everything leading up to either. None
  // of it needs a token and the refresh endpoint lives here too.
  if (first === 'auth') return true;
  switch (segments.length) {
    case 1:
      return first === 'health';
    case 2:
      // Avatars are served to <img src>, which sends no headers.
      // /public/lenses/{token} is length 3; /public/... covers the rest.
      return first === 'public' || first === 'avatars';
    case 3:
      if (first === 'public') return true;
      if (first === 'share' && second === 'cal') return true;
      // The provider redirects the browser back here; there is no
      // session yet on the leg that completes the connection.
      if (first === 'oauth' && second === 'callback') return true;
      // /invites/{token}/info previews an invite before sign-in;
      // /invites/{token}/accept needs an account and is not public.
      return first === 'invites' && third === 'info';
    default:
      return false;
  }
}

/**
 * Creates an openapi-fetch response middleware that intercepts 401
 * responses, attempts a token refresh via the provided `refreshFn`,
 * and replays the original request with the new token.
 *
 * Auth-related paths are excluded to prevent infinite loops, and the
 * API's public paths because a 401 from one of those says the token in
 * the URL is wrong, which no refresh can fix.
 *
 * This middleware is the reactive backstop for unexpected 401s. Pair
 * it with {@link createAuthRequestMiddleware} to avoid the console
 * noise caused by letting a stale-token request ever reach the server
 * in the first place.
 */
export function createRefreshMiddleware(refreshFn: () => Promise<string | null>): {
  onResponse: (ctx: { request: Request; response: Response }) => Promise<Response>;
} {
  return {
    async onResponse({ request, response }) {
      if (response.status !== 401) return response;

      if (skipsRefresh(request.url)) return response;

      const newToken = await refreshFn();
      if (!newToken) return response;

      // Replay original request with the rotated token. Clone because
      // Request bodies are single-use streams.
      const replay = new Request(request, {
        headers: new Headers(request.headers),
      });
      replay.headers.set('Authorization', `Bearer ${newToken}`);
      return fetch(replay);
    },
  };
}

/**
 * Options for {@link createAuthRequestMiddleware}.
 */
export interface AuthRequestMiddlewareOptions {
  /** Returns the current access token (same source as the store). */
  getAccessToken: () => string | undefined;
  /**
   * Refresher handle returned by {@link createTokenRefresher}. The
   * middleware calls `isExpiringSoon()` to decide whether to await a
   * refresh before the outbound request.
   */
  refresher: TokenRefresher;
}

/**
 * Creates an openapi-fetch **request** middleware that proactively
 * refreshes the access token when it is missing or within
 * {@link EXPIRY_BUFFER_SECONDS} of expiry, BEFORE the request is sent.
 *
 * The goal is to prevent the browser from ever seeing a 401 response
 * to a routine authenticated request during normal navigation. A
 * reactive-only refresh path (response middleware) cannot achieve this
 * because the 401 response is already logged to the DevTools console
 * by the time the middleware replays the request — the replay
 * succeeds, but the console stays noisy.
 *
 * Auth endpoints (`/auth/refresh`, `/auth/login`, `/auth/register`,
 * `/auth/logout`) are skipped to avoid recursive refresh loops, and the
 * API's public endpoints are skipped and left without a bearer — see
 * {@link isPublicPath} for why that matters to people who never sign in.
 */
export function createAuthRequestMiddleware(options: AuthRequestMiddlewareOptions): {
  onRequest: (ctx: { request: Request }) => Promise<Request>;
} {
  return {
    async onRequest({ request }) {
      // Public operations need no bearer, and asking for one costs a
      // refused round trip on every unauthenticated view.
      if (skipsRefresh(request.url)) return request;

      // If the current token is missing or about to expire, block the
      // request on a refresh so the outbound call carries a fresh
      // bearer. When there is no token at all we still attempt the
      // refresh — a valid httpOnly refresh cookie will mint one; an
      // invalid one returns null and we let the call proceed so the
      // response middleware can surface the terminal 401.
      if (options.refresher.isExpiringSoon()) {
        await options.refresher();
      }

      const token = options.getAccessToken();
      if (token) {
        request.headers.set('Authorization', `Bearer ${token}`);
      }
      return request;
    },
  };
}
