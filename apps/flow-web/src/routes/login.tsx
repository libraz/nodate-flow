/**
 * /login -- redirects to the centralised accounts-web login page.
 * Kept as a route so existing links / bookmarks still work.
 */

import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useEffect } from 'react';

const accountsUrl =
  (import.meta.env.VITE_ACCOUNTS_WEB_URL as string | undefined) ?? 'http://localhost:5175';

function LoginRedirect(): ReactElement | null {
  useEffect(() => {
    window.location.href = `${accountsUrl}/login?redirect=${encodeURIComponent(window.location.origin)}`;
  }, []);
  return null;
}

export const Route = createFileRoute('/login')({
  component: LoginRedirect,
});
