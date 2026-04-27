/**
 * Verify the admins table is wrapped in an overflow-x scroll container so
 * narrow viewports get a horizontal scrollbar instead of clipping wide cells.
 *
 * happy-dom does not implement getComputedStyle for inline styles, so we
 * read `wrapperEl.style.overflowX` directly — which works because the wrapper
 * uses `style={{ overflowX: 'auto' }}` (the same source the production CSS
 * cascade resolves to).
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import enAdmin from '../../../../../locales/en/admin.json';

const sdkMocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  delete: vi.fn(),
}));

vi.mock('../../../../lib/sdk', () => ({
  sdk: {
    // biome-ignore lint/style/useNamingConvention: openapi-fetch HTTP method key
    GET: sdkMocks.get,
    // biome-ignore lint/style/useNamingConvention: openapi-fetch HTTP method key
    POST: sdkMocks.post,
    // biome-ignore lint/style/useNamingConvention: openapi-fetch HTTP method key
    DELETE: sdkMocks.delete,
  },
}));

vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => () => ({ options: {} }),
}));

const { AdminsPage } = await import('../admins');

function buildI18n(): ReturnType<typeof i18n.createInstance> {
  const instance = i18n.createInstance();
  void instance
    .use(ICU)
    .use(initReactI18next)
    .init({
      lng: 'en',
      fallbackLng: 'en',
      defaultNS: 'admin',
      ns: ['admin'],
      resources: { en: { admin: enAdmin } },
      interpolation: { escapeValue: false },
      react: { useSuspense: false },
    });
  return instance;
}

function mount(): void {
  const testI18n = buildI18n();
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });

  function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return (
      <QueryClientProvider client={qc}>
        <I18nextProvider i18n={testI18n}>{children}</I18nextProvider>
      </QueryClientProvider>
    );
  }

  render(<AdminsPage />, { wrapper: Wrapper });
}

beforeAll(() => {
  if (typeof window !== 'undefined') {
    Object.defineProperty(window, 'innerWidth', {
      configurable: true,
      writable: true,
      value: 600,
    });
    if (typeof window.matchMedia !== 'function') {
      Object.defineProperty(window, 'matchMedia', {
        configurable: true,
        writable: true,
        value: (query: string) => ({
          matches: false,
          media: query,
          onchange: null,
          addListener: () => undefined,
          removeListener: () => undefined,
          addEventListener: () => undefined,
          removeEventListener: () => undefined,
          dispatchEvent: () => false,
        }),
      });
    }
  }
});

beforeEach(() => {
  sdkMocks.get.mockReset();
  sdkMocks.post.mockReset();
  sdkMocks.delete.mockReset();
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('admins responsive table wrapper', () => {
  it('wraps the admins table in a container with overflow-x: auto', async () => {
    sdkMocks.get.mockResolvedValueOnce({
      data: {
        items: [
          {
            id: 'a-1',
            email: 'admin@example.test',
            displayName: 'Admin One',
            grantedAt: 1700000000,
            grantedBy: 'root',
          },
        ],
        total: 1,
      },
    });

    mount();

    const table = await waitFor(() => {
      const t = screen.queryByRole('table');
      if (!t) throw new Error('table not yet rendered');
      return t as HTMLTableElement;
    });

    const wrapper = table.parentElement;
    expect(wrapper).not.toBeNull();
    expect((wrapper as HTMLDivElement).style.overflowX).toBe('auto');
  });
});
