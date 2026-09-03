/**
 * The import "Configuration JSON" field says what it is for, and says
 * that credentials are not it.
 *
 * `import_jobs.config_json` is a plaintext column that the job-read
 * endpoints return and backups copy verbatim. The field that writes it
 * is a free-form JSON box on a screen about connecting GitHub and Jira,
 * which is close to a prompt to paste a personal access token. The
 * server refuses credential-shaped keys, but a refusal arrives after the
 * token has already been pasted — so the field has to say so first, and
 * point at where credentials do belong.
 *
 * These tests hold three things that are each invisible when they break:
 * the caution is on screen before anything is submitted, the route to
 * the encrypted place is reachable from it, and the server's two
 * refusals arrive as two different messages rather than one.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import i18next from 'i18next';
import ICU from 'i18next-icu';
import type { ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import enSettings from '../../../../locales/en/settings.json';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  toastShow: vi.fn(),
}));

vi.mock('../../../lib/sdk', () => ({
  sdk: { GET: mocks.get, POST: mocks.post },

  authSdk: { GET: mocks.get, POST: mocks.post },
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: mocks.toastShow },
}));

vi.mock('@tanstack/react-router', () => ({
  getRouteApi: () => ({ useParams: () => ({ id: 'ws-1' }) }),
  Link: ({ to, children }: { to: string; children: ReactNode }) => <a href={to}>{children}</a>,
}));

vi.mock('../../projects/api', () => ({
  useProjectsQuery: () => ({
    data: [{ id: 'prj-1', name: 'Alpha' }],
    response: new Response(null, { status: 200 }),
  }),
  response: new Response(null, { status: 200 }),
}));

import DataSettingsPage from '../data-settings-page';

/** The shipped copy, so the assertions are about what a reader sees. */
const CONFIG_COPY = enSettings.settings.data.imports.create.config;

const UNKNOWN_KEY_MESSAGE = 'This source does not accept that key';
const SECRET_MESSAGE = 'Do not put credentials in the configuration';

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
          // The real bundle: the copy under test is the copy that ships.
          settings: enSettings,
          errors: {
            'WS.IMPORT.CONFIG_KEY_UNKNOWN': UNKNOWN_KEY_MESSAGE,
            'WS.IMPORT.CONFIG_SECRET_REJECTED': SECRET_MESSAGE,
          },
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

/** Opens the create form and returns a user-event session. */
async function openCreateForm(): Promise<ReturnType<typeof userEvent.setup>> {
  const user = userEvent.setup();
  renderPage();
  await user.click(screen.getByRole('button', { name: enSettings.settings.data.imports.new }));
  return user;
}

beforeEach(() => {
  mocks.get.mockReset().mockResolvedValue({
    data: { items: [] },
    error: null,
    response: new Response(null, { status: 200 }),
  });
  mocks.post.mockReset();
  mocks.toastShow.mockReset();
});

describe('import configuration field — credential guidance', () => {
  it('warns against credentials before anything is submitted', async () => {
    await openCreateForm();

    expect(screen.getByText(CONFIG_COPY.no_credentials)).toBeDefined();
    // Nothing has been sent, so the warning cannot be coming from a
    // server rejection.
    expect(mocks.post).not.toHaveBeenCalled();
  });

  it('points at the place credentials do belong', async () => {
    await openCreateForm();

    const link = screen.getByRole('link', { name: CONFIG_COPY.credentials_where });
    // Integration settings, where connections are stored encrypted.
    expect(link.getAttribute('href')).toBe('/settings/integrations');
  });

  it('describes the field as settings that are stored as written', async () => {
    await openCreateForm();

    expect(screen.getByText(CONFIG_COPY.hint)).toBeDefined();
  });

  it('attaches a rejected credential to the field, not to a toast', async () => {
    mocks.post.mockResolvedValue({
      data: undefined,
      error: {
        type: 'WS.IMPORT.CONFIG_SECRET_REJECTED',
        detail: 'importer: configuration must not carry credentials: token',
        status: 400,
      },
      response: new Response(null, { status: 400 }),
    });
    const user = await openCreateForm();

    await user.click(
      screen.getByRole('button', { name: enSettings.settings.data.imports.create.submit }),
    );

    await waitFor(() => {
      expect(screen.getByText(SECRET_MESSAGE)).toBeDefined();
    });
    // A toast leaves; the field the reader has to edit is where the
    // message has to stay.
    expect(mocks.toastShow).not.toHaveBeenCalled();
    // And the server's English detail never reaches the reader.
    expect(screen.queryByText(/must not carry credentials: token/)).toBeNull();
  });

  it('tells an unknown key apart from a credential', async () => {
    mocks.post.mockResolvedValue({
      data: undefined,
      error: {
        type: 'WS.IMPORT.CONFIG_KEY_UNKNOWN',
        detail: 'importer: unknown configuration key: repo',
        status: 400,
      },
      response: new Response(null, { status: 400 }),
    });
    const user = await openCreateForm();

    await user.click(
      screen.getByRole('button', { name: enSettings.settings.data.imports.create.submit }),
    );

    await waitFor(() => {
      expect(screen.getByText(UNKNOWN_KEY_MESSAGE)).toBeDefined();
    });
    // The two refusals ask for different things — remove a key, or move
    // a secret. Collapsing them into one message sends half the readers
    // to fix the wrong thing.
    expect(screen.queryByText(SECRET_MESSAGE)).toBeNull();
  });

  it('still uses a toast for failures that are not about this field', async () => {
    mocks.post.mockResolvedValue({
      data: undefined,
      error: { type: 'WS.IMPORT.ALREADY_RUNNING', detail: 'already running', status: 409 },
      response: new Response(null, { status: 400 }),
    });
    const user = await openCreateForm();

    await user.click(
      screen.getByRole('button', { name: enSettings.settings.data.imports.create.submit }),
    );

    await waitFor(() => {
      expect(mocks.toastShow).toHaveBeenCalled();
    });
  });
});
