import type React from 'react';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { ErrorBoundary } from 'react-error-boundary';

import App from './app';
import { initI18n } from './i18n';
import './styles/main.css';

initI18n();

function FatalError(): React.ReactElement {
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

createRoot(container).render(
  <StrictMode>
    <ErrorBoundary fallback={<FatalError />}>
      <App />
    </ErrorBoundary>
  </StrictMode>,
);
