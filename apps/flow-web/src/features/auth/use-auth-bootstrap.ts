/**
 * On app mount, attempts to re-establish a session from the httpOnly
 * nf_rt refresh cookie:
 *
 *   1. POST /auth/refresh
 *   2. on success, GET /me to populate the user profile
 *   3. on a refusal, leave the store empty (status: 'unauthenticated')
 *   4. on a transport failure, keep whatever session exists and report
 *      'offline' so the caller can offer a retry
 *
 * The offline case is kept distinct because the two outcomes call for
 * opposite responses: a refused refresh means sign the user out, while a
 * connection that dropped during load means try again. Treating the
 * second as the first drops the user on the login screen — and takes any
 * unsaved UI state with it — over a blip they never even saw.
 *
 * Idempotent under React 19 StrictMode double-invoke (the refresh helper
 * memoizes its in-flight promise).
 */

import { useCallback, useEffect, useRef, useState } from 'react';

import { i18n, type SupportedLanguage, setLanguage, supportedLanguages } from '../../i18n';
import { authApiRequest } from '../../lib/api';
import { isNetworkError } from '../../lib/api-error';
import { refreshAccessToken } from '../../lib/sdk';
import { queryClient } from '../../providers/query-client';
import { type AuthUser, authStore } from './auth-store';

export type AuthBootstrapStatus = 'loading' | 'authenticated' | 'unauthenticated' | 'offline';

interface BootstrapResult {
  status: AuthBootstrapStatus;
  /**
   * Re-run the probe. Only meaningful while `status` is `'offline'`;
   * the other outcomes are answers and do not need retrying.
   */
  retry: () => void;
}

let bootstrapPromise: Promise<AuthBootstrapStatus> | null = null;

/**
 * Reads the signed-in profile, separating "the server said no" from
 * "the request never got there". Both arrive as a thrown error, and
 * only the first one is an answer about the session.
 */
async function probeSignedInUser() {
  try {
    const me = await authApiRequest(
      (client) => client.GET('/me', {}),
      'Failed to load the signed-in user',
    );
    return { ok: true as const, me };
  } catch (cause) {
    return { ok: false as const, offline: isNetworkError(cause) };
  }
}

async function runBootstrap(): Promise<AuthBootstrapStatus> {
  const token = await refreshAccessToken();
  if (!token) {
    return refreshAccessToken.lastFailure() === 'network' ? 'offline' : 'unauthenticated';
  }
  // Use the typed auth-api requester for the /me probe. The SDK's
  // request middleware reads the bearer from the auth store (which the
  // refresher above has just populated) so we don't have to rebuild
  // the Authorization header by hand.
  const probe = await probeSignedInUser();
  if (!probe.ok) {
    // A transport failure here is the connection dropping between two
    // calls; the refresh above already succeeded, so the session it
    // just minted survives. A refusal from the server is the opposite
    // answer and does end the session.
    if (probe.offline) return 'offline';
    authStore.getState().clearSession();
    return 'unauthenticated';
  }
  const data = probe.me;
  const user: AuthUser = {
    id: data.id,
    email: data.email,
    displayName: data.displayName,
    locale: data.locale,
    timezone: data.timezone,
    country: data.country,
    themePreference: data.themePreference,
    weekStart: data.weekStart,
    isInstanceAdmin: data.isInstanceAdmin,
    avatarUrl: data.avatarUrl ?? null,
  };
  authStore.getState().setSession(token, user);
  // Seed the react-query cache so ThemeProvider can hydrate from the server
  // preference immediately on login, instead of waiting for the first
  // settings page visit.
  queryClient.setQueryData(['me'], data);
  // Sync i18next to the server-side profile locale so the authenticated UI
  // renders in the user's preferred language on first paint. localStorage
  // `nf.lang` is a client-side cache; `profile.locale` is authoritative.
  if (
    (supportedLanguages as readonly string[]).includes(user.locale) &&
    i18n.language !== user.locale
  ) {
    setLanguage(user.locale as SupportedLanguage);
  }
  return 'authenticated';
}

/**
 * Start the probe, or join the one already running.
 *
 * An `offline` outcome is dropped from the memo as soon as it resolves:
 * it is not an answer about the session, and caching it would make every
 * later caller — including a retry — replay the same failure without ever
 * touching the network again.
 */
function startBootstrap(): Promise<AuthBootstrapStatus> {
  if (!bootstrapPromise) {
    const started = runBootstrap().then((status) => {
      if (status === 'offline' && bootstrapPromise === started) {
        bootstrapPromise = null;
      }
      return status;
    });
    bootstrapPromise = started;
  }
  return bootstrapPromise;
}

export function useAuthBootstrap(): BootstrapResult {
  const [status, setStatus] = useState<AuthBootstrapStatus>('loading');
  // Guards against setting state after unmount. A ref rather than an
  // effect-local flag because `probe` is also called from the retry
  // button, outside any effect run.
  const unmountedRef = useRef(false);

  const probe = useCallback((): void => {
    setStatus('loading');
    startBootstrap()
      .then((next) => {
        if (!unmountedRef.current) setStatus(next);
      })
      .catch(() => {
        if (!unmountedRef.current) setStatus('unauthenticated');
      });
  }, []);

  useEffect(() => {
    unmountedRef.current = false;
    probe();
    return () => {
      unmountedRef.current = true;
    };
  }, [probe]);

  return { status, retry: probe };
}
