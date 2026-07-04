/**
 * /login -- redirects to the centralised accounts-web login page.
 * Kept as a route so existing links / bookmarks still work.
 */

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
    if (typeof raw === 'string' && raw.startsWith('/') && !raw.startsWith('//')) {
      return { returnTo: raw };
    }
    return {};
  },
  component: LoginRedirect,
});
