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

import { refreshAccessToken, sdk } from '../../lib/sdk';
import { type AuthUser, authStore } from './auth-store';

export type AuthBootstrapStatus = 'loading' | 'authenticated' | 'unauthenticated';

interface BootstrapResult {
  status: AuthBootstrapStatus;
}

let bootstrapPromise: Promise<AuthBootstrapStatus> | null = null;

async function runBootstrap(): Promise<AuthBootstrapStatus> {
  const token = await refreshAccessToken();
  if (!token) return 'unauthenticated';
  const { data, error } = await sdk.GET('/me');
  if (error || !data) {
    authStore.getState().clearSession();
    return 'unauthenticated';
  }
  const user: AuthUser = {
    id: data.id,
    email: data.email,
    displayName: data.displayName,
    locale: data.locale,
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
    bootstrapPromise.then((next) => {
      if (!cancelled) setStatus(next);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  return { status };
}
