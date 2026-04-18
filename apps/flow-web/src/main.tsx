import ConfirmProvider from '@nodate-flow/ui/primitives/confirm';
import { RouterProvider } from '@tanstack/react-router';
import { type ReactElement, StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { ErrorBoundary } from 'react-error-boundary';

import { I18nProvider } from './providers/i18n-provider';
import { QueryProvider } from './providers/query-provider';
import { ThemeProvider } from './providers/theme-provider';
import { router } from './router/router';
import './styles/main.css';

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
          </ThemeProvider>
        </QueryProvider>
      </I18nProvider>
    </ErrorBoundary>
  </StrictMode>,
);
