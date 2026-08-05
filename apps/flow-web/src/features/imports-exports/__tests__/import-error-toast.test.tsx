/**
 * Import failure messaging on the data settings page.
 *
 * The import create / cancel handlers used to surface `err.message`,
 * which is the server's `detail` string — English, written for
 * operators, and shown regardless of the reader's locale. Routing them
 * through `formatApiError` resolves the error code against the `errors`
 * namespace instead, which is the same treatment the export handler on
 * this page already gets.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import i18next from 'i18next';
import ICU from 'i18next-icu';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  toastShow: vi.fn(),
}));

vi.mock('../../../lib/sdk', () => ({
  // openapi-fetch keys its client by HTTP method, hence the caps.
  sdk: { GET: mocks.get, POST: mocks.post },
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: mocks.toastShow },
}));

vi.mock('@tanstack/react-router', () => ({
  getRouteApi: () => ({ useParams: () => ({ id: 'ws-1' }) }),
}));

vi.mock('../../projects/api', () => ({
  useProjectsQuery: () => ({ data: [{ id: 'prj-1', name: 'Alpha' }] }),
}));

import DataSettingsPage from '../data-settings-page';

/**
 * A real `errors` bundle, because the whole assertion is that the code
 * resolves to localized copy — with an empty bundle `formatApiError`
 * falls back to the server detail and the test could not tell the fix
 * from the bug.
 */
function buildI18n(): ReturnType<typeof i18next.createInstance> {
  const instance = i18next.createInstance();
  void instance
    .use(ICU)
    .use(initReactI18next)
    .init({
      lng: 'en',
      fallbackLng: 'en',
      defaultNS: 'common',
      ns: ['common', 'settings', 'errors'],
      resources: {
        en: {
          common: {},
          settings: {
            settings: {
              data: {
                imports: {
                  new: 'New import',
                  create: {
                    submit: 'Start import',
                    error: 'Fallback import failure',
                    source: 'Source',
                    project: { label: 'Project', placeholder: 'No project' },
                    config: { label: 'Config', hint: 'Optional JSON' },
                  },
                },
              },
            },
          },
          errors: { 'VALIDATION.BODY.INVALID': 'Localized validation failure' },
        },
      },
      interpolation: { escapeValue: false },
      parseMissingKeyHandler: (key: string, defaultValue?: string) =>
        defaultValue !== undefined ? defaultValue : key,
      react: { useSuspense: false },
    });
  return instance;
}

function renderPage(): void {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false },
      mutations: { retry: false, throwOnError: false },
    },
  });
  render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={buildI18n()}>
        <DataSettingsPage />
      </I18nextProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  mocks.get.mockReset().mockResolvedValue({ data: { items: [] }, error: null });
  mocks.post.mockReset();
  mocks.toastShow.mockReset();
});

describe('DataSettingsPage — import failure messaging', () => {
  it('shows the localized error for a failed import, not the server detail', async () => {
    const user = userEvent.setup();
    mocks.post.mockResolvedValue({
      data: undefined,
      error: {
        type: 'VALIDATION.BODY.INVALID',
        detail: 'configJson must be an object',
        status: 422,
      },
    });
    renderPage();

    await user.click(screen.getByRole('button', { name: 'New import' }));
    await user.click(screen.getByRole('button', { name: 'Start import' }));

    await waitFor(() => {
      expect(mocks.toastShow).toHaveBeenCalled();
    });
    const toast = mocks.toastShow.mock.calls[0]?.[0] as { tone: string; message: string };
    expect(toast.tone).toBe('danger');
    expect(toast.message).toBe('Localized validation failure');
    expect(toast.message).not.toBe('configJson must be an object');
  });

  it('falls back to the page-level copy when the failure carries no code', async () => {
    const user = userEvent.setup();
    mocks.post.mockRejectedValue('not an error object');
    renderPage();

    await user.click(screen.getByRole('button', { name: 'New import' }));
    await user.click(screen.getByRole('button', { name: 'Start import' }));

    await waitFor(() => {
      expect(mocks.toastShow).toHaveBeenCalled();
    });
    const toast = mocks.toastShow.mock.calls[0]?.[0] as { message: string };
    expect(toast.message).toBe('Fallback import failure');
  });
});
