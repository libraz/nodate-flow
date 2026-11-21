/**
 * /signup -- redirects to the centralised accounts-web signup page.
 * Kept as a route so existing links / bookmarks still work.
 */

import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useEffect } from 'react';

import { accountsWebUrl } from '../lib/accounts-web-url';

function SignupRedirect(): ReactElement | null {
  useEffect(() => {
    window.location.href = `${accountsWebUrl('/signup')}?redirect=${encodeURIComponent(window.location.origin)}`;
  }, []);
  return null;
}

export const Route = createFileRoute('/signup')({
  component: SignupRedirect,
});
