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

import { apiRequest, refreshAccessToken } from '../lib/api-client';
import type { AuthUser } from '../stores/auth-store';
import { authStore } from '../stores/auth-store';

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
}

let bootstrapPromise: Promise<AuthBootstrapStatus> | null = null;

async function runBootstrap(): Promise<AuthBootstrapStatus> {
  const token = await refreshAccessToken();
  if (!token) return 'unauthenticated';
  const result = await apiRequest<MeResponse>('/auth/me');
  if (result.error || !result.data) {
    authStore.getState().clearSession();
    return 'unauthenticated';
  }
  const user: AuthUser = {
    id: result.data.id,
    email: result.data.email,
    displayName: result.data.displayName,
    locale: result.data.locale,
    themePreference: result.data.themePreference,
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
