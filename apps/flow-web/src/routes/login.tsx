/**
 * /login -- redirects to the centralised accounts-web login page.
 * Kept as a route so existing links / bookmarks still work.
 */

import { isSafeRedirect } from '@nodate-flow/sdk';
import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useEffect } from 'react';

const accountsUrl =
  (import.meta.env.VITE_ACCOUNTS_WEB_URL as string | undefined) ?? 'http://localhost:5175';

export interface LoginSearch {
  returnTo?: string;
}

function LoginRedirect(): ReactElement | null {
  const { returnTo } = Route.useSearch();
  useEffect(() => {
    const redirectURL = new URL(returnTo ?? '/', window.location.origin).toString();
    window.location.href = `${accountsUrl}/login?redirect=${encodeURIComponent(redirectURL)}`;
  }, [returnTo]);
  return null;
}

export const Route = createFileRoute('/login')({
  validateSearch: (search: Record<string, unknown>): LoginSearch => {
    const raw = search.returnTo;
    // returnTo is echoed back to us after sign-in, so it must resolve
    // inside this app. No allowlist: a cross-origin return target is
    // never legitimate here.
    if (typeof raw === 'string' && isSafeRedirect(raw, window.location.origin)) {
      return { returnTo: raw };
    }
    return {};
  },
  component: LoginRedirect,
});
