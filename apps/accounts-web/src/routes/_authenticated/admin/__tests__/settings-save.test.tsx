import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import enAdmin from '../../../../../locales/en/admin.json';

const settingKeys = {
  registrationOpen: 'registration_open',
  mfaEnforcement: 'mfa_enforcement',
  maxWorkspacesPerUser: 'max_workspaces_per_user',
  maxMembersPerWorkspace: 'max_members_per_workspace',
} as const;

const sdkMocks = vi.hoisted(() => ({
  get: vi.fn(),
  patch: vi.fn(),
}));

vi.mock('../../../../lib/sdk', () => ({
  sdk: {
    // biome-ignore lint/style/useNamingConvention: openapi-fetch HTTP method key
    GET: sdkMocks.get,
    // biome-ignore lint/style/useNamingConvention: openapi-fetch HTTP method key
    PATCH: sdkMocks.patch,
  },
}));

vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => () => ({ options: {} }),
}));

const { SettingsPage } = await import('../settings');

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

  render(<SettingsPage />, { wrapper: Wrapper });
}

beforeEach(() => {
  sdkMocks.get.mockReset();
  sdkMocks.patch.mockReset();
});

describe('admin settings save', () => {
  it('sends backend setting keys and the three-state MFA value', async () => {
    sdkMocks.get.mockResolvedValueOnce({
      data: {
        items: [
          { key: 'registration_open', value: 'true' },
          { key: 'mfa_enforcement', value: 'optional' },
          { key: 'max_workspaces_per_user', value: '10' },
          { key: 'max_members_per_workspace', value: '25' },
        ],
      },
    });
    sdkMocks.patch.mockResolvedValueOnce({ data: { ok: true } });

    mount();

    await screen.findByRole('heading', { name: enAdmin.settings.title });
    await userEvent.selectOptions(
      screen.getByLabelText(new RegExp(enAdmin.settings.mfa_enforcement)),
      'required',
    );
    await userEvent.click(screen.getByRole('button', { name: enAdmin.settings.save }));

    await waitFor(() => expect(sdkMocks.patch).toHaveBeenCalledTimes(1));
    expect(sdkMocks.patch).toHaveBeenCalledWith('/admin/settings', {
      body: {
        settings: {
          [settingKeys.registrationOpen]: 'true',
          [settingKeys.mfaEnforcement]: 'required',
          [settingKeys.maxWorkspacesPerUser]: '10',
          [settingKeys.maxMembersPerWorkspace]: '25',
        },
      },
    });
  });
});
