/**
 * On app mount, attempts to re-establish a session from the httpOnly
 * nf_rt refresh cookie:
 *
 *   1. POST /auth/refresh
 *   2. on success, GET /me to populate the user profile
 *   3. on any failure, leave the store empty (status: 'unauthenticated')
 *
 * Idempotent under React 19 StrictMode double-invoke (the refresh helper
 * memoizes its in-flight promise).
 */

import { useEffect, useState } from 'react';

import { i18n, type SupportedLanguage, setLanguage, supportedLanguages } from '../../i18n';
import { authSdk, refreshAccessToken } from '../../lib/sdk';
import { queryClient } from '../../providers/query-client';
import { type AuthUser, authStore } from './auth-store';

export type AuthBootstrapStatus = 'loading' | 'authenticated' | 'unauthenticated';

interface BootstrapResult {
  status: AuthBootstrapStatus;
}

let bootstrapPromise: Promise<AuthBootstrapStatus> | null = null;

async function runBootstrap(): Promise<AuthBootstrapStatus> {
  const token = await refreshAccessToken();
  if (!token) return 'unauthenticated';
  // Use the typed auth-api SDK for the /me probe. The SDK's request
  // middleware reads the bearer from the auth store (which the
  // refresher above has just populated) so we don't have to rebuild
  // the Authorization header by hand.
  const { data, error, response } = await authSdk.GET('/me', {});
  if (error || !data || !response.ok) {
    authStore.getState().clearSession();
    return 'unauthenticated';
  }
  const user: AuthUser = {
    id: data.id,
    email: data.email,
    displayName: data.displayName,
    locale: data.locale,
    timezone: data.timezone,
    country: data.country,
    themePreference: data.themePreference,
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

export function useAuthBootstrap(): BootstrapResult {
  const [status, setStatus] = useState<AuthBootstrapStatus>('loading');

  useEffect(() => {
    let cancelled = false;
    if (!bootstrapPromise) {
      bootstrapPromise = runBootstrap();
    }
    bootstrapPromise
      .then((next) => {
        if (!cancelled) setStatus(next);
      })
      .catch(() => {
        if (!cancelled) setStatus('unauthenticated');
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return { status };
}
