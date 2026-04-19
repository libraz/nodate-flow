import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useEffect } from 'react';

const ACCOUNTS_WEB_URL = import.meta.env.VITE_ACCOUNTS_WEB_URL ?? 'http://localhost:5175';

function LoginRedirect(): ReactElement | null {
  useEffect(() => {
    window.location.href = `${ACCOUNTS_WEB_URL}/login?redirect=${encodeURIComponent(window.location.origin)}`;
  }, []);
  return null;
}

export const Route = createFileRoute('/login')({
  component: LoginRedirect,
});
