/**
 * On app mount, attempts to re-establish a session from the httpOnly
 * nd_rt refresh cookie:
 *
 *   1. POST /auth/refresh
 *   2. on success, GET /auth/me to populate the user profile
 *   3. on any failure, leave the store empty (status: 'unauthenticated')
 *
 * Idempotent under React 19 StrictMode double-invoke (the refresh helper
 * memoizes its in-flight promise).
 */

import { useEffect, useState } from 'react';

import type { AuthUser } from '../features/auth/auth-store';
import { authStore } from '../features/auth/auth-store';
import { refreshAccessToken, sdk } from '../lib/sdk';

export type AuthBootstrapStatus = 'loading' | 'authenticated' | 'unauthenticated';

interface BootstrapResult {
  status: AuthBootstrapStatus;
}

interface MeResponse {
  id: string;
  email: string;
  displayName: string;
  locale: string;
  themePreference: string;
  isInstanceAdmin: boolean;
}

let bootstrapPromise: Promise<AuthBootstrapStatus> | null = null;

async function runBootstrap(): Promise<AuthBootstrapStatus> {
  const token = await refreshAccessToken();
  if (!token) return 'unauthenticated';
  const { data, error } = await sdk.GET('/auth/me');
  if (error || !data) {
    authStore.getState().clearSession();
    return 'unauthenticated';
  }
  const me = data as MeResponse;
  const user: AuthUser = {
    id: me.id,
    email: me.email,
    displayName: me.displayName,
    locale: me.locale,
    themePreference: me.themePreference,
    isInstanceAdmin: me.isInstanceAdmin,
  };
  authStore.getState().setSession(token, user);
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
