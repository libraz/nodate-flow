import ConfirmProvider from '@nodate-flow/ui/primitives/confirm';
import ToastProvider from '@nodate-flow/ui/primitives/toast';
import { RouterProvider } from '@tanstack/react-router';
import { type ReactElement, StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { ErrorBoundary } from 'react-error-boundary';

import { I18nProvider } from './providers/i18n-provider';
import { QueryProvider } from './providers/query-provider';
import { ThemeProvider } from './providers/theme-provider';
import { router } from './router/router';
import './styles/main.css';

// One-shot cleanup of localStorage keys left over from the retired
// time-web app (decommissioned in commit 4dbfd58). Runs on every boot;
// removeItem on a missing key is a no-op so there is no cost after the
// first load that finds nothing to delete. Leave this in for one or
// two releases, then retire.
const LEGACY_LOCALSTORAGE_KEYS = [
  'tt_theme',
  'tt_activeCalendarIds',
  'tt_colorMode',
  'tt_token',
  'lastExternalReferrerTime',
  'lastExternalReferrer',
];
try {
  for (const k of LEGACY_LOCALSTORAGE_KEYS) localStorage.removeItem(k);
} catch {
  // SSR / restricted storage; safe to ignore
}

/**
 * Pre-i18n bootstrap error fallback. This text is intentionally hardcoded
 * English because the i18n provider has not been initialized when this
 * component renders (it sits outside <I18nProvider>).
 */
function FatalError(): ReactElement {
  return (
    <main style={{ padding: '2rem' }}>
      <p>Failed to initialize the application.</p>
    </main>
  );
}

const container = document.getElementById('root');
if (!container) {
  throw new Error('root container missing');
}

// F3: auth bootstrap runs inside the `_authenticated` layout route via
// useAuthBootstrap; no top-level AuthProvider is required because the
// auth slice is a vanilla zustand store accessible to non-React modules.
createRoot(container).render(
  <StrictMode>
    <ErrorBoundary fallback={<FatalError />}>
      <I18nProvider>
        <QueryProvider>
          <ThemeProvider>
            <RouterProvider router={router} />
            <ConfirmProvider />
            <ToastProvider />
          </ThemeProvider>
        </QueryProvider>
      </I18nProvider>
    </ErrorBoundary>
  </StrictMode>,
);
