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

/**
 * Pre-i18n bootstrap error fallback. This text is intentionally hardcoded
 * English because the i18n provider has not been initialized when this
 * component renders (it sits outside <I18nProvider>).
 */
function FatalError(): ReactElement {
  return (
    <main style={{ padding: 'var(--nf-space-8)' }}>
      <p>Failed to initialize the application.</p>
    </main>
  );
}

const container = document.getElementById('root');
if (!container) {
  throw new Error('root container missing');
}

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
