/**
 * renderWithProviders — custom render function that wraps the
 * component under test with the same provider tree the app uses:
 *
 * - QueryClientProvider (fresh QueryClient per test, retry disabled)
 * - I18nextProvider (in-memory, English locale)
 *
 * This ensures component tests exercise the real provider wiring
 * without depending on the full app shell or a running backend.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { type RenderOptions, type RenderResult, render } from '@testing-library/react';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';

/* ── Minimal i18n instance for tests ─────────────────────────── */

let testI18nInstance: ReturnType<typeof i18n.createInstance> | null = null;

/**
 * Initialise a test-only i18next instance with a minimal resource
 * bundle. Components that call `t('some.key')` will receive the key
 * back (passthrough), which is sufficient for structural assertions
 * without coupling tests to specific copy.
 */
function ensureTestI18n(): ReturnType<typeof i18n.createInstance> {
  if (testI18nInstance) return testI18nInstance;

  // Create a fresh instance so we never collide with the app singleton.
  const instance = i18n.createInstance();
  void instance
    .use(ICU)
    .use(initReactI18next)
    .init({
      lng: 'en',
      fallbackLng: 'en',
      defaultNS: 'common',
      ns: ['common', 'inbox', 'settings', 'ai', 'constraints', 'errors'],
      resources: {},
      interpolation: { escapeValue: false },
      // Return the key itself when no translation is found — makes
      // assertions readable without maintaining a test-only copy file.
      parseMissingKeyHandler: (key: string) => key,
      react: { useSuspense: false },
    });

  testI18nInstance = instance;
  return instance;
}

/* ── Provider wrapper ────────────────────────────────────────── */

export interface RenderWithProvidersOptions extends Omit<RenderOptions, 'wrapper'> {
  /** Override the default QueryClient for this test. */
  queryClient?: QueryClient;
}

/**
 * Render a React element wrapped in the standard provider stack.
 *
 * Each call creates a fresh QueryClient (retry disabled, throwOnError
 * off) so tests are fully isolated from one another.
 *
 * @example
 * ```tsx
 * const { getByRole } = renderWithProviders(<MyComponent />);
 * expect(getByRole('heading')).toHaveTextContent('Hello');
 * ```
 */
export function renderWithProviders(
  ui: ReactElement,
  options: RenderWithProvidersOptions = {},
): RenderResult & { queryClient: QueryClient } {
  const {
    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false, throwOnError: false },
        mutations: { retry: false, throwOnError: false },
      },
    }),
    ...renderOptions
  } = options;

  const testI18n = ensureTestI18n();

  function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return (
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={testI18n}>{children}</I18nextProvider>
      </QueryClientProvider>
    );
  }

  const result = render(ui, { wrapper: Wrapper, ...renderOptions });

  return { ...result, queryClient };
}
